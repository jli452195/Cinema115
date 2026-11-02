package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

type mediaCD2EmptyBatchFake struct {
	*mediaCD2Fake
	mu      sync.Mutex
	batches [][]clouddrive2.EmptyDirectoryRequest
	alter   func([]clouddrive2.EmptyDirectoryResult)
}

func (f *mediaCD2EmptyBatchFake) RemoveEmptyDirectories(ctx context.Context, requests []clouddrive2.EmptyDirectoryRequest) []clouddrive2.EmptyDirectoryResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, slices.Clone(requests))
	results := make([]clouddrive2.EmptyDirectoryResult, len(requests))
	for i, request := range requests {
		results[i].Path = request.Path
		results[i].Removed, results[i].Err = f.RemoveEmptyDirectory(ctx, request.Path, request.ID)
	}
	if f.alter != nil {
		f.alter(results)
	}
	return results
}

func TestMediaOrganizerCD2EmptyBatchSkipsOccupiedDirectoriesAndSharesCheckpoints(t *testing.T) {
	for _, count := range []int{20, 45} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := &mediaCD2EmptyBatchFake{mediaCD2Fake: connection.filesystem}
			for i := range count {
				fs.add(path.Join(rule.Source, fmt.Sprintf("empty-%02d", i)), true, 0)
			}
			for i, name := range []string{"keep.ass", "keep.iso", "download.part", "unknown.bin", "readonly.nfo"} {
				parent := path.Join(rule.Source, fmt.Sprintf("occupied-%d", i))
				fs.add(parent, true, 0)
				file := fs.add(path.Join(parent, name), false, 4)
				if name == "readonly.nfo" {
					file.ReadOnly = true
					fs.files[file.Path] = file
				}
			}
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			files.cd2 = fs
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			repository := s.db.(*testMediaOrganizeStore)
			repository.source = mediaCompletionAuditStore{repository.source, func(string) { writes++ }}
			fs.listed = nil
			if err := s.cleanupEmptyDirectories(context.Background(), files, &run); err != nil {
				t.Fatal(err)
			}
			if len(fs.batches) != (count+19)/20 || len(fs.deleted) != count {
				t.Fatal("同父目录应按20项拆批", len(fs.batches), fs.deleted)
			}
			if writes != 3 {
				t.Fatal("同层应仅保存整批意图、结果和最终摘要，不能逐目录落盘", writes)
			}
			if len(fs.listed) != count {
				t.Fatal("已知非空分支不应再次请求目录", fs.listed)
			}
			for _, batch := range fs.batches {
				if len(batch) > 20 {
					t.Fatal("批量超过上限", batch)
				}
			}
		})
	}
}

func TestMediaOrganizerCD2DirectoryTreeUsesOnlyConfirmedSourceRemovals(t *testing.T) {
	for _, scenario := range []string{"moved", "duplicate", "quarantined", "location-outside", "renamed-only", "reference", "uncertain", "retained"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := &mediaCD2EmptyBatchFake{mediaCD2Fake: connection.filesystem}
			parent := path.Join(rule.Source, "work")
			fs.add(parent, true, 0)
			fs.add(path.Join(parent, "original.mkv"), false, 4)
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			entries, err := files.scan(context.Background())
			if err != nil || len(entries) != 1 {
				t.Fatal(entries, err)
			}
			entry := entries[0]
			entry.Status = "error"
			removed := true
			switch scenario {
			case "moved", "duplicate":
				entry.Status = "done"
			case "quarantined":
				entry.Failure = &MediaOrganizeFailure{State: "stored"}
			case "location-outside":
				entry.CD2Current = &MediaCD2Location{Path: path.Join(rule.Target, "original.mkv")}
			case "renamed-only":
				entry.CD2Current = &MediaCD2Location{Path: path.Join(parent, "renamed.mkv")}
				removed = false
			case "reference":
				entry.Status, entry.CD2Reference = "done", true
				removed = false
			default:
				removed = false
			}
			if scenario == "retained" {
				entry.Status = "retained"
			}
			if removed {
				delete(fs.files, entry.File.ID)
			}
			files.cd2 = fs
			run, _ := s.Start(rule.ID)
			run.Entries = []MediaOrganizeEntry{entry}
			if err := s.cleanupEmptyDirectories(context.Background(), files, &run); err != nil {
				t.Fatal(err)
			}
			if (len(fs.batches) == 1) != removed || (len(fs.deleted) == 1) != removed {
				t.Fatal("来源树扣除了未确认移出来源的子项", scenario, fs.batches, fs.deleted)
			}
		})
	}
}

func TestMediaOrganizerCD2EmptyBatchConfirmsChildrenBeforeParentAndKeepsPartialErrors(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := &mediaCD2EmptyBatchFake{mediaCD2Fake: connection.filesystem}
			parent := path.Join(rule.Source, "tree")
			for _, full := range []string{parent, parent + "/a", parent + "/b"} {
				fs.add(full, true, 0)
			}
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("模拟批量部分异常")
			if failed {
				fs.add(parent+"/b/late.ass", false, 4)
				fs.alter = func(results []clouddrive2.EmptyDirectoryResult) {
					for i := range results {
						if results[i].Path == parent+"/a" {
							results[i].Err = failure
						}
					}
				}
			}
			files.cd2 = fs
			run, _ := s.Start(rule.ID)
			err = s.cleanupEmptyDirectories(context.Background(), files, &run)
			if failed {
				if !errors.Is(err, failure) || len(fs.batches) != 1 || !slices.Equal(fs.deleted, []string{parent + "/a"}) {
					t.Fatal("失败或未删子目录不能释放父目录，也不能重放已确认删除", err, fs.batches, fs.deleted)
				}
				if run.Entries[0].Status != "done" || run.Entries[1].Status != "retained" {
					t.Fatal("部分成功必须逐项保留", run.Entries)
				}
			} else if err != nil || len(fs.batches) != 2 || !slices.Equal(fs.deleted, []string{parent + "/a", parent + "/b", parent}) {
				t.Fatal("子目录确认清空后应向上释放父目录", err, fs.batches, fs.deleted)
			}
		})
	}
}

type mediaCD2EmptyConcurrentFake struct {
	clouddrive2.Filesystem
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
}

func (f *mediaCD2EmptyConcurrentFake) RemoveEmptyDirectories(ctx context.Context, requests []clouddrive2.EmptyDirectoryRequest) []clouddrive2.EmptyDirectoryResult {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for old := f.maximum.Load(); active > old; old = f.maximum.Load() {
		if f.maximum.CompareAndSwap(old, active) {
			break
		}
	}
	select {
	case f.started <- struct{}{}:
	case <-ctx.Done():
	}
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	results := make([]clouddrive2.EmptyDirectoryResult, len(requests))
	for i, request := range requests {
		results[i] = clouddrive2.EmptyDirectoryResult{Path: request.Path, Err: ctx.Err()}
	}
	return results
}

func TestMediaOrganizerCD2EmptyBatchConcurrencyIsBoundedAndJoinsCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			fs := &mediaCD2EmptyConcurrentFake{started: make(chan struct{}), release: make(chan struct{})}
			files := &mediaOrganizeFiles{cd2: fs}
			jobs := make([]mediaCD2EmptyDirectoryJob, 9)
			for i := range jobs {
				jobs[i].requests = []clouddrive2.EmptyDirectoryRequest{{Path: fmt.Sprintf("/source/%d/empty", i)}}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { files.removeCD2EmptyDirectoryJobs(ctx, jobs); close(done) }()
			t.Cleanup(func() { cancel(); <-done })
			for range 4 {
				select {
				case <-fs.started:
				case <-time.After(2 * time.Second):
					t.Fatal("独立父目录没有并行处理")
				}
			}
			if fs.maximum.Load() != 4 {
				t.Fatal("并发未限制在4路", fs.maximum.Load())
			}
			if canceled {
				cancel()
			} else {
				close(fs.release)
				// 后续任务仍需接收启动信号，避免测试通道阻止工作线程退出。
				for range 5 {
					select {
					case <-fs.started:
					case <-time.After(2 * time.Second):
						t.Fatal("后续父目录未被处理")
					}
				}
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("取消后未等待所有工作线程退出")
			}
			if fs.active.Load() != 0 || fs.maximum.Load() > 4 {
				t.Fatal("存在越界并发或未退出的工作线程", fs.active.Load(), fs.maximum.Load())
			}
		})
	}
}
