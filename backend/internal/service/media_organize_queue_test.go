package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMediaQueuePriorityAndStableOrder(t *testing.T) {
	s, _, _, _ := newMediaOrganizeTest(t)
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	for i, item := range []struct {
		id, trigger, action string
	}{
		{"automatic-first", "schedule", "auto"},
		{"manual-first", "manual", "auto"},
		{"retry-first", "schedule", "retry"},
		{"manual-second", "manual", "auto"},
		{"retry-second", "manual", "retry"},
		{"automatic-second", "realtime", "auto"},
	} {
		run := MediaOrganizeRun{ID: item.id, Trigger: item.trigger, Status: "queued", CreatedAt: at.Add(time.Duration(i) * time.Second), UpdatedAt: at.Add(time.Duration(i) * time.Second)}
		s.doc.Runs = append([]MediaOrganizeRun{run}, s.doc.Runs...)
		s.enqueueActionLocked(item.id, item.action)
	}
	var got []string
	for len(s.actions) > 0 {
		run, _, ok := s.takeQueuedRunLocked()
		if !ok || s.active != run.ID {
			t.Fatal("提取队列未保留当前执行批次", run, ok, s.active)
		}
		got = append(got, run.ID)
		s.active = ""
	}
	want := []string{"retry-first", "retry-second", "manual-first", "manual-second", "automatic-first", "automatic-second"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("优先级或同类入队顺序不稳定", got)
	}
	for _, id := range []string{"b", "a"} {
		s.doc.Runs = append(s.doc.Runs, MediaOrganizeRun{ID: id, Trigger: "manual", CreatedAt: at, UpdatedAt: at})
		s.enqueueActionLocked(id, "auto")
	}
	if run, _, _ := s.takeQueuedRunLocked(); run.ID != "a" {
		t.Fatal("相同时间须按编号保持确定顺序", run)
	}
}

func TestMediaQueueRetryAcceptsOtherBatchesAndRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "正常提交", true: "持久化失败"}[fail], func(t *testing.T) {
			s, _, store, rule := newMediaOrganizeTest(t)
			queuedScan, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			history := mediaRecordFixture("history", "partial", 1)
			history.Rule = rule
			history.Entries[0].Status, history.Entries[0].Result = "error", nil
			saveMediaFixtures(t, s, history)
			s.active = "another-active-batch"
			if fail {
				store.err = errors.New("模拟入队持久化失败")
			}
			_, err = s.Retry(history.ID, MediaOrganizeCorrection{FileID: history.Entries[0].File.ID})
			if fail {
				if err == nil || s.actions[history.ID] != "" || s.actions[queuedScan.ID] != "auto" || s.doc.Runs[len(s.doc.Runs)-1].Status != "partial" {
					t.Fatal("回滚后不能遗留重试动作或改坏其他排队批次", err, s.actions, s.doc.Runs)
				}
				return
			}
			if err != nil || s.actions[history.ID] != "retry" || s.actions[queuedScan.ID] != "auto" || s.active != "another-active-batch" {
				t.Fatal("无关批次运行或排队不得阻止历史重试", err, s.actions, s.active)
			}
			if _, err := s.Retry(history.ID, MediaOrganizeCorrection{FileID: history.Entries[0].File.ID}); !errors.Is(err, ErrMediaOrganizeBusy) {
				t.Fatal("同批次排队重试必须仍被拒绝", err)
			}
		})
	}
}

func TestMediaQueueManualPromotionAndFailedPromotion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "提升手动扫描", true: "提升失败回滚"}[fail], func(t *testing.T) {
			s, _, store, rule := newMediaOrganizeTest(t)
			now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
			s.monitorOptions.now = func() time.Time { return now }
			auto, err := s.queueTriggeredScanLocked(rule, "schedule")
			if err != nil {
				t.Fatal(err)
			}
			if repeated, err := s.queueTriggeredScanLocked(rule, "realtime"); err != nil || repeated.ID != auto.ID || len(s.actions) != 1 {
				t.Fatal("同规则自动扫描应合并", repeated, err, s.actions)
			}
			now = now.Add(time.Minute)
			if fail {
				store.err = errors.New("模拟提升失败")
			}
			promoted, err := s.Start(rule.ID)
			if fail {
				if err == nil || s.doc.Runs[0].Trigger != "schedule" || !s.doc.Runs[0].UpdatedAt.Equal(auto.UpdatedAt) || s.actions[auto.ID] != "auto" {
					t.Fatal("提升失败应保留原排队优先级和时刻", err, s.doc.Runs, s.actions)
				}
				return
			}
			if err != nil || promoted.ID != auto.ID || promoted.Trigger != "manual" || !promoted.UpdatedAt.Equal(now) || len(s.actions) != 1 {
				t.Fatal("自动扫描提升为手动后应使用本次入队时刻", promoted, err)
			}
		})
	}
}

func TestMediaQueueReleasesActiveAfterCheckpointFailure(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.RecognitionVersion = "invalid"
	s.removeActionLocked(run.ID)
	s.active = run.ID
	store.err = errors.New("模拟检查点失败")
	if err := s.processQueuedRun(context.Background(), run, "auto"); err == nil || s.active != "" {
		t.Fatal("错误检查点失败也必须释放当前执行状态", err, s.active)
	}
}

func TestMediaQueueRetainedAttachmentsDoNotAllowEmptyRetry(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	run := mediaRecordFixture("retained", "completed", 1)
	run.Rule = rule
	attachment := run.Entries[0]
	attachment.File.ID += ".nfo"
	attachment.File.MediaType, attachment.Status = "attachment", "retained"
	attachment.VideoID = run.Entries[0].File.ID
	run.Entries = append(run.Entries, attachment)
	saveMediaFixtures(t, s, run)
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: run.Entries[0].File.ID}); !errors.Is(err, ErrMediaOrganizeInput) || len(s.actions) != 0 {
		t.Fatal("只剩保留附件的成功任务不能空重试", err, s.actions)
	}
}

func TestMediaQueueRestartKeepsQueuedRetryInterrupted(t *testing.T) {
	s, metadata, store, rule := newMediaOrganizeTest(t)
	run := mediaRecordFixture("queued-retry", "partial", 1)
	run.Rule = rule
	run.Entries[0].Status, run.Entries[0].Result = "error", nil
	saveMediaFixtures(t, s, run)
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: run.Entries[0].File.ID}); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewMediaOrganizer(store, metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := restarted.Snapshot(run.ID)
	if err != nil || stored.Status != "interrupted" || len(restarted.actions) != 0 || restarted.active != "" {
		t.Fatal("重启必须保留任务供主动核实，不自动重放外部写入", err, stored, restarted.actions, restarted.active)
	}
}

func TestMediaQueueFinishingScanKeepsCurrentRuleSettings(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	s.monitorOptions.now = func() time.Time { return now }
	updated, err := s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{ScheduleEnabled: true, IntervalSeconds: 7})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.queueTriggeredScanLocked(updated, "schedule")
	if err != nil {
		t.Fatal(err)
	}
	s.removeActionLocked(run.ID)
	s.active = run.ID
	if _, err := s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{ScheduleEnabled: true, IntervalSeconds: 19}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := s.finishScanRound(run); err != nil || s.doc.Rules[0].IntervalSeconds != 19 || !s.doc.Rules[0].NextScanAt.Equal(now.Add(19*time.Second)) {
		t.Fatal("完成轮次须采用当前间隔，不能回写旧快照", err, s.doc.Rules[0])
	}
	before := s.doc.Rules[0]
	store.err = errors.New("模拟完成调度保存失败")
	now = now.Add(time.Minute)
	if err := s.finishScanRound(run); err == nil || s.doc.Rules[0] != before {
		t.Fatal("调度保存失败须恢复先前规则", err, s.doc.Rules[0], before)
	}
	store.err = nil
	nextRule := before
	nextRule.Target = t.TempDir()
	if _, err := s.SaveRule(nextRule); err != nil {
		t.Fatal(err)
	}
	before = s.doc.Rules[0]
	if err := s.finishScanRound(run); err != nil || s.doc.Rules[0] != before {
		t.Fatal("旧轮次完成不得覆盖业务设置已变化的新规则", err, s.doc.Rules[0], before)
	}
}

func TestMediaQueueBusinessEditRearmsRealtimeScanAfterCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "提交后重新扫描", true: "失败保留原排队"}[fail], func(t *testing.T) {
			s, _, store, rule := newMediaOrganizeTest(t)
			now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
			s.monitorOptions.now = func() time.Time { return now }
			var err error
			rule, err = s.UpdateSchedule(context.Background(), rule.ID, MediaOrganizeSchedule{RealtimeEnabled: true, IntervalSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			local := &mediaLocalRuntime{rule: rule, initialized: true, status: MediaOrganizeMonitorStatus{Mode: "realtime"}, dirtyDue: now}
			s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: local}
			if err := s.schedule(); err != nil || len(s.actions) != 1 || !local.dirtyDue.IsZero() {
				t.Fatal("实时事件应进入队列并消耗待扫描标记", err, s.actions, local.dirtyDue)
			}
			queued := s.doc.Runs[0]
			if queued.Trigger != "realtime" {
				t.Fatal("仅启用实时监控时不应创建定时轮次", queued)
			}
			now = now.Add(time.Second)
			changed := rule
			changed.Target = t.TempDir()
			if fail {
				store.err = errors.New("模拟业务设置保存失败")
			}
			_, err = s.SaveRule(changed)
			if fail {
				if err == nil || !local.dirtyDue.IsZero() || s.doc.Rules[0].Target != rule.Target || len(s.doc.Runs) != 1 || s.doc.Runs[0].ID != queued.ID || s.actions[queued.ID] != "auto" {
					t.Fatal("保存失败不得重置监控运行态或丢失原队列", err, local.dirtyDue, s.doc.Rules, s.doc.Runs, s.actions)
				}
				return
			}
			if err != nil || !local.dirtyDue.Equal(now) || s.actions[queued.ID] != "" || len(s.doc.Runs) != 0 {
				t.Fatal("业务修改提交后应替换待执行实时扫描", err, local.dirtyDue, s.doc.Runs, s.actions)
			}
			if err := s.schedule(); err != nil || len(s.doc.Runs) != 1 || len(s.actions) != 1 || s.doc.Runs[0].ID == queued.ID || s.doc.Runs[0].Rule.Target != changed.Target || !local.dirtyDue.IsZero() {
				t.Fatal("新实时扫描必须使用已提交的目标目录", err, s.doc.Runs, s.actions, local.dirtyDue)
			}
		})
	}
}
