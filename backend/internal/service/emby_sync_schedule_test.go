package service

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestEmbySyncScheduleDefaultsAndCalendar(t *testing.T) {
	settings := embySyncSettings(EmbyConfig{})
	if !settings.Enabled || settings.IntervalDays != 7 || settings.Time != "03:00" {
		t.Fatal("默认应每周凌晨校准", settings)
	}
	base := time.Date(2026, 12, 28, 20, 30, 0, 0, time.UTC)
	if got := embySyncAfter(base, settings).Format(time.RFC3339); got != "2027-01-05T03:00:00+08:00" {
		t.Fatal("周期应按北京时间跨年计算", got)
	}
	for _, invalid := range []EmbySyncSettings{
		{IntervalDays: 0, Time: "03:00"}, {IntervalDays: 31, Time: "03:00"},
		{IntervalDays: 7, Time: "3:00"}, {IntervalDays: 7, Time: "24:00"},
		{IntervalDays: 7, Time: "03:00:00"},
	} {
		if validateEmbySyncSettings(invalid) == nil {
			t.Fatal("无效设置未拒绝", invalid)
		}
	}
	c := EmbyConfig{APIKey: "test", LibraryIDs: []string{"lib"}}
	state := model.EmbyState{Status: "idle", Active: "snapshot", SyncedAt: base.Format(time.RFC3339)}
	if got := embyNextSyncAt(c, state, base); got != "2027-01-05T03:00:00+08:00" {
		t.Fatal("未显示正确的下次时间", got)
	}
	state.Status = "queued"
	if embyNextSyncAt(c, state, base) != "" {
		t.Fatal("排队任务不应显示另一个自动计划")
	}
}

func TestEmbySyncSettingsPersistenceAndIsolation(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	initial, err := f.service.Status(ctx)
	if err != nil || initial.Status != "queued" {
		t.Fatal("首次保存应自动排队", initial, err)
	}
	f.sync(t)
	before := f.service.snapshot()
	settings := EmbySyncSettings{Enabled: false, IntervalDays: 14, Time: "04:15"}
	if err := f.service.SaveSyncSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	after := f.service.snapshot()
	if after.APIKey != before.APIKey || embyScope(after) != embyScope(before) || after.WebhookSecret != before.WebhookSecret {
		t.Fatal("独立设置不应覆盖连接或同步范围")
	}
	if err := f.service.Save(ctx, before); err != nil {
		t.Fatal(err)
	}
	if f.service.Config().SyncSettings != settings {
		t.Fatal("连接页面的旧草稿覆盖了校准设置")
	}
	state, err := f.service.Status(ctx)
	if err != nil || state.Status != "idle" || state.NextSyncAt != "" {
		t.Fatal("相同范围保存不应重复同步，关闭后不应有计划", state, err)
	}
	secrets, err := NewSessionStore(f.service.secrets.path, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewEmbyService(secrets, f.db)
	if err != nil || restored.Config().SyncSettings != settings {
		t.Fatal("重启后计划未恢复", err)
	}
	if err := f.service.Save(ctx, EmbyConfig{URL: f.server.URL, LibraryIDs: []string{"movies"}}); err != nil {
		t.Fatal(err)
	}
	state, err = f.service.Status(ctx)
	if err != nil || state.Status != "queued" || state.NextSyncAt != "" {
		t.Fatal("关闭定时后范围变化仍应自动排队", state, err)
	}
}

func TestEmbySyncScheduleDueAndDisabled(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	c := f.service.snapshot()
	scope := embyScope(c)
	state, err := f.db.EmbyState(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	next, err := time.Parse(time.RFC3339, embyNextSyncAt(c, state, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if queued, err := f.service.queueDueSync(ctx, c, state, next.Add(-time.Second)); err != nil || queued {
		t.Fatal("未到时间不应同步", queued, err)
	}
	settings := embySyncSettings(c)
	settings.Enabled = false
	if err := f.service.SaveSyncSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if queued, err := f.service.queueDueSync(ctx, c, state, next); err != nil || queued {
		t.Fatal("关闭后旧配置不能自动入队", queued, err)
	}
	settings.Enabled = true
	if err := f.service.SaveSyncSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if queued, err := f.service.queueDueSync(ctx, c, state, next); err != nil || !queued {
		t.Fatal("到期应自动入队", queued, err)
	}
	state, err = f.db.EmbyState(ctx, scope)
	if err != nil || state.Status != "queued" || state.Active == "" {
		t.Fatal("计划入队应保留旧快照", state, err)
	}
}

func TestEmbySyncScheduleScopeChangeDiscardsHistoricalStage(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	c := f.service.snapshot()
	scope := embyScope(c)
	if err := f.db.EmbyBeginSync(ctx, scope, "historical-stage", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyStage(ctx, scope, "historical-stage", []model.EmbyItem{{ID: "stale", Type: "Movie", TMDB: "99999"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyCompleteStage(ctx, scope, "historical-stage", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyFailSync(ctx, scope, "historical-stage", "发布失败", false); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Save(ctx, EmbyConfig{URL: f.server.URL, LibraryIDs: []string{"movies"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	state, err := f.db.EmbyState(ctx, scope)
	if err != nil || state.Status != "queued" || state.StagingComplete || state.StagingGeneration != "" || state.Active == "" {
		t.Fatal("切回历史范围应保留旧正式索引并丢弃过期暂存", state, err)
	}
	if err := f.service.syncLibrary(ctx, c); err != nil {
		t.Fatal(err)
	}
	if f.match(t, "movie", "100").State != "present" || f.match(t, "movie", "99999").State != "absent" {
		t.Fatal("切回范围没有重新读取 Emby 的当前目录")
	}
}

func TestEmbySyncSettingsRescheduleExhaustedCycle(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	c := f.service.snapshot()
	scope := embyScope(c)
	if err := f.service.SaveSyncSettings(ctx, EmbySyncSettings{Enabled: true, IntervalDays: 30, Time: "03:00"}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyProgress(ctx, scope, "failed", 0, "连接失败"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyRecordSyncRetry(ctx, scope, 3, time.Now().AddDate(0, 0, 30).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	settings := EmbySyncSettings{Enabled: true, IntervalDays: 1, Time: "05:00"}
	if err := f.service.SaveSyncSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	state, err := f.db.EmbyState(ctx, scope)
	want := embySyncAfter(time.Now(), settings).Format(time.RFC3339)
	if err != nil || state.SyncRetryAt != want || state.SyncRetryCount != 3 || state.Active == "" {
		t.Fatal("修改周期应重新安排已耗尽重试的下一轮", state, want, err)
	}
}

func TestEmbySyncScheduleFailureBackoffAndManualRetry(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	c := f.service.snapshot()
	scope := embyScope(c)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, embySyncLocation)
	f.mu.Lock()
	f.failPage = true
	f.mu.Unlock()
	for failure := 1; failure <= 3; failure++ {
		if failure == 1 {
			if err := f.service.StartSync(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.service.syncLibrary(ctx, c); err != nil {
			t.Fatal(err)
		}
		state, err := f.db.EmbyState(ctx, scope)
		if err != nil || state.Status != "failed" || state.Active == "" {
			t.Fatal("失败未保留已发布快照", state, err)
		}
		if queued, err := f.service.queueDueSync(ctx, c, state, now); err != nil || queued {
			t.Fatal("失败不应立即重扫", queued, err)
		}
		state, err = f.db.EmbyState(ctx, scope)
		_, want := embySyncRetry(now, embySyncSettings(c), failure-1)
		if err != nil || state.SyncRetryCount != failure || state.SyncRetryAt != want.Format(time.RFC3339) {
			t.Fatal("重试次数或时间未持久化", state, err)
		}
		if err := f.db.EmbyRecover(ctx); err != nil {
			t.Fatal(err)
		}
		if queued, err := f.service.queueDueSync(ctx, c, state, want.Add(-time.Second)); err != nil || queued {
			t.Fatal("恢复后不应提前重试", queued, err)
		}
		if queued, err := f.service.queueDueSync(ctx, c, state, want); err != nil || !queued {
			t.Fatal("到达重试时间应入队", queued, err)
		}
		now = want.Add(time.Minute)
	}
	if err := f.service.syncLibrary(ctx, c); err != nil {
		t.Fatal(err)
	}
	state, err := f.db.EmbyState(ctx, scope)
	if err != nil || state.Status != "failed" || state.SyncRetryCount != 0 {
		t.Fatal("下一周期应重新计算重试次数", state, err)
	}
	if _, err := f.service.queueDueSync(ctx, c, state, now); err != nil {
		t.Fatal(err)
	}
	if err := f.service.StartSync(ctx); err != nil {
		t.Fatal(err)
	}
	state, err = f.db.EmbyState(ctx, scope)
	if err != nil || state.SyncRetryCount != 0 || state.SyncRetryAt != "" || state.Status != "queued" {
		t.Fatal("手动同步应立即排队并重置重试", state, err)
	}
	f.mu.Lock()
	f.failPage = false
	f.mu.Unlock()
	if err := f.service.syncLibrary(ctx, c); err != nil {
		t.Fatal(err)
	}
	state, err = f.db.EmbyState(ctx, scope)
	if err != nil || state.Status != "idle" || state.SyncRetryCount != 0 || state.SyncRetryAt != "" {
		t.Fatal("恢复成功后重试状态未清空", state, err)
	}
}
