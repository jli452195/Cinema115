package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestAnimeRSSLogsDescribeCommittedPollingWithoutExtraRequests(t *testing.T) {
	f, db, runner, source, now := discoveryServiceFixture(t)
	f.service.Logs = &GenerationLogs{}
	reads := 0
	var readErr error
	runner.Feeds = animeRSSFunc(func(_ context.Context, b *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		reads++
		if _, err := b.startRequest(); err != nil {
			t.Fatal(err)
		}
		return discoveryServicePage(in, now, 1), readErr
	})
	check := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now, source.ID); err != nil {
			t.Fatal(err)
		}
		if err := runner.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	check()
	info := f.service.Logs.Snapshot("INFO", "订阅")
	if len(info) != 1 || info[0].TaskID != source.SubscriptionID || !strings.Contains(info[0].Source, "蜜柑") || !strings.Contains(info[0].Message, "选中 1 条待下载") || !strings.Contains(info[0].Message, "《") {
		t.Fatal("missing RSS identity or actual result", info)
	}
	check()
	if len(f.service.Logs.Snapshot("INFO")) != 1 || len(f.service.Logs.Snapshot("DEBUG")) != 2 {
		t.Fatal("unchanged RSS flooded INFO or paired stage rows", f.service.Logs.Snapshot("ALL"))
	}
	readErr = errors.New("https://private.invalid/feed?token=private-token Cookie: private-cookie")
	check()
	if len(f.service.Logs.Snapshot("WARNING")) != 1 {
		t.Fatal("first read failure missing")
	}
	check()
	if len(f.service.Logs.Snapshot("WARNING")) != 1 {
		t.Fatal("repeated unchanged failure flooded warnings")
	}
	readErr = nil
	check()
	info = f.service.Logs.Snapshot("INFO")
	if len(info) != 2 || !strings.Contains(info[0].Message, "读取已恢复") || reads != 5 {
		t.Fatal("missing recovery or extra feed requests", info, reads)
	}
	if err := runner.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	info = f.service.Logs.Snapshot("INFO")
	if len(info) != 3 || !strings.Contains(info[0].Message, "已加入下载队列") || reads != 5 {
		t.Fatal("queue log missing or duplicated", info, reads)
	}
	for _, entry := range f.service.Logs.Snapshot("ALL") {
		if strings.Contains(entry.Message, "private") || strings.Contains(entry.Message, "115 请求") || strings.Contains(entry.Message, "阶段开始") {
			t.Fatal("RSS log exposed upstream text or misleading I/O metrics", entry)
		}
	}
}

func TestAnimeSubscriptionLogsRetainTitlesAndSkipReplayedActions(t *testing.T) {
	s, feed, in := newAnimeDefinitionService(t)
	s.Logs = &GenerationLogs{}
	ctx := context.Background()
	before := feed.reads
	created, err := s.SaveDefinition(ctx, "admin", "logs-create", "", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDefinition(ctx, "admin", "logs-create", "", in); err != nil {
		t.Fatal(err)
	}
	if info := s.Logs.Snapshot("INFO"); len(info) != 1 || !strings.Contains(info[0].Message, "已开始订阅") || info[0].TaskID != created.ID {
		t.Fatal("save log missing or replay duplicated it", info)
	}
	for _, action := range []string{"pause", "resume", "archive", "unarchive", "remove"} {
		sub, err := s.Store.GetAnimeSubscription(ctx, created.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		input := AnimeControlInput{ExpectedRevision: sub.Revision}
		if _, err := s.ControlSubscription(ctx, "admin", "logs-"+action, created.ID, action, input); err != nil {
			t.Fatal(action, err)
		}
		count := len(s.Logs.Snapshot("ALL"))
		if _, err := s.ControlSubscription(ctx, "admin", "logs-"+action, created.ID, action, input); err != nil || len(s.Logs.Snapshot("ALL")) != count {
			t.Fatal("action replay changed logs", action, err)
		}
	}
	info := s.Logs.Snapshot("INFO")
	if len(info) != 6 || !strings.Contains(info[0].Message, "移除订阅已完成") || !strings.Contains(info[0].Message, "《test anime》") || feed.reads != before {
		t.Fatal("removal lost title or operations read upstream", info, feed.reads, before)
	}
}

func TestAnimeLogsScrubDynamicMetadataAndCompressTiming(t *testing.T) {
	s := &AnimeAdminService{Logs: &GenerationLogs{}}
	poll := storage.AnimeSourcePoll{Source: model.AnimeSource{Provider: "anibt", GroupName: bindingPtr("字幕组\nCookie: private-cookie")}, Subscription: model.AnimeSubscription{ID: strings.Repeat("a", 32)}, Entry: model.AnimeEntry{Title: "动画 https://private.invalid/path?token=private-token"}}
	s.logAnimeRSSResult(poll, AnimeSourceResult{Cached: true}, storage.AnimeDiscoveryStats{}, 0, time.Second)
	s.logAnimeRSSFailure(poll, "SOURCE_BINDING_CHANGED", 0, true)
	_, finish := s.observeAnimeStage(context.Background(), "local-job", "organize")
	finish()
	finish()
	entries := s.Logs.Snapshot("ALL")
	if len(entries) != 3 || !strings.Contains(entries[0].Message, "整理文件执行结束") || strings.Contains(entries[0].Message, "115 请求") || !strings.Contains(entries[1].Message, "已停止自动轮询") || !strings.Contains(entries[2].Message, "命中缓存") {
		t.Fatal("missing action/retry context or repeated timing summary", entries)
	}
	for _, entry := range entries {
		text := entry.Source + entry.Message
		if strings.Contains(text, "private") || strings.Contains(text, "https://") || strings.ContainsAny(text, "\r\n") {
			t.Fatal("untrusted display metadata leaked", entry)
		}
	}
}
