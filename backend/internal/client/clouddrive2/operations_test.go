package clouddrive2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestOperationCacheReducesMoveRequestsAndInvalidatesSnapshots(t *testing.T) {
	client, server := fixture(t)
	ctx := WithDirectoryCache(context.Background())
	if _, err := client.List(ctx, "/cloud-a/inbox", true); err != nil {
		t.Fatal(err)
	}
	if _, err := client.List(ctx, "/cloud-a/library", true); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Move(ctx, []string{"/cloud-a/inbox/movie.mkv"}, "/cloud-a/library"); err != nil {
		t.Fatal(err)
	}
	files, err := client.List(ctx, "/cloud-a/library", true)
	if err != nil || len(files) != 1 || files[0].ID != "native-file-1" {
		t.Fatalf("写后快照未刷新: %+v %v", files, err)
	}
	server.mu.Lock()
	lists, moves := server.lists, server.moves
	server.mu.Unlock()
	// 来源、目标及目标父目录写前各读一次，来源及目标写后各读一次。
	if lists != 5 || moves != 1 {
		t.Fatalf("重复读取未合并: lists=%d moves=%d", lists, moves)
	}
}

func TestStandaloneRenameUsesTwoDirectoryReads(t *testing.T) {
	client, server := fixture(t)
	if _, err := client.Rename(context.Background(), "/cloud-a/inbox/movie.mkv", "renamed.mkv"); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	lists := server.lists
	server.mu.Unlock()
	if lists != 2 {
		t.Fatalf("重命名应合并为写前写后各一次: %d", lists)
	}
}

func TestDirectoryCacheCoalescesConcurrentReadsAndAllowsWaiterCancellation(t *testing.T) {
	client, server := fixture(t)
	server.listStarted = make(chan string, 1)
	release := make(chan struct{})
	server.listRelease = release
	ctx, cancel := context.WithTimeout(WithDirectoryCache(context.Background()), 3*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	errorsSeen := make(chan error, 12)
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := client.List(ctx, "/cloud-a/inbox", true)
		errorsSeen <- err
	}()
	select {
	case <-server.listStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waitCtx, stopWait := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stopWait()
	if _, err := client.List(waitCtx, "/cloud-a/inbox", true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待查询不可取消: %v", err)
	}
	for range 11 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := client.List(ctx, "/cloud-a/inbox", true)
			errorsSeen <- err
		}()
	}
	close(release)
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	server.mu.Lock()
	lists := server.lists
	server.mu.Unlock()
	if lists != 1 {
		t.Fatalf("并发读取没有合并: %d", lists)
	}
}

func TestDirectoryLocksAllowIndependentWritesAndCancelOverlaps(t *testing.T) {
	client := &Client{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	releaseA, err := client.lockWrite(ctx, "/cloud-a/inbox")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	releaseB, err := client.lockWrite(ctx, "/cloud-b/library")
	if err != nil {
		t.Fatalf("无关目录被阻塞: %v", err)
	}
	releaseB()
	waitCtx, stopWait := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stopWait()
	if release, err := client.lockWrite(waitCtx, "/cloud-a"); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("重叠路径应等待且可取消: %v", err)
	}
	releaseA()
	releaseA()
	release, err := client.lockWrite(ctx, "/cloud-a")
	if err != nil {
		t.Fatalf("锁释放后无法继续: %v", err)
	}
	release()
}

func TestDirectoryCacheScopesAndForcedReadsRemainIndependent(t *testing.T) {
	client, server := fixture(t)
	ctx := WithDirectoryCache(context.Background())
	for _, force := range []bool{false, true, false, true} {
		if _, err := client.List(ctx, "/cloud-a/inbox", force); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.List(WithDirectoryCache(ctx), "/cloud-a/inbox", true); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	lists := server.lists
	server.mu.Unlock()
	if lists != 3 {
		t.Fatalf("强制刷新或作用域隔离失效: %d", lists)
	}
}
