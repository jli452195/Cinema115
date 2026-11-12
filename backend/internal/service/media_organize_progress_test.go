package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMediaProgressVisibleWhileRecognitionWaits(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "one")
	mediaTestFile(t, rule.Source, "Work.S01E02.strm", "two")
	started, release := make(chan struct{}, 1), make(chan struct{})
	s.metadata = mediaWaitingRecognition{metadata, started, release}
	counter := &mediaAttachmentSaveCounter{mediaOrganizeStorage: s.db}
	s.db = counter
	if _, err := s.Start(rule.ID); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	run, action, found := s.takeQueuedRunLocked()
	s.mu.Unlock()
	if !found {
		t.Fatal("扫描未入队")
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- s.processQueuedRun(ctx, run, action) }()
	defer cancel()
	defer func() { close(release); <-finished }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("识别未进入等待")
	}
	history, err := s.History(ctx, "", "running", 1)
	if err != nil || history.Counts["running"] != 2 || len(history.Activity) != 1 {
		t.Fatal("识别完成前应显示待处理文件和活动进度", history, err)
	}
	activity := history.Activity[0]
	if activity.Stage != "recognizing" || activity.Current != "Work" || activity.Total != 1 || activity.Files != 2 || activity.StartedAt.IsZero() {
		t.Fatal("缺少当前作品、识别组数或开始时间", activity)
	}
	encoded, err := json.Marshal(activity)
	if err != nil || !strings.Contains(string(encoded), `"completed":0`) {
		t.Fatal("等待首组查询时也必须显示零进度", string(encoded), err)
	}
	before := len(counter.saved)
	changes, unsubscribe := s.SubscribeHistoryChanges()
	defer unsubscribe()
	notified := 0
	for i := 0; i < 100; i++ {
		s.publishMediaProgress(&run, "recognizing", "Work", 0, 1)
		select {
		case <-changes:
			notified++
		default:
		}
	}
	if len(counter.saved) != before {
		t.Fatal("识别进度不能反复写入整个批次")
	}
	if notified > 1 {
		t.Fatal("高频进度不应堆积通知")
	}
	if state := s.State(); len(state.Activity) != 1 || state.Activity[0].Stage != "recognizing" {
		t.Fatal("其他页也应能单独读取轻量进度", state)
	}
}

func TestMediaRetainedVideoNeverBecomesFailedHistory(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	run := mediaRecordFixture("waiting-for-stable", "completed", 1)
	entry := &run.Entries[0]
	entry.Status, entry.Message, entry.Result = "retained", "视频仍在变化，留待下一轮", nil
	saveMediaFixtures(t, s, run)
	if tasks := mediaTasks(run); len(tasks) != 0 {
		t.Fatal("等待稳定不是最终失败", tasks)
	}
	history, err := s.History(context.Background(), "", "all", 1)
	if err != nil || history.Counts["all"] != 0 || history.Counts["failed"] != 0 {
		t.Fatal("持久化和查询不能重新制造假失败", history, err)
	}
}
