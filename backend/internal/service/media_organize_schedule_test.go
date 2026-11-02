package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/storage"
)

func TestMediaOrganizerScheduleClockAndCoalescing(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{ScheduleEnabled: true, IntervalSeconds: 7}); err != nil {
		t.Fatal(err)
	}
	if err := s.schedule(); err != nil || len(s.actions) != 1 {
		t.Fatal("启用后应立即排入一次扫描", err, s.actions)
	}
	next := s.doc.Rules[0].NextScanAt
	if !next.Equal(now.Add(7 * time.Second)) {
		t.Fatal("未使用自定义秒数", next)
	}
	s.doc.Runs[0].Status = "completed"
	clear(s.actions)
	now = now.Add(3 * time.Second)
	manual, err := s.Start(rule.ID)
	if err != nil || manual.Trigger != "manual" || !s.doc.Rules[0].NextScanAt.Equal(next) {
		t.Fatal("手动扫描不得推迟定时周期", err, manual)
	}
	s.doc.Runs[0].Status = "completed"
	clear(s.actions)
	now = next
	if err := s.schedule(); err != nil || len(s.actions) != 1 {
		t.Fatal("手动扫描后到期仍应调度", err)
	}
	active := s.doc.Runs[0].ID
	s.active = active
	s.doc.Runs[0].Status = "running"
	now = now.Add(time.Minute)
	for range 3 {
		if err := s.schedule(); err != nil || len(s.actions) != 1 {
			t.Fatal("运行中的到期轮次不得重复堆积", err, s.actions)
		}
	}
	s.active = ""
	s.doc.Runs[0].Status = "completed"
	clear(s.actions)
	if err := s.schedule(); err != nil || len(s.actions) != 1 {
		t.Fatal("运行期间已到期，应在完成后合并补扫一次", err)
	}
}

func TestMediaOrganizerPauseQueueAndPersistenceRollback(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	settings := MediaOrganizeSchedule{ScheduleEnabled: true, IntervalSeconds: 19}
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	queued := s.doc.Runs[0]
	store.err = errors.New("模拟存储失败")
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{IntervalSeconds: 19}); err == nil {
		t.Fatal("应报告持久化失败")
	}
	store.err = nil
	if len(s.doc.Runs) != 1 || s.doc.Runs[0].ID != queued.ID || s.actions[queued.ID] != "auto" || !s.doc.Rules[0].ScheduleEnabled {
		t.Fatal("失败暂停必须同时保留排队记录与执行动作", s.doc, s.actions)
	}
	manual, err := s.Start(rule.ID)
	if err != nil || manual.ID != queued.ID || manual.Trigger != "manual" {
		t.Fatal("立即运行应把复用的未执行扫描提升为手动任务", err, manual)
	}
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{IntervalSeconds: 19}); err != nil {
		t.Fatal(err)
	}
	if s.actions[manual.ID] != "auto" || len(s.doc.Runs) != 1 {
		t.Fatal("暂停不得取消用户明确提交的手动任务", s.actions, s.doc.Runs)
	}
	s.doc.Runs[0].Status = "completed"
	clear(s.actions)
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{IntervalSeconds: 19}); err != nil {
		t.Fatal(err)
	}
	if len(s.actions) != 0 || len(s.doc.Runs) != 0 || !s.doc.Rules[0].NextScanAt.IsZero() {
		t.Fatal("暂停应取消尚未执行的自动扫描", s.actions, s.doc.Runs)
	}
}

func TestMediaOrganizerScheduleOnlyLocalReadiness(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://original/")
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{ScheduleEnabled: true, IntervalSeconds: 11}); err != nil {
		t.Fatal(err)
	}
	s.monitorRunning = true
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	run := s.doc.Runs[0]
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" || len(run.Entries) != 1 || run.Entries[0].Status != "done" {
		t.Fatal("仅定时扫描不能因没有实时运行时而永久跳过文件", run)
	}
}

func TestMediaOrganizerUnavailableRealtimeDoesNotEnableSchedule(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.RealtimeEnabled = true
	s.doc.Rules[0] = rule
	s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: {
		rule: rule, initialized: true, status: MediaOrganizeMonitorStatus{Mode: "unavailable"},
		dirtyDue: time.Now().Add(-time.Second), settleDue: time.Now().Add(-time.Second), retryDue: time.Now().Add(-time.Second),
	}}
	if err := s.schedule(); err != nil || len(s.actions) != 0 {
		t.Fatal("监听失败不得偷偷开启定时扫描", err, s.actions)
	}
}

func TestMediaOrganizerEditingPreservesWatchAndRunningSnapshot(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule = mediaMonitorEnable(t, s, rule)
	s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: {rule: rule, files: make(map[string]mediaLocalObservation)}}
	originalWatch := s.localMonitors[rule.ID]
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.active = run.ID
	updated := rule
	updated.Name, updated.Target, updated.IntervalSeconds = "下一批配置", t.TempDir(), 13
	if _, err := s.SaveRule(updated); err != nil {
		t.Fatal("正在运行仍应允许编辑下一批配置", err)
	}
	if s.doc.Runs[0].Rule.Target != rule.Target || s.doc.Rules[0].Target != updated.Target || !mediaSameWatchRule(originalWatch.rule, updated) {
		t.Fatal("修改目标和周期不得替换当前快照或来源观察", s.doc)
	}
	if err := s.DeleteRule(rule.ID); !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("执行中的当前规则不能删除", err)
	}
	updated.Source = t.TempDir()
	if mediaSameWatchRule(originalWatch.rule, updated) {
		t.Fatal("来源变化必须重置监听与稳定证据")
	}
}

func TestMediaOrganizerStoredIntervalsPreserved(t *testing.T) {
	s, _, _, _ := newMediaOrganizeTest(t)
	for _, raw := range []string{
		`[{"id":"local","storage":"local","enabled":true,"intervalMinutes":9},{"id":"cd2","storage":"cd2","enabled":true}]`,
		`{"rules":[{"id":"local","storage":"local","enabled":true,"intervalMinutes":9},{"id":"cd2","storage":"cd2","enabled":true}]}`,
	} {
		if err := s.decodeSQLite(storage.MediaOrganizeRows{Rules: raw}); err != nil {
			t.Fatal(err)
		}
		if len(s.doc.Rules) != 2 || s.doc.Rules[0].IntervalSeconds != 540 || !s.doc.Rules[0].RealtimeEnabled || !s.doc.Rules[0].ScheduleEnabled || s.doc.Rules[1].IntervalSeconds != 20 || s.doc.Rules[1].RealtimeEnabled || !s.doc.Rules[1].ScheduleEnabled {
			t.Fatal("读取原工作区应保留周期和开关", s.doc.Rules)
		}
	}
	raw := `{"rules":[{"id":"new","storage":"local","realtimeEnabled":true,"scheduleEnabled":false,"intervalSeconds":37,"nextScanAt":"2026-11-03T00:00:00Z"}]}`
	if err := s.decodeSQLite(storage.MediaOrganizeRows{Rules: raw}); err != nil {
		t.Fatal(err)
	}
	if s.doc.Rules[0].IntervalSeconds != 37 || !s.doc.Rules[0].RealtimeEnabled || s.doc.Rules[0].ScheduleEnabled || s.doc.Rules[0].NextScanAt.IsZero() {
		t.Fatal("新字段应保持原值", s.doc.Rules)
	}
}
