package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func media115AssertPublishedStatus(t *testing.T, s *MediaOrganizer, run *MediaOrganizeRun, fileID, expected string) {
	t.Helper()
	task, err := s.Task(run.ID, fileID)
	if err != nil || task.Status != expected {
		t.Fatal("详情状态应与实际执行阶段一致", expected, task, err)
	}
	history, err := s.History(context.Background(), "", "all", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range history.Items {
		if item.RunID == run.ID && item.FileID == fileID {
			if item.Status != expected {
				t.Fatal("历史状态应与详情一致", expected, item)
			}
			return
		}
	}
	t.Fatal("运行时历史中缺少当前视频", fileID, history)
}

func TestMedia115PrewriteCheckpointsPublishRunningUntilConfirmed(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 1)
	t.Cleanup(files.close)
	run.Status, run.streaming = "running", true
	run.processing = map[string]bool{run.Entries[0].File.ID: true}
	entry := &run.Entries[0]
	entry.Status, entry.Message, entry.ResultCode = "error", "上次整理失败", "OLD_MOVE_FAILED"
	entry.Result = &MediaOrganizeResult{Status: "error", Message: entry.Message, At: time.Now()}
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	check := func() {
		callbacks++
		current, err := s.Snapshot(run.ID)
		if err != nil || len(current.Entries) != 1 {
			t.Fatal(current, err)
		}
		pending := current.Entries[0]
		if pending.Status != "ready" || pending.Result != nil || pending.Message != "" || pending.ResultCode != "" || pending.P115Move == nil {
			t.Fatal("写入前仅保留可恢复意图，清除旧失败结果", pending)
		}
		media115AssertPublishedStatus(t, s, run, entry.File.ID, "running")
		stored, err := s.db.QueryMediaOrganizeLive(context.Background(), "", "failed", 1, nil)
		if err != nil || stored.Total != 0 {
			t.Fatal("预写检查点不能写入失败摘要", stored, err)
		}
	}
	drive.beforeRename, drive.beforeMove = check, check
	if _, err := s.execute115Batch(context.Background(), files, run, drive, []int{0}); err != nil {
		t.Fatal(err)
	}
	if callbacks != 2 || entry.Status != "done" || entry.P115Move != nil || entry.Result == nil || entry.Result.Status != "done" {
		t.Fatal("改名和移动前均保持运行态，目标确认后才成功", callbacks, *entry)
	}
	media115AssertPublishedStatus(t, s, run, entry.File.ID, "done")
	stored, err := s.db.QueryMediaOrganizeLive(context.Background(), "", "all", 1, nil)
	if err != nil || stored.Total != 1 || stored.Counts["done"] != 1 || stored.Counts["failed"] != 0 {
		t.Fatal("成功后保存成功摘要，不能遗留失败摘要", stored, err)
	}
}

func TestMedia115ConfirmedBatchFailureStillPublishesFailed(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 1)
	t.Cleanup(files.close)
	run.Status, run.streaming = "running", true
	drive.moveErr = errors.New("模拟批量移动被拒绝")
	drive.beforeMove = func() {
		media115AssertPublishedStatus(t, s, run, run.Entries[0].File.ID, "running")
	}
	if _, err := s.execute115Batch(context.Background(), files, run, drive, []int{0}); err != nil {
		t.Fatal(err)
	}
	entry := run.Entries[0]
	if entry.Status != "error" || entry.P115Move == nil || entry.Result == nil || entry.Result.Status != "error" {
		t.Fatal("实际移动失败仍保留失败结果和核验意图", entry)
	}
	media115AssertPublishedStatus(t, s, run, entry.File.ID, "failed")
	stored, err := s.db.QueryMediaOrganizeLive(context.Background(), "", "failed", 1, nil)
	if err != nil || stored.Total != 1 {
		t.Fatal("真实失败应保存失败摘要", stored, err)
	}
	mediaFinishRun(run)
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	media115AssertPublishedStatus(t, s, run, entry.File.ID, "failed")
}

func TestMedia115CurrentBatchDoesNotHideEarlierFailedIntent(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 2)
	t.Cleanup(files.close)
	run.Status, run.streaming = "running", true
	run.processing = map[string]bool{run.Entries[0].File.ID: true}
	previous := &run.Entries[1]
	previous.Status, previous.Message = "error", "此前移动结果未确认"
	previous.P115Move = media115BatchIntent(run.Rule, *previous, "20")
	previous.P115Move.Phase = "moving"
	previous.Result = &MediaOrganizeResult{Status: "error", Message: previous.Message, At: time.Now()}
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	drive.beforeMove = func() {
		media115AssertPublishedStatus(t, s, run, run.Entries[0].File.ID, "running")
		media115AssertPublishedStatus(t, s, run, previous.File.ID, "failed")
		stored, err := s.db.QueryMediaOrganizeLive(context.Background(), "", "failed", 1, nil)
		if err != nil || stored.Total != 1 {
			t.Fatal("当前批次预写不能掩盖先前真实失败", stored, err)
		}
	}
	if _, err := s.execute115Batch(context.Background(), files, run, drive, []int{0}); err != nil {
		t.Fatal(err)
	}
	media115AssertPublishedStatus(t, s, run, run.Entries[0].File.ID, "done")
	media115AssertPublishedStatus(t, s, run, previous.File.ID, "failed")
}
