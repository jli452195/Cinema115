package clouddrive2

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strm115/backend/internal/client/clouddrive2/pb"
)

type batchEmptyDirectoryRPC struct {
	*emptyDirectoryRPC
	batches       [][]string
	singles       []string
	applyLimit    int
	batchErr      error
	applyOnError  bool
	partialResult bool
	afterDelete   func()
	beforeList    func(string)
}

func (r *batchEmptyDirectoryRPC) GetSubFiles(ctx context.Context, in *pb.ListSubFileRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[pb.SubFilesReply], error) {
	if r.beforeList != nil {
		r.beforeList(in.GetPath())
	}
	return r.emptyDirectoryRPC.GetSubFiles(ctx, in, opts...)
}

func (r *batchEmptyDirectoryRPC) DeleteFile(ctx context.Context, in *pb.FileRequest, opts ...grpc.CallOption) (*pb.FileOperationResult, error) {
	if !in.GetForceRefresh() {
		return nil, errors.New("删除空目录必须强制刷新")
	}
	r.singles = append(r.singles, in.GetPath())
	return r.apply(ctx, []string{in.GetPath()}, opts...)
}

func (r *batchEmptyDirectoryRPC) DeleteFiles(ctx context.Context, in *pb.MultiFileRequest, opts ...grpc.CallOption) (*pb.FileOperationResult, error) {
	r.batches = append(r.batches, slices.Clone(in.GetPath()))
	return r.apply(ctx, in.GetPath(), opts...)
}

func (r *batchEmptyDirectoryRPC) apply(ctx context.Context, paths []string, opts ...grpc.CallOption) (*pb.FileOperationResult, error) {
	if r.batchErr == nil || r.applyOnError {
		applied := paths
		if r.skipDelete {
			applied = nil
		} else if r.applyLimit > 0 {
			applied = paths[:min(len(paths), r.applyLimit)]
		}
		if _, err := r.CloudDriveFileSrvClient.DeleteFiles(ctx, &pb.MultiFileRequest{Path: applied}, opts...); err != nil {
			return nil, err
		}
	}
	if r.afterDelete != nil {
		r.afterDelete()
	}
	if r.batchErr != nil {
		return nil, r.batchErr
	}
	return &pb.FileOperationResult{Success: !r.partialResult, ErrorMessage: "部分目录未删除"}, nil
}

func batchEmptyDirectoryFixture(t *testing.T, count int) (*Client, *testServer, *batchEmptyDirectoryRPC, []EmptyDirectoryRequest) {
	t.Helper()
	client, server := fixture(t)
	requests := make([]EmptyDirectoryRequest, 0, count)
	for i := range count {
		full, id := fmt.Sprintf("/cloud-a/inbox/empty-%02d", i), fmt.Sprintf("native-%02d", i)
		server.files[full] = testFile(full, id, true)
		requests = append(requests, EmptyDirectoryRequest{Path: full, ID: id})
	}
	rpc := &batchEmptyDirectoryRPC{emptyDirectoryRPC: &emptyDirectoryRPC{CloudDriveFileSrvClient: client.rpc}}
	client.rpc = rpc
	return client, server, rpc, requests
}

func TestRemoveEmptyDirectoriesSharesParentRefreshAndUsesTwentyItemBatches(t *testing.T) {
	for _, count := range []int{1, 20, 45} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			client, server, rpc, requests := batchEmptyDirectoryFixture(t, count)
			results := client.RemoveEmptyDirectories(WithDirectoryCache(context.Background()), requests)
			if len(results) != count {
				t.Fatal("结果数量发生变化", results)
			}
			for i, result := range results {
				if result.Path != requests[i].Path || !result.Removed || result.Err != nil {
					t.Fatal("批量删除未确认", result)
				}
				if _, exists := server.files[result.Path]; exists {
					t.Fatal("空目录仍存在", result.Path)
				}
			}
			groups := (count + 19) / 20
			if len(rpc.lists)+len(rpc.batches)+len(rpc.singles) != count+groups*3 {
				t.Fatal("未按同父目录共享读取和结果复核", rpc.lists, rpc.batches, rpc.singles)
			}
			if count == 20 && (len(rpc.lists) != 22 || len(rpc.batches) != 1 || len(rpc.batches[0]) != 20 || len(rpc.singles) != 0) {
				t.Fatal("20 个同父目录应只使用 23 次 RPC", rpc.lists, rpc.batches, rpc.singles)
			}
			for _, batch := range rpc.batches {
				if len(batch) > 20 {
					t.Fatal("单次批量超过 20 个目录", len(batch))
				}
				for _, full := range batch {
					if path.Dir(full) != "/cloud-a/inbox" {
						t.Fatal("不同父目录混入一次删除", batch)
					}
				}
			}
		})
	}
}

func TestRemoveEmptyDirectoriesRetainsIneligibleCandidatesWithoutBlockingOthers(t *testing.T) {
	client, server, rpc, requests := batchEmptyDirectoryFixture(t, 8)
	server.files[requests[0].Path+"/new.part"] = testFile(requests[0].Path+"/new.part", "new-content", false)
	server.files[requests[1].Path].Id = "changed-id"
	server.files[requests[2].Path].ReadOnly = true
	server.files[requests[3].Path].CloudAPI.ReadOnly = true
	server.files[requests[4].Path] = testFile(requests[4].Path, requests[4].ID, false)
	rpc.failListing = requests[5].Path
	delete(server.files, requests[6].Path)
	results := client.RemoveEmptyDirectories(context.Background(), requests)
	for i, result := range results {
		if result.Path != requests[i].Path || result.Removed != (i >= 6) || (result.Err != nil) != (i == 5) {
			t.Fatal("独立候选的状态受其他失败影响", i, result)
		}
	}
	if !reflect.DeepEqual(rpc.singles, []string{requests[7].Path}) || len(rpc.batches) != 0 {
		t.Fatal("不符合空目录条件的项目进入删除请求", rpc.batches, rpc.singles)
	}
	if _, exists := server.files[requests[0].Path+"/new.part"]; !exists {
		t.Fatal("非空目录中的未知内容被删除")
	}
}

func TestRemoveEmptyDirectoriesBypassesCachedContents(t *testing.T) {
	client, server, rpc, requests := batchEmptyDirectoryFixture(t, 2)
	ctx := WithDirectoryCache(context.Background())
	for _, full := range []string{"/cloud-a/inbox", requests[0].Path, requests[1].Path} {
		if _, err := client.List(ctx, full, true); err != nil {
			t.Fatal(err)
		}
	}
	rpc.lists = nil
	newFile := requests[0].Path + "/late.ass"
	server.files[newFile] = testFile(newFile, "late-subtitle", false)
	results := client.RemoveEmptyDirectories(ctx, requests)
	if results[0].Removed || !results[1].Removed || results[0].Err != nil || results[1].Err != nil {
		t.Fatal("实时内容变化未被识别", results)
	}
	if len(rpc.lists) != 4 || !reflect.DeepEqual(rpc.singles, []string{requests[1].Path}) {
		t.Fatal("清理必须绕过缓存并完整核验", rpc.lists, rpc.singles)
	}
	if _, exists := server.files[newFile]; !exists {
		t.Fatal("迟到字幕被清理")
	}
}

func TestRemoveEmptyDirectoriesConfirmsPartialResultsWithoutRetry(t *testing.T) {
	for _, scenario := range []string{"部分回执", "错误且部分生效", "错误且全部生效", "错误未生效", "假成功", "结果读取失败"} {
		t.Run(scenario, func(t *testing.T) {
			client, _, rpc, requests := batchEmptyDirectoryFixture(t, 3)
			switch scenario {
			case "部分回执":
				rpc.partialResult, rpc.applyLimit = true, 1
			case "错误且部分生效":
				rpc.batchErr, rpc.applyOnError, rpc.applyLimit = status.Error(codes.Unavailable, "批量结果不明"), true, 1
			case "错误且全部生效":
				rpc.batchErr, rpc.applyOnError = status.Error(codes.Unavailable, "批量结果不明"), true
			case "错误未生效":
				rpc.batchErr = status.Error(codes.Unavailable, "批量结果不明")
			case "假成功":
				rpc.skipDelete = true
			case "结果读取失败":
				rpc.afterDelete = func() { rpc.failListing = "/cloud-a/inbox" }
			}
			results := client.RemoveEmptyDirectories(context.Background(), requests)
			for i, result := range results {
				wantRemoved := scenario == "错误且全部生效" || (i == 0 && (scenario == "部分回执" || scenario == "错误且部分生效"))
				if result.Removed != wantRemoved || result.Err == nil {
					t.Fatal("必须按逐项实时核验结果报告并保留错误", i, result)
				}
			}
			if len(rpc.batches) != 1 || len(rpc.singles) != 0 || len(rpc.lists) != 5 {
				t.Fatal("结果未知不应盲目重放删除", rpc.batches, rpc.singles, rpc.lists)
			}
		})
	}
}

func TestRemoveEmptyDirectoriesContinuesAcrossIndependentParents(t *testing.T) {
	client, server, rpc, requests := batchEmptyDirectoryFixture(t, 2)
	rpc.failListing = "/cloud-a/inbox"
	other := "/cloud-b/library/empty"
	server.files[other] = testFile(other, "other-id", true)
	requests = append(requests, EmptyDirectoryRequest{Path: other, ID: "other-id"})
	results := client.RemoveEmptyDirectories(context.Background(), requests)
	if results[0].Removed || results[0].Err == nil || results[1].Removed || results[1].Err == nil || !results[2].Removed || results[2].Err != nil {
		t.Fatal("一个父目录读取失败不应阻止独立目录清理", results)
	}
	if !reflect.DeepEqual(rpc.singles, []string{other}) {
		t.Fatal("失败组不应进入删除", rpc.singles)
	}
}

func TestRemoveEmptyDirectoriesRejectsRootInvalidAndDuplicateRequests(t *testing.T) {
	client, _, rpc, requests := batchEmptyDirectoryFixture(t, 1)
	requests = append(requests, EmptyDirectoryRequest{Path: "/"}, EmptyDirectoryRequest{Path: "relative"}, requests[0])
	results := client.RemoveEmptyDirectories(context.Background(), requests)
	if !results[0].Removed || results[0].Err != nil {
		t.Fatal("有效候选未执行", results)
	}
	for _, result := range results[1:] {
		if result.Removed || !errors.Is(result.Err, ErrInput) {
			t.Fatal("无效目录进入清理", result)
		}
	}
	if len(rpc.singles) != 1 || len(rpc.batches) != 0 {
		t.Fatal("无效或重复请求产生额外删除", rpc.singles, rpc.batches)
	}
}

func TestRemoveEmptyDirectoriesWriteLockAndCancellation(t *testing.T) {
	client, _, rpc, requests := batchEmptyDirectoryFixture(t, 2)
	release, err := client.lockWrite(context.Background(), "/cloud-a/inbox")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	results := client.RemoveEmptyDirectories(ctx, requests)
	release()
	for _, result := range results {
		if result.Removed || !errors.Is(result.Err, context.DeadlineExceeded) {
			t.Fatal("写锁等待必须响应取消", result)
		}
	}
	if len(rpc.lists) != 0 || len(rpc.batches) != 0 || len(rpc.singles) != 0 {
		t.Fatal("取消后不应发起 RPC", rpc.lists, rpc.batches, rpc.singles)
	}
	results = client.RemoveEmptyDirectories(context.Background(), requests)
	for _, result := range results {
		if !result.Removed || result.Err != nil {
			t.Fatal("取消后锁未释放", result)
		}
	}
}

func TestRemoveEmptyDirectoriesRechecksNotFoundAndPathReplacement(t *testing.T) {
	for _, scenario := range []string{"已消失", "同路径变为文件"} {
		t.Run(scenario, func(t *testing.T) {
			client, server, rpc, requests := batchEmptyDirectoryFixture(t, 1)
			full := requests[0].Path
			rpc.beforeList = func(listed string) {
				if listed != full {
					return
				}
				server.mu.Lock()
				defer server.mu.Unlock()
				if scenario == "已消失" {
					delete(server.files, full)
				} else {
					server.files[full] = testFile(full, "replacement-file", false)
				}
			}
			result := client.RemoveEmptyDirectories(context.Background(), requests)[0]
			if result.Removed != (scenario == "已消失") || (result.Err == nil) != (scenario == "已消失") {
				t.Fatal("子目录读取未找到不能直接推断目录已消失", result)
			}
			if len(rpc.batches) != 0 || len(rpc.singles) != 0 || !reflect.DeepEqual(rpc.lists, []string{"/cloud-a/inbox", full, "/cloud-a/inbox"}) {
				t.Fatal("路径变化时只应复核，不应发起删除", rpc.lists, rpc.batches, rpc.singles)
			}
		})
	}
}

func TestRemoveEmptyDirectoriesCancellationAfterDeleteKeepsResultUnknown(t *testing.T) {
	client, _, rpc, requests := batchEmptyDirectoryFixture(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rpc.afterDelete = cancel
	results := client.RemoveEmptyDirectories(ctx, requests)
	for _, result := range results {
		if result.Removed || !errors.Is(result.Err, context.Canceled) {
			t.Fatal("取消导致结果未核验时不能记成功", result)
		}
	}
	if len(rpc.batches) != 1 || len(rpc.singles) != 0 {
		t.Fatal("已取消请求不得重放删除", rpc.batches, rpc.singles)
	}
	rpc.afterDelete = nil
	for _, result := range client.RemoveEmptyDirectories(context.Background(), requests) {
		if !result.Removed || result.Err != nil {
			t.Fatal("取消后应释放写锁，并在新请求中重新确认状态", result)
		}
	}
}
