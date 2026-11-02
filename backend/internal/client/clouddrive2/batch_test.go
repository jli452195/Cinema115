package clouddrive2

import (
	"context"
	"errors"
	"fmt"
	"path"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"strm115/backend/internal/client/clouddrive2/pb"
)

type afterBatchRenameClient struct {
	pb.CloudDriveFileSrvClient
	after func()
}

func (c *afterBatchRenameClient) RenameFiles(ctx context.Context, request *pb.RenameFilesRequest, options ...grpc.CallOption) (*pb.FileOperationResult, error) {
	reply, err := c.CloudDriveFileSrvClient.RenameFiles(ctx, request, options...)
	c.after()
	return reply, err
}

func addBatchFiles(server *testServer, count int) ([]RenameOperation, []string) {
	operations := make([]RenameOperation, 0, count)
	paths := make([]string, 0, count)
	for i := 0; i < count; i++ {
		source := fmt.Sprintf("/cloud-a/inbox/episode-%02d.mkv", i)
		name := fmt.Sprintf("Series.S01E%02d.mkv", i)
		server.files[source] = testFile(source, fmt.Sprintf("episode-%02d", i), false)
		operations = append(operations, RenameOperation{Source: source, Name: name})
		paths = append(paths, path.Join(path.Dir(source), name))
	}
	return operations, paths
}

func TestRenameManyAndMoveManyUseOneWriteAndSharedDirectoryReads(t *testing.T) {
	client, server := fixture(t)
	operations, paths := addBatchFiles(server, 20)
	ctx := WithDirectoryCache(context.Background())
	results, err := client.RenameMany(ctx, operations)
	if err != nil || len(results) != 20 || server.renameBatches != 1 || server.lists != 2 {
		t.Fatalf("rename results=%+v error=%v batches=%d lists=%d", results, err, server.renameBatches, server.lists)
	}
	for i, result := range results {
		if result.Err != nil || result.Source != operations[i].Source || result.Target != paths[i] || result.File.ID != fmt.Sprintf("episode-%02d", i) {
			t.Fatalf("rename item %d: %+v", i, result)
		}
	}
	listsBeforeMove := server.lists
	results, err = client.MoveMany(ctx, paths, "/cloud-a/library")
	if err != nil || len(results) != 20 || server.moves != 1 || server.lists-listsBeforeMove > 5 {
		t.Fatalf("move results=%+v error=%v moves=%d lists=%d", results, err, server.moves, server.lists-listsBeforeMove)
	}
	for i, result := range results {
		if result.Err != nil || result.Source != paths[i] || result.Target != path.Join("/cloud-a/library", operations[i].Name) || result.File.Path != result.Target {
			t.Fatalf("move item %d: %+v", i, result)
		}
	}
}

func TestBatchOperationsReconcilePartialAggregateFailure(t *testing.T) {
	for _, operation := range []string{"rename", "move"} {
		t.Run(operation, func(t *testing.T) {
			client, server := fixture(t)
			operations, _ := addBatchFiles(server, 3)
			var results []BatchFileResult
			var err error
			if operation == "rename" {
				server.renameLimit = 1
				results, err = client.RenameMany(context.Background(), operations)
			} else {
				server.moveLimit = 1
				sources := make([]string, 0, len(operations))
				for _, operation := range operations {
					sources = append(sources, operation.Source)
				}
				results, err = client.MoveMany(context.Background(), sources, "/cloud-a/library")
			}
			if err == nil || len(results) != 3 || results[0].Err != nil || results[0].File.ID != "episode-00" || results[1].Err == nil || results[2].Err == nil {
				t.Fatalf("partial results=%+v error=%v", results, err)
			}
			if _, exists := server.files[operations[0].Source]; exists {
				t.Fatal("confirmed operation should have removed its original source")
			}
			if _, exists := server.files[operations[1].Source]; !exists {
				t.Fatal("unprocessed source must remain untouched")
			}
			if server.renameBatches+server.moves != 1 {
				t.Fatal("partial failure must not replay the write batch")
			}
		})
	}
}

func TestBatchOperationsReconcileRPCFailureAfterAppliedWrite(t *testing.T) {
	for _, operation := range []string{"rename", "move"} {
		t.Run(operation, func(t *testing.T) {
			client, server := fixture(t)
			operations, _ := addBatchFiles(server, 2)
			var results []BatchFileResult
			var err error
			if operation == "rename" {
				server.renameRPCError = status.Error(codes.DeadlineExceeded, "reply lost")
				results, err = client.RenameMany(context.Background(), operations)
			} else {
				server.moveRPCError = status.Error(codes.Unavailable, "reply lost")
				results, err = client.MoveMany(context.Background(), []string{operations[0].Source, operations[1].Source}, "/cloud-a/library")
			}
			if err != nil || len(results) != 2 || results[0].Err != nil || results[1].Err != nil {
				t.Fatalf("applied write should be confirmed from actual identities: %+v %v", results, err)
			}
			if server.renameBatches+server.moves != 1 {
				t.Fatal("lost reply must not cause a second write")
			}
		})
	}
}

func TestBatchRenameRejectsDependenciesConflictsAndCancellationBeforeWriting(t *testing.T) {
	tests := []struct {
		name       string
		operations []RenameOperation
		cancel     bool
		want       error
	}{
		{name: "duplicate source", operations: []RenameOperation{{Source: "/cloud-a/inbox/movie.mkv", Name: "a.mkv"}, {Source: "/cloud-a/inbox/movie.mkv", Name: "b.mkv"}}, want: ErrInput},
		{name: "dependent target", operations: []RenameOperation{{Source: "/cloud-a/inbox/movie.mkv", Name: "other.mkv"}, {Source: "/cloud-a/inbox/other.mkv", Name: "next.mkv"}}, want: ErrInput},
		{name: "existing target", operations: []RenameOperation{{Source: "/cloud-a/inbox/other.mkv", Name: "movie.mkv"}}, want: ErrExists},
		{name: "canceled", operations: []RenameOperation{{Source: "/cloud-a/inbox/movie.mkv", Name: "new.mkv"}}, cancel: true, want: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, server := fixture(t)
			server.files["/cloud-a/inbox/other.mkv"] = testFile("/cloud-a/inbox/other.mkv", "other", false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				cancel()
			}
			_, err := client.RenameMany(ctx, test.operations)
			if !errors.Is(err, test.want) || server.renameBatches != 0 {
				t.Fatalf("must reject before writing: %v writes=%d", err, server.renameBatches)
			}
		})
	}
}

func TestMoveManyRejectsDuplicateTargetsAndMissingCrossCloudHash(t *testing.T) {
	t.Run("duplicate targets", func(t *testing.T) {
		client, server := fixture(t)
		server.files["/cloud-a/other"] = testFile("/cloud-a/other", "other-dir", true)
		server.files["/cloud-a/other/movie.mkv"] = testFile("/cloud-a/other/movie.mkv", "other-file", false)
		_, err := client.MoveMany(context.Background(), []string{"/cloud-a/inbox/movie.mkv", "/cloud-a/other/movie.mkv"}, "/cloud-a/library")
		if !errors.Is(err, ErrInput) || server.moves != 0 {
			t.Fatalf("duplicate targets must not write: %v", err)
		}
	})
	t.Run("cross cloud hash", func(t *testing.T) {
		client, server := fixture(t)
		server.files["/cloud-a/inbox/movie.mkv"].FileHashes = nil
		_, err := client.MoveMany(context.Background(), []string{"/cloud-a/inbox/movie.mkv"}, "/cloud-b/library")
		if err == nil || server.moves != 0 {
			t.Fatalf("unverifiable cross cloud move must not write: %v", err)
		}
	})
}

func TestBatchRenameDoesNotInventSuccessWhenCallerCanceledAfterWrite(t *testing.T) {
	client, server := fixture(t)
	operations, _ := addBatchFiles(server, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.rpc = &afterBatchRenameClient{CloudDriveFileSrvClient: client.rpc, after: cancel}
	results, err := client.RenameMany(ctx, operations)
	if !errors.Is(err, context.Canceled) || len(results) != 2 || !errors.Is(results[0].Err, context.Canceled) || !errors.Is(results[1].Err, context.Canceled) {
		t.Fatalf("canceled verification must remain unconfirmed: %+v %v", results, err)
	}
	if server.renameBatches != 1 || server.lists != 1 {
		t.Fatalf("cancellation must stop follow-up requests: writes=%d lists=%d", server.renameBatches, server.lists)
	}
}

func TestBatchRenameRejectsUnrelatedTargetIdentityDespiteAggregateSuccess(t *testing.T) {
	client, server := fixture(t)
	operations, paths := addBatchFiles(server, 2)
	client.rpc = &afterBatchRenameClient{CloudDriveFileSrvClient: client.rpc, after: func() {
		server.mu.Lock()
		defer server.mu.Unlock()
		server.files[paths[0]].FileHashes[2] = "different-content"
	}}
	results, err := client.RenameMany(context.Background(), operations)
	if err == nil || len(results) != 2 || results[0].Err == nil || results[0].File.Path != "" || results[1].Err != nil {
		t.Fatalf("wrong identity must not be accepted: %+v %v", results, err)
	}
}
