package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

// A trusted, already-previewed season with existing destination directories.
// This isolates execution traffic from TMDB, scanning and preview traffic.
func organizeBatchFixture(t *testing.T, count int) (*ManualOrganize, *organizeFakeDrive, string, OrganizeJob) {
	t.Helper()
	s, drive, id, _ := newOrganizeTest(t)
	account, err := s.accounts.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	job := OrganizeJob{ID: "batch-plan", Config: s.Config(), Revision: account.Revision, Kind: "tv", TMDBID: 42, Title: "测试作品", Folder: "测试作品 (2024) {tmdb-42}", Status: "preview", CreatedAt: time.Now(), Items: make([]OrganizeItem, 0, count)}
	drive.dirs["30"] = make([]p115.ShareFile, 0, count)
	drive.dirs["20"] = []p115.ShareFile{{ID: "40", Name: job.Folder, Directory: true}}
	drive.dirs["40"] = []p115.ShareFile{{ID: "50", Name: "Season 01", Directory: true}}
	drive.dirs["50"] = []p115.ShareFile{}
	for i := 0; i < count; i++ {
		file := p115.ShareFile{ID: fmt.Sprint(10000 + i), Name: fmt.Sprintf("Wrong.S01E%03d.mkv", i+1), Size: int64(100 + i)}
		drive.dirs["30"] = append(drive.dirs["30"], file)
		item := OrganizeItem{File: OrganizeFile{ShareFile: file, ParentID: "30", SourcePath: path.Join("/来源/待整理", file.Name)}, Directory: path.Join(job.Folder, "Season 01"), Name: fmt.Sprintf("测试作品 - S01E%03d.mkv", i+1), Status: "pending"}
		item.TargetPath = path.Join(job.Config.TargetPath, item.Directory, item.Name)
		job.Items = append(job.Items, item)
	}
	s.plans[job.ID] = job
	return s, drive, id, job
}

func TestManualOrganizeBatchRequestReduction(t *testing.T) {
	s, drive, id, plan := organizeBatchFixture(t, 100)
	drive.pageSize = 100
	job := organizeTestRun(t, s, id, plan)
	if job.Status != "completed" || len(drive.dirs["50"]) != 100 || len(drive.dirs["30"]) != 0 {
		t.Fatal("整季未整理完成", job.Status)
	}
	reads := 0
	for _, count := range drive.listCalls {
		reads += count
	}
	requests := reads + drive.creates + drive.renames + drive.moves
	if requests != 16 || drive.renames != 2 || drive.moves != 2 || drive.listCalls["20"] != 1 || drive.listCalls["40"] != 1 {
		t.Fatalf("重复扫描或批量写入退化: requests=%d reads=%v renames=%d moves=%d", requests, drive.listCalls, drive.renames, drive.moves)
	}
	if job.Stats.DirectoryRequests != reads || job.Stats.RenameRequests != 2 || job.Stats.MoveRequests != 2 || job.Stats.BatchesCompleted != 2 || job.Stats.Checkpoints != 10 {
		t.Fatalf("批次统计错误: %+v", job.Stats)
	}
	for _, batch := range drive.renameBatches {
		if len(batch) != 50 {
			t.Fatal("未按 50 个文件发送改名", len(batch))
		}
	}
	t.Logf("100 files, existing folders, single-page directories: 900 -> %d drive requests; 502 -> %d execution checkpoints", requests, job.Stats.Checkpoints)
}

func TestManualOrganizeBatchPartialOutcomes(t *testing.T) {
	for _, phase := range []string{"rename", "move"} {
		for _, responseError := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/error=%v", phase, responseError), func(t *testing.T) {
				s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
				applied := map[string]bool{plan.Items[0].File.ID: true, plan.Items[2].File.ID: true}
				if phase == "rename" {
					drive.renameApplyIDs = applied
					if responseError {
						drive.renameErr = context.DeadlineExceeded
					}
				} else {
					drive.moveApplyIDs = applied
					if responseError {
						drive.moveErr = context.DeadlineExceeded
					}
				}
				job := organizeTestRun(t, s, id, plan)
				if job.Status != "partial" || job.Stats.BatchesCompleted != 0 || drive.renames != 1 || job.Items[p115.DriveMutationBatchLimit].Status != "pending" {
					t.Fatal("部分成功后不能处理后续批次", job.Status, drive.renames, job.Items[p115.DriveMutationBatchLimit])
				}
				for i := 0; i < p115.DriveMutationBatchLimit; i++ {
					want := "uncertain"
					if applied[job.Items[i].File.ID] {
						want = "renamed"
						if phase == "move" {
							want = "done"
						}
					}
					if job.Items[i].Status != want {
						t.Fatalf("row %d=%s want %s", i, job.Items[i].Status, want)
					}
				}
				if phase == "rename" && drive.moves != 0 {
					t.Fatal("改名未全部确认时不能开始移动")
				}
				if phase == "move" && drive.moves != 1 {
					t.Fatal("部分成功的批量移动不得拆分重试")
				}
				if _, err := s.Start(id, plan.ID); err != nil || drive.renames != 1 {
					t.Fatal("重复提交不可重放批次", err)
				}
			})
		}
	}
}

func TestManualOrganizeBatchConflictChecksStayFresh(t *testing.T) {
	t.Run("collision in last row blocks whole batch", func(t *testing.T) {
		s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
		drive.dirs["50"] = append(drive.dirs["50"], p115.ShareFile{ID: "900", Name: plan.Items[p115.DriveMutationBatchLimit-1].Name})
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.renames+drive.moves != 0 {
			t.Fatal("必须先验证整批，不可改动前面的文件")
		}
	})
	t.Run("destination changes after rename", func(t *testing.T) {
		s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
		drive.afterRename = func() {
			drive.dirs["50"] = append(drive.dirs["50"], p115.ShareFile{ID: "900", Name: plan.Items[2].Name})
		}
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.moves != 0 || job.Items[0].Status != "renamed" || job.Items[p115.DriveMutationBatchLimit].Status != "pending" {
			t.Fatal("移动前未重新检查目标")
		}
	})
	t.Run("destination changes between batches", func(t *testing.T) {
		s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
		drive.afterMove = func() {
			drive.dirs["50"] = append(drive.dirs["50"], p115.ShareFile{ID: "900", Name: plan.Items[p115.DriveMutationBatchLimit].Name})
		}
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.moves != 1 || drive.renames != 1 || job.Items[p115.DriveMutationBatchLimit-1].Status != "done" || job.Items[p115.DriveMutationBatchLimit].Status != "blocked" {
			t.Fatal("下一批不得使用旧的目标目录内容")
		}
	})
	t.Run("source changes between batches", func(t *testing.T) {
		s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
		drive.afterMove = func() { drive.dirs["30"][0].Name = "changed.mkv" }
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.renames != 1 || job.Items[p115.DriveMutationBatchLimit].Status != "blocked" {
			t.Fatal("下一批不得使用旧的来源目录内容")
		}
	})
	t.Run("cached directory deleted", func(t *testing.T) {
		s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
		drive.beforeRead = func(cid string, _ int) error {
			if drive.moves > 0 && cid == "30" {
				delete(drive.dirs, "50")
			}
			return nil
		}
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.moves != 1 || drive.renames != 1 {
			t.Fatal("失效的缓存目录不得继续写入")
		}
	})
}

func TestManualOrganizeBatchFullPaginationAndExactSelection(t *testing.T) {
	s, drive, id, plan := organizeBatchFixture(t, 101)
	drive.pageSize = 7
	drive.dirs["30"] = append(drive.dirs["30"], p115.ShareFile{ID: "900", Name: "new-arrival.mkv", Size: 999})
	job := organizeTestRun(t, s, id, plan)
	if job.Status != "completed" || drive.moves != 3 || len(drive.dirs["50"]) != 101 || len(drive.dirs["30"]) != 1 || drive.dirs["30"][0].ID != "900" {
		t.Fatal("分页或精确文件范围被破坏")
	}
	seen := make(map[string]bool)
	for _, batch := range drive.moveBatches {
		if len(batch) > p115.DriveMutationBatchLimit {
			t.Fatal("批次超限")
		}
		for _, id := range batch {
			if seen[id] || id == "900" {
				t.Fatal("重复或额外文件被移动")
			}
			seen[id] = true
		}
	}
	if len(seen) != 101 {
		t.Fatal("文件遗漏")
	}
}

func TestManualOrganizeBatchNoUnnecessaryWrites(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		t.Run(fmt.Sprint(inPlace), func(t *testing.T) {
			s, drive, id, plan := organizeBatchFixture(t, 5)
			for i := range plan.Items {
				plan.Items[i].File.Name = plan.Items[i].Name
				drive.dirs["30"][i].Name = plan.Items[i].Name
			}
			if inPlace {
				drive.dirs["40"][0].ID = "30"
			}
			s.plans[plan.ID] = plan
			job := organizeTestRun(t, s, id, plan)
			if job.Status != "completed" || drive.renames != 0 {
				t.Fatal("已正确命名的文件不应再次改名")
			}
			if inPlace && drive.moves != 0 {
				t.Fatal("已在正确目录的文件不应再次移动")
			}
		})
	}
}

func TestManualOrganizeBatchReadbackFailureStopsWithoutReplay(t *testing.T) {
	s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
	drive.beforeRead = func(cid string, _ int) error {
		if drive.renames > 0 && cid == "30" {
			return errors.New("read unavailable")
		}
		return nil
	}
	job := organizeTestRun(t, s, id, plan)
	if job.Status != "partial" || drive.moves != 0 || drive.renames != 1 {
		t.Fatal("读回失败不得继续或重试写操作")
	}
	for i := 0; i < p115.DriveMutationBatchLimit; i++ {
		if job.Items[i].Status != "uncertain" {
			t.Fatal("未确认状态丢失")
		}
	}
}

type organizeCheckpointStore struct {
	data      string
	failStage string
	snapshots []organizeDocument
}

func (s *organizeCheckpointStore) Value() string { return s.data }
func (s *organizeCheckpointStore) Save(raw string) error {
	var doc organizeDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return err
	}
	if len(doc.Jobs) > 0 {
		for _, item := range doc.Jobs[0].Items {
			if item.Status == s.failStage {
				return errors.New("disk full")
			}
		}
	}
	s.data = raw
	s.snapshots = append(s.snapshots, doc)
	return nil
}

func TestManualOrganizeBatchDurableIntentBeforeWrites(t *testing.T) {
	for _, stage := range []string{"renaming", "moving"} {
		t.Run(stage, func(t *testing.T) {
			s, drive, id, plan := organizeBatchFixture(t, p115.DriveMutationBatchLimit+1)
			store := &organizeCheckpointStore{failStage: stage}
			s.store = store
			if _, err := s.Start(id, plan.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.runJob(context.Background(), &s.pending.job, s.pending.cookie); err == nil {
				t.Fatal("应报告阶段保存失败")
			}
			if drive.moves != 0 || (stage == "renaming" && drive.renames != 0) {
				t.Fatal("未持久化意图就发出了写请求")
			}
			restarted, err := newManualOrganizeTest(store, s.accounts, drive, organizeFakeMetadata{})
			if err != nil {
				t.Fatal(err)
			}
			if restarted.pending != nil || restarted.History()[0].Status != "interrupted" {
				t.Fatal("重启不得重放未确认的批次")
			}
			if strings.Contains(store.data, "secret-cookie") {
				t.Fatal("记录泄漏账号凭据")
			}
		})
	}
}

func TestManualOrganizeBatchGroupsSourceAndDestination(t *testing.T) {
	s, drive, id, plan := organizeBatchFixture(t, 8)
	drive.dirs["31"] = []p115.ShareFile{}
	drive.dirs["51"] = []p115.ShareFile{}
	drive.dirs["40"] = append(drive.dirs["40"], p115.ShareFile{ID: "51", Name: "Season 02", Directory: true})
	for i := 4; i < 8; i++ {
		plan.Items[i].File.ParentID = "31"
		drive.dirs["31"] = append(drive.dirs["31"], drive.dirs["30"][i])
	}
	drive.dirs["30"] = drive.dirs["30"][:4]
	for _, i := range []int{2, 3, 6, 7} {
		plan.Items[i].Directory = path.Join(plan.Folder, "Season 02")
	}
	s.plans[plan.ID] = plan
	job := organizeTestRun(t, s, id, plan)
	if job.Status != "completed" || job.Stats.BatchesTotal != 4 || drive.moves != 4 || len(drive.dirs["50"]) != 4 || len(drive.dirs["51"]) != 4 {
		t.Fatal("不同来源或目标必须分开处理")
	}
	if drive.listCalls["20"] != 1 {
		t.Fatal("共用作品目录重复查找")
	}
}
