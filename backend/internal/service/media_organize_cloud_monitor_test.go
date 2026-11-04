package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestMediaCloud115ReceivesEventsWithoutSTRMMapping(t *testing.T) {
	driveSTRM, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.DeleteDriveSTRM(ctx, "mapping"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveDriveSTRMCursor(ctx, "main", "100"); err != nil {
		t.Fatal(err)
	}
	s, _, _, run, _ := newMedia115PipelineTest(t)
	s.accounts = driveSTRM.Accounts
	rule := run.Rule
	rule.AccountID, rule.RealtimeEnabled = "main", true
	s.doc.Rules = []MediaOrganizeRule{rule}
	s.mu.Lock()
	runtime := s.cloudRuntimeLocked(rule)
	runtime.directories["30"] = "Work"
	s.mu.Unlock()
	reader := NewDriveSTRMEvents(driveSTRM, driveSTRMLifeFake(func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		return p115.LifeEventPage{Count: 2, Events: []p115.LifeEvent{{ID: "101", Type: 2, ParentID: "30", FileID: "501"}, {ID: "100", Type: 8}}}, nil
	}))
	reader.Organizer = s
	if err := reader.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(runtime.pending) != 1 || runtime.pending["30"].Relative != "Work" {
		t.Fatal("organizer event was lost")
	}
	if cursor, _ := db.LoadDriveSTRMCursor(ctx, "main"); cursor != "101" {
		t.Fatal("cursor did not advance")
	}
}

func TestMediaCloud115EventsCoalesceAndKeepNewChanges(t *testing.T) {
	s, _, _, run, _ := newMedia115PipelineTest(t)
	rule := run.Rule
	s.doc.Runs = nil
	clear(s.actions)
	rule.RealtimeEnabled = true
	rule.ScheduleEnabled = false
	s.doc.Rules = []MediaOrganizeRule{rule}
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	runtime := s.cloudRuntimeLocked(rule)
	runtime.initialized = true
	runtime.directories["30"] = "Work"
	runtime.directories["31"] = "Work/Season 01"
	runtime.directories["32"] = "Work/Season 02"
	runtime.directories["33"] = "Other"
	if err := s.notify115Directories(context.Background(), rule.AccountID, []string{"31", "32"}); err != nil {
		t.Fatal(err)
	}
	if runtime.full || len(runtime.pending) != 1 || runtime.pending["30"].Relative != "Work" {
		t.Fatal("同作品的各季事件应合并为作品范围", runtime)
	}
	now = now.Add(3 * time.Second)
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	queued, _, ok := s.takeQueuedRunLocked()
	if !ok || len(queued.scanScopes) != 1 || queued.scanScopes[0].Relative != "Work" {
		t.Fatal("应只扫描变化作品", queued.scanScopes)
	}
	if err := s.notify115Directories(context.Background(), rule.AccountID, []string{"33"}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.pending) != 1 || runtime.pending["33"].Relative != "Other" {
		t.Fatal("运行中到达的新事件不能丢失", runtime.pending)
	}
}

func TestMediaCloudPartialScanKeepsUnrelatedBranchesAndFullDeadline(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	for _, work := range []string{"Work", "Other"} {
		fs.add(path.Join(rule.Source, work), true, 0)
		fs.add(path.Join(rule.Source, work, "orphan.ass"), false, 4)
	}
	rule.AttachmentGraceSeconds = 0
	rule.ScheduleEnabled = true
	rule.NextScanAt = time.Now().Add(time.Hour)
	s.doc.Rules = []MediaOrganizeRule{rule}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.doc.Runs[0].scanScopes = []mediaScanScope{{ID: path.Join(rule.Source, "Work"), Relative: "Work"}}
	run, action, _ := s.takeQueuedRunLocked()
	if err := s.processQueuedRun(context.Background(), run, action); err != nil {
		t.Fatal(err)
	}
	if _, exists := fs.files[path.Join(rule.Source, "Work")]; exists {
		t.Fatal("局部作品清空后应删除自身目录")
	}
	if _, exists := fs.files[path.Join(rule.Source, "Other/orphan.ass")]; !exists {
		t.Fatal("未变化作品不能被局部清理")
	}
	if slices.Contains(fs.listed, path.Join(rule.Source, "Other")) || !s.doc.Rules[0].NextScanAt.Equal(rule.NextScanAt) {
		t.Fatal("局部扫描不能遍历其他作品或推迟全扫")
	}
}

func TestMediaCloudCompletedBranchCleansBeforeNextWork(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "a-work", Directory: true}, {ID: "31", Name: "z-broken", Directory: true}}
	drive.paths["30"], drive.paths["31"] = "/inbox/a-work", "/inbox/z-broken"
	drive.dirs["30"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, {ID: "502", Name: "unrelated.nfo", Size: 8}}
	failure := errors.New("后续作品不可读")
	drive.beforeDirectory = func(id string) error {
		if id != "31" {
			return nil
		}
		if !slices.Contains(drive.deleted, "30") || !slices.Contains(drive.deleted, "502") {
			t.Fatal("读取下一作品前应清理完成作品及残留附件", drive.deleted)
		}
		return failure
	}
	if err := s.process115Auto(context.Background(), files, run); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(run.Entries, func(e MediaOrganizeEntry) bool { return e.File.ID == "501" && e.Status == "done" }) {
		t.Fatal("后续失败不能丢失已成功的视频")
	}
	if slices.Contains(drive.deleted, "31") || slices.Contains(drive.deleted, "10") {
		t.Fatal("未读完整分支与来源根不能删除")
	}
}

func TestMediaCloudStopFinishesIssuedBatchAndReleasesWorker(t *testing.T) {
	s, fs, _, rule := newMediaCD2BatchOrganizer(t)
	s.doc.Rules = []MediaOrganizeRule{rule}
	entries := make([]MediaOrganizeEntry, 0, 25)
	for i := 1; i <= 25; i++ {
		entries = append(entries, mediaCD2BatchEntry(fs, rule, fmt.Sprintf("old-%02d.mkv", i), fmt.Sprintf("Work/S01E%02d.mkv", i)))
	}
	run := mediaCD2BatchRun(t, s, rule, entries...)
	s.removeActionLocked(run.ID)
	s.active = run.ID
	s.doc.Rules[0].RealtimeEnabled, s.doc.Rules[0].ScheduleEnabled = true, true
	fs.beforeMove = func([]string) {
		if _, err := s.Stop(context.Background(), rule.ID); err != nil {
			t.Fatal(err)
		}
		fs.beforeMove = nil
	}
	if err := s.processQueuedRun(context.Background(), run, "execute"); err != nil {
		t.Fatal(err)
	}
	if len(fs.moves) != 1 || len(fs.moves[0]) != 20 {
		t.Fatal("停止后应收口已发出的二十项，不发下一批", fs.moves)
	}
	if s.active != "" || len(s.stopping) != 0 || s.doc.Rules[0].RealtimeEnabled || s.doc.Rules[0].ScheduleEnabled {
		t.Fatal("停止后应释放运行状态并暂停自动触发", s.active, s.stopping, s.doc.Rules)
	}
	stored := s.doc.Runs[0]
	if len(stored.Entries) != 20 || slices.ContainsFunc(stored.Entries, func(e MediaOrganizeEntry) bool { return e.Status != "done" }) {
		t.Fatal("未执行文件不能写成失败", stored.Entries)
	}
	if _, err := s.Start(rule.ID); err != nil {
		t.Fatal("停止后应能重新手动运行", err)
	}
}

func TestMediaCloudStopClearsQueuedRun(t *testing.T) {
	s, _, _, _, rule := newMediaCD2Organizer(t)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Stop(context.Background(), rule.ID); err != nil {
		t.Fatal(err)
	}
	if s.actions[run.ID] != "" || s.State().Active {
		t.Fatal("停止排队任务后不能残留运行占用")
	}
}

func TestMediaCloudOrphanDeadlineQueuesLocalScope(t *testing.T) {
	s, _, _, _, rule := newMediaCD2Organizer(t)
	rule.ScheduleEnabled = true
	rule.AttachmentGraceSeconds = 10
	s.doc.Rules = []MediaOrganizeRule{rule}
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	file := OrganizeFile{ShareFile: p115.ShareFile{ID: rule.Source + "/Work/a.ass"}, ParentID: rule.Source + "/Work", SourcePath: "Work/a.ass", MediaType: "subtitle"}
	s.scheduleOrphanScopes(rule, []MediaOrganizeEntry{{File: file}}, map[string]mediaOrphan{file.ID: {Since: now}}, nil)
	runtime := s.cloudMonitors[rule.ID]
	if !runtime.due.Equal(now.Add(10*time.Second)) || len(runtime.pending) != 1 || runtime.full {
		t.Fatal("附件应十秒后仅补查作品", runtime)
	}
	rule.ScheduleEnabled = false
	s.doc.Rules = []MediaOrganizeRule{rule}
	now = now.Add(11 * time.Second)
	if err := s.schedule(); err != nil || len(s.actions) != 0 {
		t.Fatal("暂停后不能执行残留的附件补查", err)
	}
}
