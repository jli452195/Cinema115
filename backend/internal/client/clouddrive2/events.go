package clouddrive2

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"strm115/backend/internal/client/clouddrive2/pb"
)

// Change 使用令牌可见根内的原生路径；只有明确的上传完成通知才携带 Ready。
type Change struct {
	Type      string
	Path      string
	NewPath   string
	Directory bool
	File      *File
	Ready     bool
}

// Events 在调用方取消或流结束前持续接收；重连与事件分发由共用连接服务负责。
func (c *Client) Events(ctx context.Context, consume func(Change) error) error {
	return c.EventsWithReady(ctx, nil, consume)
}

// EventsWithReady 在首帧接收成功后确认连接，并将原始挂载路径映射为令牌可见路径。
func (c *Client) EventsWithReady(ctx context.Context, connected func(), consume func(Change) error) error {
	if consume == nil {
		return fmt.Errorf("%w：缺少 CD2 事件处理函数", ErrInput)
	}
	requestCtx, requestCancel := c.request(ctx)
	info, err := c.rpc.GetApiTokenInfo(requestCtx, &pb.StringValue{Value: c.token})
	requestCancel()
	if err != nil {
		return c.wrap("读取事件令牌根目录", err)
	}
	root, err := CleanPath(info.GetRootDir())
	if err != nil {
		return fmt.Errorf("读取事件令牌根目录: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	values, _ := metadata.FromOutgoingContext(ctx)
	values = values.Copy()
	values.Set("authorization", "Bearer "+c.token)
	ctx = metadata.NewOutgoingContext(ctx, values)
	stream, err := c.rpc.PushMessage(ctx, &emptypb.Empty{})
	if err != nil {
		return c.wrap("订阅目录变化", err)
	}
	for {
		message, err := stream.Recv()
		if err != nil {
			return c.wrap("接收目录变化", err)
		}
		if connected != nil {
			connected()
			connected = nil
		}
		if change := message.GetFileSystemChange(); change != nil {
			value, ok := fileSystemChange(change, root)
			if !ok {
				continue
			}
			if err := consume(value); err != nil {
				return fmt.Errorf("处理 CD2 目录变化: %w", err)
			}
			continue
		}
		if transfers := message.GetTransferTaskStatus(); transfers != nil {
			for _, upload := range transfers.GetUploadFileStatusChanges() {
				if upload.GetStatusEnum() != pb.UploadFileInfo_Finish {
					continue
				}
				full, ok := eventPath(root, upload.GetDestPath())
				if !ok {
					continue
				}
				size := int64(upload.GetSize())
				if size < 0 {
					continue
				}
				if err := consume(Change{Type: "upload_finished", Path: full, File: &File{Path: full, Size: size}, Ready: true}); err != nil {
					return fmt.Errorf("处理 CD2 上传完成: %w", err)
				}
			}
		}
	}
}

func fileSystemChange(change *pb.FileSystemChange, root string) (Change, bool) {
	full, ok := eventPath(root, change.GetPath())
	if !ok {
		return Change{}, false
	}
	value := Change{Path: full, Directory: change.GetIsDirectory()}
	switch change.GetChangeType() {
	case pb.FileSystemChange_CREATE:
		value.Type = "create"
	case pb.FileSystemChange_DELETE:
		value.Type = "delete"
	case pb.FileSystemChange_RENAME:
		value.Type = "rename"
		value.NewPath, ok = eventPath(root, change.GetNewPath())
		if !ok {
			return Change{}, false
		}
	case pb.FileSystemChange_MODIFY:
		value.Type = "modify"
	default:
		return Change{}, false
	}
	if change.GetTheFile() != nil {
		current, original := value.Path, change.GetPath()
		if value.NewPath != "" {
			current, original = value.NewPath, change.GetNewPath()
		}
		if file, err := fileValue(change.GetTheFile(), original); err == nil {
			if full, ok := eventPath(root, file.Path); ok && full == current {
				file.Path = full
				value.File = &file
			}
		}
	}
	return value, true
}

// 仅使用官方令牌根目录去前缀，不按网盘名称或路径后缀猜测归属。
func eventPath(root, original string) (string, bool) {
	full, err := CleanPath(original)
	if err != nil {
		return "", false
	}
	if root == "/" {
		return full, true
	}
	if full == root {
		return "/", true
	}
	if !strings.HasPrefix(full, root+"/") {
		return "", false
	}
	return strings.TrimPrefix(full, root), true
}
