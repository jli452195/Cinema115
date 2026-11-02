package clouddrive2

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"strm115/backend/internal/client/clouddrive2/pb"
)

type eventTestSubscription struct {
	hasDeadline bool
	auth        string
}

type eventTestTokenRequest struct {
	hasDeadline bool
	auth        string
	token       string
}

type eventTestServer struct {
	pb.UnimplementedCloudDriveFileSrvServer
	messages       []*pb.CloudDrivePushMessage
	started        chan eventTestSubscription
	cancelled      chan struct{}
	streamErr      error
	tokenInfo      *pb.TokenInfo
	tokenErr       error
	tokenCalls     atomic.Int32
	tokenRequests  chan eventTestTokenRequest
	sendHeaders    bool
	beforeMessages <-chan struct{}
}

func (s *eventTestServer) GetApiTokenInfo(ctx context.Context, request *pb.StringValue) (*pb.TokenInfo, error) {
	s.tokenCalls.Add(1)
	values, _ := metadata.FromIncomingContext(ctx)
	_, hasDeadline := ctx.Deadline()
	s.tokenRequests <- eventTestTokenRequest{hasDeadline: hasDeadline, auth: strings.Join(values.Get("authorization"), ","), token: request.GetValue()}
	if s.tokenErr != nil {
		return nil, s.tokenErr
	}
	if s.tokenInfo != nil {
		return s.tokenInfo, nil
	}
	return &pb.TokenInfo{RootDir: "/"}, nil
}

func (s *eventTestServer) PushMessage(_ *emptypb.Empty, stream grpc.ServerStreamingServer[pb.CloudDrivePushMessage]) error {
	values, _ := metadata.FromIncomingContext(stream.Context())
	_, hasDeadline := stream.Context().Deadline()
	s.started <- eventTestSubscription{hasDeadline: hasDeadline, auth: strings.Join(values.Get("authorization"), ",")}
	if s.sendHeaders {
		if err := stream.SendHeader(metadata.Pairs("event-stream", "ready")); err != nil {
			return err
		}
	}
	if s.beforeMessages != nil {
		select {
		case <-s.beforeMessages:
		case <-stream.Context().Done():
			close(s.cancelled)
			return stream.Context().Err()
		}
	}
	for _, message := range s.messages {
		if err := stream.Send(message); err != nil {
			return err
		}
	}
	if s.streamErr != nil {
		return s.streamErr
	}
	<-stream.Context().Done()
	close(s.cancelled)
	return stream.Context().Err()
}

func eventFixture(t *testing.T, server *eventTestServer) *Client {
	t.Helper()
	server.started, server.cancelled = make(chan eventTestSubscription, 1), make(chan struct{})
	server.tokenRequests = make(chan eventTestTokenRequest, 1)
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	pb.RegisterCloudDriveFileSrvServer(grpcServer, server)
	done := make(chan error, 1)
	go func() { done <- grpcServer.Serve(listener) }()
	client, err := New(Config{URL: "http://127.0.0.1:19798", Token: "event-test-token"}, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		grpcServer.Stop()
		_ = listener.Close()
		<-done
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); grpcServer.Stop(); _ = listener.Close(); <-done })
	return client
}

func eventMessage(change *pb.FileSystemChange) *pb.CloudDrivePushMessage {
	return &pb.CloudDrivePushMessage{MessageType: pb.CloudDrivePushMessage_FILE_SYSTEM_CHANGE, Data: &pb.CloudDrivePushMessage_FileSystemChange{FileSystemChange: change}}
}

func TestEventsPreserveNativeChangesAndExplicitUploadCompletion(t *testing.T) {
	newPath := "/其他网盘/电影/新名字.mkv"
	server := &eventTestServer{messages: []*pb.CloudDrivePushMessage{
		{MessageType: pb.CloudDrivePushMessage_LOG_MESSAGE},
		eventMessage(&pb.FileSystemChange{Path: "/其他网盘/电影/新文件.mkv", ChangeType: pb.FileSystemChange_CREATE, TheFile: &pb.CloudDriveFile{Id: "native-id", Name: "新文件.mkv", FullPathName: "/其他网盘/电影/新文件.mkv", FileType: pb.CloudDriveFile_File, FileHashes: map[uint32]string{2: "sha1-from-cd2"}}}),
		eventMessage(&pb.FileSystemChange{Path: "/其他网盘/电影/新文件.mkv", ChangeType: pb.FileSystemChange_MODIFY}),
		eventMessage(&pb.FileSystemChange{Path: "/其他网盘/电影/旧名字.mkv", NewPath: &newPath, ChangeType: pb.FileSystemChange_RENAME, TheFile: &pb.CloudDriveFile{Id: "renamed-native-id", FullPathName: newPath, FileType: pb.CloudDriveFile_File}}),
		eventMessage(&pb.FileSystemChange{Path: "/其他网盘/旧目录", ChangeType: pb.FileSystemChange_DELETE, IsDirectory: true}),
		eventMessage(&pb.FileSystemChange{Path: "/其他网盘/未来类型", ChangeType: pb.FileSystemChange_ChangeType(50)}),
		{MessageType: pb.CloudDrivePushMessage_UPLOADER_COUNT, Data: &pb.CloudDrivePushMessage_TransferTaskStatus{TransferTaskStatus: &pb.TransferTaskStatus{UploadFileStatusChanges: []*pb.UploadFileInfo{
			{DestPath: "/其他网盘/电影/未完成.mkv", StatusEnum: pb.UploadFileInfo_Transfer},
			{DestPath: "/其他网盘/电影/跳过.mkv", StatusEnum: pb.UploadFileInfo_Skipped},
			{DestPath: "/其他网盘/电影/完成.mkv", Size: 420, StatusEnum: pb.UploadFileInfo_Finish},
		}}}},
	}}
	client := eventFixture(t, server)
	wantedStop := errors.New("本轮消费结束")
	changes := make([]Change, 0, 5)
	err := client.Events(context.Background(), func(change Change) error {
		changes = append(changes, change)
		if len(changes) == 5 {
			return wantedStop
		}
		return nil
	})
	if !errors.Is(err, wantedStop) || len(changes) != 5 {
		t.Fatalf("事件接收结果不符: %+v %v", changes, err)
	}
	if changes[0].Type != "create" || changes[0].File == nil || changes[0].File.ID != "native-id" || changes[0].File.Hashes[2] != "sha1-from-cd2" || changes[0].Ready {
		t.Fatalf("创建事件错误地被视为已完成，或丢失原生文件信息: %+v", changes[0])
	}
	if changes[1].Type != "modify" || changes[1].Ready || changes[2].Type != "rename" || changes[2].NewPath != newPath || changes[2].File == nil || changes[2].File.Path != newPath {
		t.Fatalf("修改或重命名事件不符: %+v", changes)
	}
	if changes[3].Type != "delete" || !changes[3].Directory || changes[3].File != nil {
		t.Fatalf("删除目录事件不符: %+v", changes[3])
	}
	if changes[4].Type != "upload_finished" || !changes[4].Ready || changes[4].Path != "/其他网盘/电影/完成.mkv" || changes[4].File == nil || changes[4].File.Size != 420 {
		t.Fatalf("上传完成必须来源于明确完成状态: %+v", changes[4])
	}
	select {
	case <-server.cancelled:
	case <-time.After(time.Second):
		t.Fatal("消费结束后未取消服务器推送流")
	}
}

func TestEventsUseCallerLifetimeAndPropagateCancellation(t *testing.T) {
	server := &eventTestServer{}
	client := eventFixture(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Events(ctx, func(Change) error { return nil }) }()
	select {
	case subscribed := <-server.started:
		if subscribed.hasDeadline || subscribed.auth != "Bearer event-test-token" {
			t.Fatalf("推送流不应继承单次调用超时，且必须携带认证: %+v", subscribed)
		}
	case <-time.After(time.Second):
		t.Fatal("没有开始事件订阅")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("调用方取消没有传递: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后事件接收未结束")
	}
}

func TestEventsWrapStreamErrorsAndRejectMissingConsumer(t *testing.T) {
	server := &eventTestServer{streamErr: status.Error(codes.PermissionDenied, "denied event-test-token")}
	client := eventFixture(t, server)
	err := client.Events(context.Background(), func(Change) error { return nil })
	if err == nil || strings.Contains(err.Error(), "event-test-token") || !strings.Contains(err.Error(), "权限") || status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("推送错误应脱敏且保留原错误: %v", err)
	}
	if err := client.Events(context.Background(), nil); !errors.Is(err, ErrInput) {
		t.Fatalf("缺少消费函数应返回输入错误: %v", err)
	}
}

func TestEventsWithReadyConfirmsFirstFrameOnceAndLoadsRootOnce(t *testing.T) {
	gate := make(chan struct{})
	server := &eventTestServer{sendHeaders: true, beforeMessages: gate, messages: []*pb.CloudDrivePushMessage{
		{MessageType: pb.CloudDrivePushMessage_LOG_MESSAGE},
		{MessageType: pb.CloudDrivePushMessage_UPLOADER_COUNT},
		eventMessage(&pb.FileSystemChange{Path: "/电影/已完成.mkv", ChangeType: pb.FileSystemChange_CREATE}),
	}}
	client := eventFixture(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var connected atomic.Int32
	wantedStop := errors.New("首帧确认完成")
	done := make(chan error, 1)
	go func() {
		done <- client.EventsWithReady(ctx, func() { connected.Add(1) }, func(Change) error {
			if connected.Load() != 1 {
				return errors.New("文件消费前应恰好确认一次连接")
			}
			return wantedStop
		})
	}()
	select {
	case <-server.started:
	case <-time.After(time.Second):
		t.Fatal("没有开始事件订阅")
	}
	if connected.Load() != 0 {
		t.Fatal("仅创建流或收到响应头不能视为首帧确认")
	}
	close(gate)
	select {
	case err := <-done:
		if !errors.Is(err, wantedStop) || connected.Load() != 1 {
			t.Fatalf("首个非文件帧应确认一次连接: %d %v", connected.Load(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("首帧后未处理文件事件")
	}
	if server.tokenCalls.Load() != 1 {
		t.Fatalf("一个事件流只能查询一次令牌根目录: %d", server.tokenCalls.Load())
	}
	request := <-server.tokenRequests
	if !request.hasDeadline || request.auth != "Bearer event-test-token" || request.token != "event-test-token" {
		t.Fatalf("令牌根查询必须固定本客户端凭据并使用独立超时: %+v", request)
	}
}

func TestEventsWithReadyDoesNotConfirmFailedOrUnknownRootStreams(t *testing.T) {
	tests := []struct {
		name     string
		server   *eventTestServer
		wantCode codes.Code
	}{
		{name: "推送鉴权失败", server: &eventTestServer{streamErr: status.Error(codes.PermissionDenied, "denied event-test-token")}, wantCode: codes.PermissionDenied},
		{name: "根目录查询失败", server: &eventTestServer{tokenErr: status.Error(codes.PermissionDenied, "denied event-test-token")}, wantCode: codes.PermissionDenied},
		{name: "未返回根目录", server: &eventTestServer{tokenInfo: &pb.TokenInfo{}}},
		{name: "根目录格式无效", server: &eventTestServer{tokenInfo: &pb.TokenInfo{RootDir: "非绝对路径"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := eventFixture(t, test.server)
			connected, consumed := false, false
			err := client.EventsWithReady(context.Background(), func() { connected = true }, func(Change) error { consumed = true; return nil })
			if err == nil || connected || consumed || strings.Contains(err.Error(), "event-test-token") {
				t.Fatalf("未确认连接或根目录时不应消费事件，错误必须脱敏: connected=%v consumed=%v err=%v", connected, consumed, err)
			}
			if test.wantCode != codes.OK && status.Code(errors.Unwrap(err)) != test.wantCode {
				t.Fatalf("远端错误码丢失: %v", err)
			}
		})
	}
}

func TestEventsWithReadyConfirmsNonFileFirstFrame(t *testing.T) {
	server := &eventTestServer{messages: []*pb.CloudDrivePushMessage{
		{MessageType: pb.CloudDrivePushMessage_LOG_MESSAGE},
		{MessageType: pb.CloudDrivePushMessage_UPLOADER_COUNT},
	}}
	client := eventFixture(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connected := make(chan struct{}, 2)
	done := make(chan error, 1)
	consumed := false
	go func() {
		done <- client.EventsWithReady(ctx, func() { connected <- struct{}{} }, func(Change) error { consumed = true; return nil })
	}()
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("首个非文件响应也应确认真实连接")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || consumed || len(connected) != 0 {
			t.Fatalf("非文件消息不应被消费或重复确认，且必须传递取消: consumed=%v connected=%d err=%v", consumed, len(connected), err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后事件流未退出")
	}
}

func TestEventsMapOfficialScopedRootToVisiblePaths(t *testing.T) {
	const root = "/远端网盘/账号A"
	newPath := root + "/Cinema115/新名字.mkv"
	outside := "/其他网盘/Cinema115/同名字.mkv"
	server := &eventTestServer{tokenInfo: &pb.TokenInfo{RootDir: root + "/"}, messages: []*pb.CloudDrivePushMessage{
		eventMessage(&pb.FileSystemChange{Path: root + "/Cinema115/新文件.mkv", ChangeType: pb.FileSystemChange_CREATE, TheFile: &pb.CloudDriveFile{Id: "scoped-native-id", FullPathName: root + "/Cinema115/新文件.mkv", FileType: pb.CloudDriveFile_File}}),
		eventMessage(&pb.FileSystemChange{Path: root + "/Cinema115/旧名字.mkv", NewPath: &newPath, ChangeType: pb.FileSystemChange_RENAME, TheFile: &pb.CloudDriveFile{Id: "renamed-id", FileType: pb.CloudDriveFile_File}}),
		eventMessage(&pb.FileSystemChange{Path: root, ChangeType: pb.FileSystemChange_MODIFY, IsDirectory: true}),
		eventMessage(&pb.FileSystemChange{Path: outside, ChangeType: pb.FileSystemChange_CREATE}),
		eventMessage(&pb.FileSystemChange{Path: root + "-其他/Cinema115/同名字.mkv", ChangeType: pb.FileSystemChange_CREATE}),
		eventMessage(&pb.FileSystemChange{Path: "/Cinema115/同名字.mkv", ChangeType: pb.FileSystemChange_CREATE}),
		eventMessage(&pb.FileSystemChange{Path: root + "/Cinema115/外移.mkv", NewPath: &outside, ChangeType: pb.FileSystemChange_RENAME}),
		{MessageType: pb.CloudDrivePushMessage_UPLOADER_COUNT, Data: &pb.CloudDrivePushMessage_TransferTaskStatus{TransferTaskStatus: &pb.TransferTaskStatus{UploadFileStatusChanges: []*pb.UploadFileInfo{
			{DestPath: outside, StatusEnum: pb.UploadFileInfo_Finish},
			{DestPath: root + "-其他/Cinema115/同名字.mkv", StatusEnum: pb.UploadFileInfo_Finish},
			{DestPath: "/Cinema115/同名字.mkv", StatusEnum: pb.UploadFileInfo_Finish},
			{DestPath: root + "/Cinema115/完成.mkv", StatusEnum: pb.UploadFileInfo_Finish},
		}}}},
	}}
	client := eventFixture(t, server)
	changes := make([]Change, 0, 4)
	wantedStop := errors.New("路径映射完成")
	err := client.Events(context.Background(), func(change Change) error {
		changes = append(changes, change)
		if change.Ready {
			return wantedStop
		}
		return nil
	})
	if !errors.Is(err, wantedStop) || len(changes) != 4 {
		t.Fatalf("只能消费官方根目录内且成功映射的事件: %+v %v", changes, err)
	}
	if changes[0].Path != "/Cinema115/新文件.mkv" || changes[0].File == nil || changes[0].File.Path != changes[0].Path || changes[0].File.ID != "scoped-native-id" {
		t.Fatalf("创建事件路径和文件身份映射不符: %+v", changes[0])
	}
	if changes[1].Path != "/Cinema115/旧名字.mkv" || changes[1].NewPath != "/Cinema115/新名字.mkv" || changes[1].File == nil || changes[1].File.Path != changes[1].NewPath || changes[1].File.ID != "renamed-id" {
		t.Fatalf("重命名路径和文件回退路径映射不符: %+v", changes[1])
	}
	if changes[2].Path != "/" || !changes[2].Directory || changes[3].Path != "/Cinema115/完成.mkv" || !changes[3].Ready {
		t.Fatalf("根目录和上传完成路径映射不符: %+v", changes)
	}
}
