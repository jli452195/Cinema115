package clouddrive2

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"strm115/backend/internal/client/clouddrive2/pb"
)

type testServer struct {
	pb.UnimplementedCloudDriveFileSrvServer
	mu           sync.Mutex
	files        map[string]*pb.CloudDriveFile
	skipMove     bool
	policy       pb.MoveFileRequest_ConflictPolicy
	across       bool
	moves        int
	unauthorized bool
	lists        int
	listStarted  chan string
	listRelease  <-chan struct{}
}

func (s *testServer) GetSystemInfo(context.Context, *emptypb.Empty) (*pb.CloudDriveSystemInfo, error) {
	return &pb.CloudDriveSystemInfo{SystemReady: true}, nil
}

func (s *testServer) GetRuntimeInfo(context.Context, *emptypb.Empty) (*pb.RuntimeInfo, error) {
	return &pb.RuntimeInfo{ProductVersion: "1.1.2"}, nil
}

func (s *testServer) GetApiTokenInfo(context.Context, *pb.StringValue) (*pb.TokenInfo, error) {
	return &pb.TokenInfo{Token: "scoped-token", Permissions: &pb.TokenPermissions{AllowList: true, AllowMove: true, AllowRename: true, AllowPushMessage: true}}, nil
}

func (s *testServer) GetSubFiles(req *pb.ListSubFileRequest, stream grpc.ServerStreamingServer[pb.SubFilesReply]) error {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
	if s.listStarted != nil {
		select {
		case s.listStarted <- req.GetPath():
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	if s.listRelease != nil {
		select {
		case <-s.listRelease:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unauthorized {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	if f, ok := s.files[req.GetPath()]; !ok || !f.GetIsDirectory() {
		return status.Error(codes.NotFound, "missing directory")
	}
	keys := make([]string, 0, len(s.files))
	for key := range s.files {
		if key != req.GetPath() && path.Dir(key) == req.GetPath() {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := stream.Send(&pb.SubFilesReply{SubFiles: []*pb.CloudDriveFile{proto.Clone(s.files[key]).(*pb.CloudDriveFile)}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *testServer) FindFileByPath(_ context.Context, req *pb.FindFileByPathRequest) (*pb.CloudDriveFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[req.GetPath()]
	if f == nil {
		return nil, status.Error(codes.NotFound, "missing file")
	}
	return proto.Clone(f).(*pb.CloudDriveFile), nil
}

func (s *testServer) CreateFolder(_ context.Context, req *pb.CreateFolderRequest) (*pb.CreateFolderResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	full := path.Join(req.GetParentPath(), req.GetFolderName())
	f := testFile(full, full, true)
	s.files[full] = f
	return &pb.CreateFolderResult{FolderCreated: proto.Clone(f).(*pb.CloudDriveFile), Result: &pb.FileOperationResult{Success: true}}, nil
}

func (s *testServer) RenameFile(_ context.Context, req *pb.RenameFileRequest) (*pb.FileOperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[req.GetTheFilePath()]
	if f == nil {
		return nil, status.Error(codes.NotFound, "missing file")
	}
	delete(s.files, req.GetTheFilePath())
	f.FullPathName, f.Name = path.Join(path.Dir(req.GetTheFilePath()), req.GetNewName()), req.GetNewName()
	s.files[f.FullPathName] = f
	return &pb.FileOperationResult{Success: true, ResultFilePaths: []string{f.FullPathName}}, nil
}

func (s *testServer) MoveFile(_ context.Context, req *pb.MoveFileRequest) (*pb.FileOperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy, s.across, s.moves = req.GetConflictPolicy(), req.GetMoveAcrossClouds(), s.moves+1
	if s.skipMove {
		return &pb.FileOperationResult{Success: true}, nil
	}
	out := make([]string, 0, len(req.GetTheFilePaths()))
	for _, source := range req.GetTheFilePaths() {
		f := s.files[source]
		if f == nil {
			return nil, status.Error(codes.NotFound, "missing file")
		}
		delete(s.files, source)
		f.FullPathName = path.Join(req.GetDestPath(), f.GetName())
		dest := s.files[req.GetDestPath()]
		if f.CloudAPI.GetUserName() != dest.CloudAPI.GetUserName() {
			f.Id = "new-cloud-copy"
		}
		f.CloudAPI = proto.Clone(dest.CloudAPI).(*pb.CloudAPI)
		s.files[f.FullPathName] = f
		out = append(out, f.FullPathName)
	}
	return &pb.FileOperationResult{Success: true, ResultFilePaths: out}, nil
}

func (s *testServer) DeleteFiles(_ context.Context, req *pb.MultiFileRequest) (*pb.FileOperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range req.GetPath() {
		delete(s.files, source)
	}
	return &pb.FileOperationResult{Success: true}, nil
}

func testFile(full, id string, directory bool) *pb.CloudDriveFile {
	typeID := pb.CloudDriveFile_File
	if directory {
		typeID = pb.CloudDriveFile_Directory
	}
	account := "account-a"
	if strings.HasPrefix(full, "/cloud-b") {
		account = "account-b"
	}
	return &pb.CloudDriveFile{Id: id, Name: path.Base(full), FullPathName: full, Size: 42, IsDirectory: directory, FileType: typeID, CloudAPI: &pb.CloudAPI{Name: "GenericCloud", UserName: account}, FileHashes: map[uint32]string{2: "0123456789abcdef"}}
}

func fixture(t *testing.T) (*Client, *testServer) {
	t.Helper()
	s := &testServer{files: make(map[string]*pb.CloudDriveFile)}
	for _, full := range []string{"/", "/cloud-a", "/cloud-a/inbox", "/cloud-a/library", "/cloud-b", "/cloud-b/library"} {
		s.files[full] = testFile(full, full, true)
	}
	s.files["/cloud-a/inbox/movie.mkv"] = testFile("/cloud-a/inbox/movie.mkv", "native-file-1", false)
	listener := bufconn.Listen(1 << 20)
	check := func(ctx context.Context) error {
		values, _ := metadata.FromIncomingContext(ctx)
		if auth := values.Get("authorization"); len(auth) != 1 || auth[0] != "Bearer scoped-token" {
			return status.Error(codes.Unauthenticated, "missing authorization")
		}
		return nil
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := check(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}), grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := check(stream.Context()); err != nil {
			return err
		}
		return handler(srv, stream)
	}))
	pb.RegisterCloudDriveFileSrvServer(server, s)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	client, err := New(Config{URL: "http://127.0.0.1:19798", Token: "scoped-token"}, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); server.Stop(); _ = listener.Close(); <-done })
	return client, s
}

func TestNativeDirectoriesAndReadOnlyProbe(t *testing.T) {
	client, _ := fixture(t)
	files, err := client.List(context.Background(), "/", false)
	if err != nil || len(files) != 2 || files[0].Path != "/cloud-a" || files[1].CloudAccount != "account-b" {
		t.Fatalf("native listing: %+v %v", files, err)
	}
	probe := client.Probe(context.Background())
	raw, _ := json.Marshal(probe)
	if !probe.Reachable || !probe.Authorized || !probe.Listable || probe.Version != "1.1.2" || probe.Permissions == nil || !probe.Permissions.Move || strings.Contains(string(raw), "scoped-token") {
		t.Fatalf("probe: %+v", probe)
	}
}

func TestBatchMoveUsesSkipAndVerifiesNativeResults(t *testing.T) {
	client, s := fixture(t)
	for _, name := range []string{"movie.srt", "movie.nfo"} {
		full := path.Join("/cloud-a/inbox", name)
		s.files[full] = testFile(full, name, false)
	}
	paths := []string{"/cloud-a/inbox/movie.mkv", "/cloud-a/inbox/movie.srt", "/cloud-a/inbox/movie.nfo"}
	got, err := client.Move(context.Background(), paths, "/cloud-a/library")
	if err != nil || len(got) != 3 || s.policy != pb.MoveFileRequest_Skip || !s.across {
		t.Fatalf("move: %v %v", got, err)
	}
	if _, err := client.Stat(context.Background(), paths[0], true); !errors.Is(err, ErrNotFound) {
		t.Fatal("source should be moved", err)
	}
}

func TestCrossCloudMoveAcceptsChangedNativeIDWithMatchingHash(t *testing.T) {
	client, _ := fixture(t)
	got, err := client.Move(context.Background(), []string{"/cloud-a/inbox/movie.mkv"}, "/cloud-b/library")
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	f, err := client.Stat(context.Background(), got[0], true)
	if err != nil || f.ID != "new-cloud-copy" || f.CloudAccount != "account-b" {
		t.Fatal(f, err)
	}
}

func TestExistingTargetAndSkippedOperationAreNotReportedAsMoved(t *testing.T) {
	t.Run("existing target", func(t *testing.T) {
		client, s := fixture(t)
		full := "/cloud-a/library/movie.mkv"
		s.files[full] = testFile(full, "other", false)
		_, err := client.Move(context.Background(), []string{"/cloud-a/inbox/movie.mkv"}, "/cloud-a/library")
		if !errors.Is(err, ErrExists) || s.moves != 0 {
			t.Fatal("must preserve source and target", err)
		}
	})
	t.Run("skip response", func(t *testing.T) {
		client, s := fixture(t)
		s.skipMove = true
		_, err := client.Move(context.Background(), []string{"/cloud-a/inbox/movie.mkv"}, "/cloud-a/library")
		if err == nil {
			t.Fatal("aggregate success is not file completion")
		}
		if _, err := client.Stat(context.Background(), "/cloud-a/inbox/movie.mkv", true); err != nil {
			t.Fatal("source must remain", err)
		}
	})
}

func TestRenameCreateDeleteAndCancellation(t *testing.T) {
	client, _ := fixture(t)
	directory, err := client.CreateDirectory(context.Background(), "/cloud-a/library", "Film")
	if err != nil || directory.Path != "/cloud-a/library/Film" {
		t.Fatal(directory, err)
	}
	full, err := client.Rename(context.Background(), "/cloud-a/inbox/movie.mkv", "Film.mkv")
	if err != nil || full != "/cloud-a/inbox/Film.mkv" {
		t.Fatal(full, err)
	}
	if err := client.Delete(context.Background(), []string{full}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Stat(context.Background(), full, true); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(ctx, "/", false); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was not preserved", err)
	}
}

func TestProbeDistinguishesReachabilityFromAuthorization(t *testing.T) {
	client, s := fixture(t)
	s.unauthorized = true
	probe := client.Probe(context.Background())
	if !probe.Reachable || probe.Authorized || probe.Listable {
		t.Fatal(probe)
	}
}

func TestConfigAndNativePaths(t *testing.T) {
	config, err := Prepare(Config{URL: "https://[::1]", Token: "token"})
	if err != nil || config.URL != "https://[::1]:443" {
		t.Fatal(config, err)
	}
	for _, value := range []string{"ftp://localhost:21", "http://localhost:0", "http://localhost:70000", "http://user:password@localhost:80", "http://localhost:80/path"} {
		if _, err := Prepare(Config{URL: value}); !errors.Is(err, ErrInput) {
			t.Fatal("invalid service address accepted")
		}
	}
	for _, value := range []string{"relative", "/a/../b", "/a/./b", "/a\\b"} {
		if _, err := CleanPath(value); !errors.Is(err, ErrInput) {
			t.Fatal("invalid native path accepted")
		}
	}
}
