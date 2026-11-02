package clouddrive2

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"strm115/backend/internal/client/clouddrive2/pb"
)

const MaxDirectoryEntries = 20000

type Client struct {
	conn              *grpc.ClientConn
	rpc               pb.CloudDriveFileSrvClient
	token             string
	operationsMu      sync.Mutex
	writePaths        map[string]bool
	writesChanged     chan struct{}
	directoryVersions map[string]uint64
	version           uint64
}

// New 只创建可复用连接；实际网络请求由带超时的业务调用触发。
func New(config Config, options ...grpc.DialOption) (*Client, error) {
	config, err := Prepare(config)
	if err != nil {
		return nil, err
	}
	if config.Token == "" {
		return nil, fmt.Errorf("%w：请填写 API Token", ErrInput)
	}
	u, _ := url.Parse(config.URL)
	var transport credentials.TransportCredentials = insecure.NewCredentials()
	if u.Scheme == "https" {
		transport = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()})
	}
	dialOptions := []grpc.DialOption{grpc.WithTransportCredentials(transport), grpc.WithNoProxy(), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(32 << 20))}
	dialOptions = append(dialOptions, options...)
	conn, err := grpc.NewClient("dns:///"+u.Host, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("创建 CloudDrive2 连接: %w", err)
	}
	return &Client{conn: conn, rpc: pb.NewCloudDriveFileSrvClient(conn), token: config.Token}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) request(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	values, _ := metadata.FromOutgoingContext(ctx)
	values = values.Copy()
	values.Set("authorization", "Bearer "+c.token)
	return metadata.NewOutgoingContext(ctx, values), cancel
}

type remoteError struct {
	cause   error
	message string
}

func (e *remoteError) Error() string { return e.message }
func (e *remoteError) Unwrap() error { return e.cause }

func (c *Client) wrap(action string, err error) error {
	if err == nil {
		return nil
	}
	var cause error = err
	message := "CloudDrive2 请求失败"
	switch status.Code(err) {
	case codes.NotFound:
		cause, message = errors.Join(ErrNotFound, err), ErrNotFound.Error()
	case codes.AlreadyExists:
		cause, message = errors.Join(ErrExists, err), ErrExists.Error()
	case codes.Unauthenticated:
		message = "CloudDrive2 授权失效，请检查 API Token"
	case codes.PermissionDenied:
		message = "CloudDrive2 令牌权限或目录范围不足"
	case codes.Unimplemented:
		message = "当前 CloudDrive2 版本不支持此接口"
	case codes.Unavailable:
		message = "CloudDrive2 服务不可达，请检查地址、端口与 TLS 设置"
	case codes.DeadlineExceeded:
		cause, message = errors.Join(context.DeadlineExceeded, err), "CloudDrive2 请求超时"
	case codes.Canceled:
		cause, message = errors.Join(context.Canceled, err), "CloudDrive2 请求已取消"
	default:
		message += "（" + status.Code(err).String() + "）"
	}
	return &remoteError{cause: cause, message: action + "：" + message}
}

func (c *Client) result(action string, result *pb.FileOperationResult) error {
	if result != nil && result.GetSuccess() {
		return nil
	}
	message := "CloudDrive2 未确认操作成功"
	if result != nil && result.GetErrorMessage() != "" {
		message = strings.ReplaceAll(result.GetErrorMessage(), c.token, "[已隐藏]")
		if len([]rune(message)) > 400 {
			message = string([]rune(message)[:400]) + "…"
		}
	}
	return errors.New(action + "：" + message)
}

func fileValue(value *pb.CloudDriveFile, fallback string) (File, error) {
	if value == nil {
		return File{}, errors.New("CloudDrive2 返回空文件信息")
	}
	full := value.GetFullPathName()
	if full == "" {
		full = fallback
	}
	full, err := CleanPath(full)
	if err != nil {
		return File{}, err
	}
	f := File{ID: value.GetId(), Name: value.GetName(), Path: full, Size: value.GetSize(), Directory: value.GetIsDirectory() || value.GetFileType() == pb.CloudDriveFile_Directory, ReadOnly: value.GetReadOnly() || value.GetCloudAPI().GetReadOnly(), CloudName: value.GetCloudAPI().GetName(), CloudAccount: value.GetCloudAPI().GetUserName(), Hashes: value.GetFileHashes()}
	if f.Name == "" {
		f.Name = path.Base(full)
	}
	if value.GetWriteTime() != nil {
		f.Modified = value.GetWriteTime().AsTime()
	}
	return f, nil
}

func (c *Client) List(ctx context.Context, directory string, force bool) ([]File, error) {
	directory, err := CleanPath(directory)
	if err != nil {
		return nil, err
	}
	return c.cachedList(ctx, directory, force)
}

func (c *Client) list(ctx context.Context, directory string, force bool) ([]File, error) {
	ctx, cancel := c.request(ctx)
	defer cancel()
	stream, err := c.rpc.GetSubFiles(ctx, &pb.ListSubFileRequest{Path: directory, ForceRefresh: force, CheckExpires: boolValue(true)})
	if err != nil {
		return nil, c.wrap("读取目录", err)
	}
	files := make([]File, 0, 128)
	seen := make(map[string]bool)
	for {
		reply, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, c.wrap("读取目录", err)
		}
		for _, item := range reply.GetSubFiles() {
			if err := cleanName(item.GetName()); err != nil {
				return nil, fmt.Errorf("读取目录: %w", err)
			}
			f, err := fileValue(item, path.Join(directory, item.GetName()))
			if err != nil {
				return nil, fmt.Errorf("读取目录: %w", err)
			}
			if path.Dir(f.Path) != directory || f.Path == directory {
				return nil, errors.New("CloudDrive2 返回的子项路径与目录不一致")
			}
			if seen[f.Path] {
				continue
			}
			if len(files) >= MaxDirectoryEntries {
				return nil, errors.New("CloudDrive2 单个目录条目过多，请缩小整理范围")
			}
			seen[f.Path] = true
			files = append(files, f)
		}
	}
}

func (c *Client) Stat(ctx context.Context, full string, force bool) (File, error) {
	full, err := CleanPath(full)
	if err != nil {
		return File{}, err
	}
	// 官方路径查询没有强制刷新字段；写入前通过刷新父目录重新核对身份。
	if force && full != "/" {
		files, err := c.List(ctx, path.Dir(full), true)
		if err != nil {
			return File{}, err
		}
		for _, f := range files {
			if f.Path == full {
				return f, nil
			}
		}
		return File{}, ErrNotFound
	}
	ctx, cancel := c.request(ctx)
	defer cancel()
	value, err := c.rpc.FindFileByPath(ctx, &pb.FindFileByPathRequest{ParentPath: "/", Path: full})
	if err != nil {
		return File{}, c.wrap("读取文件信息", err)
	}
	f, err := fileValue(value, full)
	if err != nil {
		return File{}, err
	}
	if f.Path != full {
		return File{}, errors.New("CloudDrive2 返回的文件路径与请求不一致")
	}
	return f, nil
}

func (c *Client) CreateDirectory(ctx context.Context, parent, name string) (File, error) {
	ctx = withDirectoryCache(ctx)
	parent, err := CleanPath(parent)
	if err != nil {
		return File{}, err
	}
	if err := cleanName(name); err != nil {
		return File{}, err
	}
	release, err := c.lockWrite(ctx, parent)
	if err != nil {
		return File{}, err
	}
	defer release()
	full := path.Join(parent, name)
	existing, err := c.Stat(ctx, full, true)
	if err == nil {
		if existing.Directory && !existing.ReadOnly {
			return existing, nil
		}
		return File{}, ErrExists
	}
	if !errors.Is(err, ErrNotFound) {
		return File{}, err
	}
	ctx, cancel := c.request(ctx)
	defer cancel()
	result, err := c.rpc.CreateFolder(ctx, &pb.CreateFolderRequest{ParentPath: parent, FolderName: name})
	c.invalidateDirectories(parent)
	if err != nil {
		return File{}, c.wrap("创建目录", err)
	}
	if err := c.result("创建目录", result.GetResult()); err != nil {
		return File{}, err
	}
	f, err := fileValue(result.GetFolderCreated(), full)
	if err != nil {
		return File{}, err
	}
	if f.Path != full || !f.Directory {
		return File{}, errors.New("CloudDrive2 未确认目标目录已创建")
	}
	return f, nil
}

func (c *Client) Rename(ctx context.Context, source, name string) (string, error) {
	ctx = withDirectoryCache(ctx)
	source, err := CleanPath(source)
	if err != nil {
		return "", err
	}
	if err := cleanName(name); err != nil {
		return "", err
	}
	release, err := c.lockWrite(ctx, path.Dir(source))
	if err != nil {
		return "", err
	}
	defer release()
	target := path.Join(path.Dir(source), name)
	if target == source {
		return source, nil
	}
	if _, err := c.Stat(ctx, target, true); err == nil {
		return "", ErrExists
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	before, err := c.Stat(ctx, source, true)
	if err != nil {
		return "", err
	}
	ctx, cancel := c.request(ctx)
	defer cancel()
	result, err := c.rpc.RenameFile(ctx, &pb.RenameFileRequest{TheFilePath: source, NewName: name})
	c.invalidateDirectories(path.Dir(source))
	if err != nil {
		return "", c.wrap("重命名文件", err)
	}
	if err := c.result("重命名文件", result); err != nil {
		return "", err
	}
	after, err := c.Stat(ctx, target, true)
	if err != nil {
		return "", err
	}
	if !sameFile(before, after, false) {
		return "", errors.New("CloudDrive2 重命名结果身份未确认")
	}
	if _, err := c.Stat(ctx, source, true); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return "", fmt.Errorf("核对重命名来源: %w", err)
		}
		return "", errors.New("CloudDrive2 尚未确认原路径已移除")
	}
	return target, nil
}

func sameFile(before, after File, across bool) bool {
	if before.Size != after.Size || before.Directory != after.Directory {
		return false
	}
	matchedHash := false
	for kind, hash := range before.Hashes {
		if kind == 0 {
			continue
		}
		if other := after.Hashes[kind]; hash != "" && other != "" {
			if !strings.EqualFold(hash, other) {
				return false
			}
			matchedHash = true
		}
	}
	if across {
		return matchedHash && !before.Directory
	}
	if !before.Modified.IsZero() && !after.Modified.IsZero() && !before.Modified.Equal(after.Modified) {
		return false
	}
	return before.ID != "" && before.ID == after.ID || matchedHash || !before.Modified.IsZero() && before.Modified.Equal(after.Modified)
}

func hasHash(file File) bool {
	for kind, hash := range file.Hashes {
		if kind != 0 && hash != "" {
			return true
		}
	}
	return false
}

func (c *Client) Move(ctx context.Context, sources []string, destination string) ([]string, error) {
	ctx = withDirectoryCache(ctx)
	destination, err := CleanPath(destination)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 || len(sources) > 100 {
		return nil, fmt.Errorf("%w：每批移动 1 至 100 项", ErrInput)
	}
	directories := make([]string, 0, len(sources)+1)
	directories = append(directories, destination)
	for _, source := range sources {
		clean, err := CleanPath(source)
		if err != nil {
			return nil, err
		}
		directories = append(directories, path.Dir(clean))
	}
	release, err := c.lockWrite(ctx, directories...)
	if err != nil {
		return nil, err
	}
	defer release()
	dest, err := c.Stat(ctx, destination, true)
	if err != nil {
		return nil, err
	}
	if !dest.Directory || dest.ReadOnly {
		return nil, errors.New("CloudDrive2 目标目录不可写")
	}
	before := make([]File, 0, len(sources))
	paths := make([]string, 0, len(sources))
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		source, err = CleanPath(source)
		if err != nil {
			return nil, err
		}
		target := path.Join(destination, path.Base(source))
		if source == target || seen[target] {
			return nil, ErrExists
		}
		seen[target] = true
		if _, err := c.Stat(ctx, target, true); err == nil {
			return nil, ErrExists
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		f, err := c.Stat(ctx, source, true)
		if err != nil {
			return nil, err
		}
		if f.ReadOnly {
			return nil, errors.New("CloudDrive2 来源不可写")
		}
		across := f.CloudName != dest.CloudName || f.CloudAccount != dest.CloudAccount
		if across && (f.Directory || !hasHash(f)) {
			return nil, errors.New("CD2 跨存储移动需要可核验的文件哈希，请选择同一存储内的目录")
		}
		before, paths = append(before, f), append(paths, source)
	}
	ctx, cancel := c.request(ctx)
	defer cancel()
	policy := pb.MoveFileRequest_Skip
	result, err := c.rpc.MoveFile(ctx, &pb.MoveFileRequest{TheFilePaths: paths, DestPath: destination, ConflictPolicy: &policy, MoveAcrossClouds: boolValue(true)})
	c.invalidateDirectories(directories...)
	if err != nil {
		return nil, c.wrap("移动文件", err)
	}
	if err := c.result("移动文件", result); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for _, f := range before {
		target := path.Join(destination, f.Name)
		after, err := c.Stat(ctx, target, true)
		if err != nil {
			return nil, fmt.Errorf("移动结果尚未确认: %w", err)
		}
		across := f.CloudName != dest.CloudName || f.CloudAccount != dest.CloudAccount
		if !sameFile(f, after, across) {
			return nil, errors.New("CloudDrive2 移动结果身份未确认，请检查任务与目标文件")
		}
		if _, err := c.Stat(ctx, f.Path, true); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return nil, fmt.Errorf("核对移动来源: %w", err)
			}
			return nil, errors.New("CloudDrive2 尚未确认来源已移除，请稍后核对任务")
		}
		out = append(out, target)
	}
	return out, nil
}

func (c *Client) Delete(ctx context.Context, sources []string) error {
	ctx = withDirectoryCache(ctx)
	if len(sources) == 0 || len(sources) > 100 {
		return fmt.Errorf("%w：每批删除 1 至 100 项", ErrInput)
	}
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		full, err := CleanPath(source)
		if err != nil {
			return err
		}
		if full == "/" {
			return fmt.Errorf("%w：不能删除 CD2 根目录", ErrInput)
		}
		paths = append(paths, full)
	}
	directories := make([]string, 0, len(paths))
	for _, full := range paths {
		directories = append(directories, path.Dir(full))
	}
	release, err := c.lockWrite(ctx, directories...)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := c.request(ctx)
	defer cancel()
	result, err := c.rpc.DeleteFiles(ctx, &pb.MultiFileRequest{Path: paths})
	c.invalidateDirectories(directories...)
	if err != nil {
		return c.wrap("删除文件", err)
	}
	if err := c.result("删除文件", result); err != nil {
		return err
	}
	for _, full := range paths {
		if _, err := c.Stat(ctx, full, true); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return fmt.Errorf("核对删除结果: %w", err)
			}
			return errors.New("CloudDrive2 尚未确认文件已删除")
		}
	}
	return nil
}

func boolValue(value bool) *bool { return &value }

func (c *Client) Probe(ctx context.Context) Probe {
	started := time.Now()
	result := Probe{}
	ctx, cancel := c.request(ctx)
	defer cancel()
	info, err := c.rpc.GetSystemInfo(ctx, &emptypb.Empty{})
	if err != nil {
		result.Message = c.wrap("连接测试", err).Error()
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result
	}
	result.Reachable = true
	if !info.GetSystemReady() {
		result.Message = "CloudDrive2 服务尚未就绪"
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result
	}
	if _, err = c.List(ctx, "/", false); err != nil {
		result.Message = err.Error()
	} else {
		result.Authorized, result.Listable = true, true
		result.Message = "CloudDrive2 授权及目录读取成功"
	}
	if runtime, err := c.rpc.GetRuntimeInfo(ctx, &emptypb.Empty{}); err == nil {
		result.Version = runtime.GetProductVersion()
	}
	if tokenInfo, err := c.rpc.GetApiTokenInfo(ctx, &pb.StringValue{Value: c.token}); err == nil && tokenInfo.GetPermissions() != nil {
		p := tokenInfo.GetPermissions()
		result.Permissions = &Permissions{List: p.GetAllowList(), Read: p.GetAllowRead(), CreateFolder: p.GetAllowCreateFolder(), Rename: p.GetAllowRename(), Move: p.GetAllowMove(), Copy: p.GetAllowCopy(), Delete: p.GetAllowDelete(), Push: p.GetAllowPushMessage()}
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	return result
}
