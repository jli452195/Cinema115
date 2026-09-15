package storage

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestFollowBotJobOnlyForFirstSubscriberAndNewCycle(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	follow := model.Follow{Kind: "tv", TMDBID: 100, Title: "测试剧集", Date: "2020-01-01"}
	if err := s.SubscribeWithAutomation(ctx, "alice", follow, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SubscribeWithAutomation(ctx, "bob", follow, nil, true); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM follow_bot_jobs WHERE kind='tv' AND tmdb_id=100").Scan(&count); err != nil || count != 1 {
		t.Fatalf("多人订阅创建了重复任务：%d %v", count, err)
	}
	checks, err := s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(checks) != 1 {
		t.Fatalf("没有先安排入库检查：%+v %v", checks, err)
	}
	if err := s.SaveFollowCheck(ctx, checks[0], model.FollowProgress{State: "absent", CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	job, err := s.NextFollowBotJob(ctx, time.Now().Add(time.Second))
	if err != nil || job == nil || job.Cycle != 1 || job.Attempts != 1 {
		t.Fatalf("首次任务错误：%+v %v", job, err)
	}
	if err := s.CancelFollow(ctx, "alice", "tv", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelFollow(ctx, "bob", "tv", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.SubscribeWithAutomation(ctx, "alice", follow, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM follow_bot_jobs WHERE kind='tv' AND tmdb_id=100").Scan(&count); err != nil || count != 2 {
		t.Fatalf("新订阅周期没有创建新任务：%d %v", count, err)
	}
	checks, err = s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(checks) != 1 {
		t.Fatalf("新周期没有安排入库检查：%+v %v", checks, err)
	}
	if err := s.SaveFollowCheck(ctx, checks[0], model.FollowProgress{State: "tracking", Aired: 2, Owned: 1, CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	job, err = s.NextFollowBotJob(ctx, time.Now().Add(time.Second))
	if err != nil || job == nil || job.Cycle != 2 {
		t.Fatalf("新周期任务错误：%+v %v", job, err)
	}
}

func TestEnablingFollowAutomationQueuesExistingRequests(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if err := s.Subscribe(ctx, "alice", model.Follow{Kind: "movie", TMDBID: 7, Title: "电影", Date: "2024-01-01"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFollowSettings(ctx, model.FollowSettings{AutoEnabled: true, BaseURL: "http://example.com"}); err != nil {
		t.Fatal(err)
	}
	settings, err := s.FollowSettings(ctx)
	if err != nil || !settings.AutoEnabled {
		t.Fatalf("追更设置未保存：%+v %v", settings, err)
	}
	checks, err := s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(checks) != 1 {
		t.Fatalf("启用后没有先检查已有申请：%+v %v", checks, err)
	}
	if err := s.SaveFollowCheck(ctx, checks[0], model.FollowProgress{State: "absent", CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	job, err := s.NextFollowBotJob(ctx, time.Now().Add(time.Second))
	if err != nil || job == nil || job.TMDBID != 7 {
		t.Fatalf("已有申请没有补入队列：%+v %v", job, err)
	}
}

func TestCaughtUpFollowDoesNotQueueBotSearch(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if err := s.SubscribeWithAutomation(ctx, "alice", model.Follow{Kind: "tv", TMDBID: 9, Title: "已追平剧集"}, nil, true); err != nil {
		t.Fatal(err)
	}
	checks, err := s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(checks) != 1 {
		t.Fatal(checks, err)
	}
	if err := s.SaveFollowCheck(ctx, checks[0], model.FollowProgress{State: "caught_up", Aired: 12, Owned: 12, CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	job, err := s.NextFollowBotJob(ctx, time.Now().Add(time.Second))
	if err != nil || job != nil {
		t.Fatalf("已追平仍触发 Bot 搜索：%+v %v", job, err)
	}
}

func TestFollowBotVersionThirteenMigrationAndCheckingCancellation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if err := s.SubscribeWithAutomation(ctx, "alice", model.Follow{Kind: "movie", TMDBID: 12, Title: "测试电影"}, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='queued'; PRAGMA user_version=12;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM follow_bot_jobs WHERE tmdb_id=12`).Scan(&status); err != nil || status != "checking" {
		t.Fatalf("旧任务没有恢复为入库检查：%q %v", status, err)
	}
	if err := s.CancelFollow(ctx, "alice", "movie", 12); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM follow_bot_jobs WHERE tmdb_id=12`).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("最后一人取消后仍保留检查任务：%q %v", status, err)
	}
}
