package clouddrive2

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strm115/backend/internal/client/clouddrive2/pb"
)

type emptyDirectoryRPC struct {
	pb.CloudDriveFileSrvClient
	lists       []string
	deletes     int
	failListing string
	skipDelete  bool
	deleteErr   error
}

func (r *emptyDirectoryRPC) GetSubFiles(ctx context.Context, in *pb.ListSubFileRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[pb.SubFilesReply], error) {
	if !in.GetForceRefresh() {
		return nil, errors.New("清理必须强制刷新目录")
	}
	r.lists = append(r.lists, in.GetPath())
	stream, err := r.CloudDriveFileSrvClient.GetSubFiles(ctx, in, opts...)
	if err != nil || in.GetPath() != r.failListing {
		return stream, err
	}
	return &failedEmptyDirectoryStream{ServerStreamingClient: stream}, nil
}

type failedEmptyDirectoryStream struct {
	grpc.ServerStreamingClient[pb.SubFilesReply]
}

func (s *failedEmptyDirectoryStream) Recv() (*pb.SubFilesReply, error) {
	return nil, status.Error(codes.Unavailable, "目录流尚未完整返回")
}

func (r *emptyDirectoryRPC) DeleteFile(ctx context.Context, in *pb.FileRequest, opts ...grpc.CallOption) (*pb.FileOperationResult, error) {
	r.deletes++
	if !in.GetForceRefresh() {
		return nil, errors.New("删除空目录必须强制刷新")
	}
	if r.deleteErr != nil {
		return nil, r.deleteErr
	}
	if r.skipDelete {
		return &pb.FileOperationResult{Success: true}, nil
	}
	return r.CloudDriveFileSrvClient.DeleteFiles(ctx, &pb.MultiFileRequest{Path: []string{in.GetPath()}}, opts...)
}

func TestRemoveEmptyDirectoryUsesFreshListingAndChecksResult(t *testing.T) {
	client, server := fixture(t)
	const full = "/cloud-a/inbox/empty"
	server.files[full] = testFile(full, "empty-native", true)
	rpc := &emptyDirectoryRPC{CloudDriveFileSrvClient: client.rpc}
	client.rpc = rpc
	removed, err := client.RemoveEmptyDirectory(WithDirectoryCache(context.Background()), full, "empty-native")
	if err != nil || !removed || rpc.deletes != 1 {
		t.Fatal("应通过单目录接口清理并核验", removed, rpc.deletes, err)
	}
	if !reflect.DeepEqual(rpc.lists, []string{"/cloud-a/inbox", full, "/cloud-a/inbox"}) {
		t.Fatal("空目录清理只刷新必要的身份、内容和结果", rpc.lists)
	}
	if _, exists := server.files[full]; exists {
		t.Fatal("目录没有被删除")
	}
}

func TestRemoveEmptyDirectoryRetainsNewContentAndChangedIdentity(t *testing.T) {
	for _, scenario := range []string{"新内容", "身份变化", "变为文件", "只读", "流读取失败"} {
		t.Run(scenario, func(t *testing.T) {
			client, server := fixture(t)
			const full = "/cloud-a/inbox/empty"
			server.files[full] = testFile(full, "empty-native", true)
			ctx := WithDirectoryCache(context.Background())
			if _, err := client.List(ctx, full, true); err != nil {
				t.Fatal(err)
			}
			if _, err := client.List(ctx, "/cloud-a/inbox", true); err != nil {
				t.Fatal(err)
			}
			rpc := &emptyDirectoryRPC{CloudDriveFileSrvClient: client.rpc}
			client.rpc = rpc
			switch scenario {
			case "新内容":
				server.files[full+"/.new.mkv.part"] = testFile(full+"/.new.mkv.part", "new-video", false)
			case "身份变化":
				server.files[full].Id = "replacement-native"
			case "变为文件":
				server.files[full] = testFile(full, "empty-native", false)
			case "只读":
				server.files[full].ReadOnly = true
			case "流读取失败":
				rpc.failListing = full
			}
			removed, err := client.RemoveEmptyDirectory(ctx, full, "empty-native")
			if removed || rpc.deletes != 0 || (err != nil) != (scenario == "流读取失败") {
				t.Fatal("目录复核未通过时不得删除", removed, rpc.deletes, err)
			}
			if _, exists := server.files[full]; !exists {
				t.Fatal("复核失败的目录被删除")
			}
		})
	}
}

func TestRemoveEmptyDirectoryFailureDoesNotReportSuccessOrRetry(t *testing.T) {
	for _, scenario := range []string{"未生效", "请求失败"} {
		t.Run(scenario, func(t *testing.T) {
			client, server := fixture(t)
			const full = "/cloud-a/inbox/empty"
			server.files[full] = testFile(full, "empty-native", true)
			rpc := &emptyDirectoryRPC{CloudDriveFileSrvClient: client.rpc, skipDelete: scenario == "未生效"}
			if scenario == "请求失败" {
				rpc.deleteErr = status.Error(codes.DeadlineExceeded, "结果未知")
			}
			client.rpc = rpc
			removed, err := client.RemoveEmptyDirectory(context.Background(), full, "empty-native")
			if removed || err == nil || rpc.deletes != 1 {
				t.Fatal("结果未确认不能记为成功或盲目重放", removed, rpc.deletes, err)
			}
		})
	}
}

func TestRemoveEmptyDirectoryRootAndWriteLockBoundaries(t *testing.T) {
	client, _ := fixture(t)
	if removed, err := client.RemoveEmptyDirectory(context.Background(), "/", ""); removed || !errors.Is(err, ErrInput) {
		t.Fatal("禁止清理 CD2 根目录", removed, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	release, err := client.lockWrite(context.Background(), "/cloud-a/inbox")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if removed, err := client.RemoveEmptyDirectory(ctx, "/cloud-a/inbox/empty", ""); removed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("目录写锁等待必须响应取消", removed, err)
	}
}
