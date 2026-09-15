package storage

import (
	"context"
	"strm115/backend/internal/model"
	"testing"
	"time"
)

func TestFollowCheckMigrationNotificationsAndRestart(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	f := model.Follow{Kind: "tv", TMDBID: 10, Title: "测试剧集"}
	for _, user := range []string{"alice", "bob"} {
		if err := s.Subscribe(ctx, user, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; ALTER TABLE follow_settings DROP COLUMN account_id; ALTER TABLE follow_settings DROP COLUMN auto_enabled; PRAGMA user_version=10;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	apply := func(p model.FollowProgress) {
		t.Helper()
		if err := s.RecheckFollows(ctx); err != nil {
			t.Fatal(err)
		}
		jobs, err := s.DueFollowChecks(ctx, now)
		if err != nil || len(jobs) != 1 {
			t.Fatal(jobs, err)
		}
		p.CheckedAt = now
		if err := s.SaveFollowCheck(ctx, jobs[0], p, now+3600); err != nil {
			t.Fatal(err)
		}
	}
	apply(model.FollowProgress{State: "caught_up", Aired: 2, Owned: 2})
	apply(model.FollowProgress{State: "caught_up", Aired: 2, Owned: 2})
	// 临时故障恢复不重复通知。
	apply(model.FollowProgress{Error: "等待复核"})
	apply(model.FollowProgress{State: "caught_up", Aired: 2, Owned: 2})
	for _, user := range []string{"alice", "bob"} {
		n, err := s.WebsiteUnread(ctx, user)
		if err != nil || n != 1 {
			t.Fatal(user, n, err)
		}
	}
	rows, err := s.WebsiteNotifications(ctx, "alice", 0)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if err := s.ReadWebsiteNotifications(ctx, "bob", rows[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.WebsiteUnread(ctx, "alice"); n != 1 {
		t.Fatal("跨用户标记了通知")
	}
	if err := s.ReadWebsiteNotifications(ctx, "alice", rows[0].ID, true); err != nil {
		t.Fatal(err)
	}
	apply(model.FollowProgress{State: "tracking", Aired: 3, Owned: 2})
	if n, _ := s.WebsiteUnread(ctx, "alice"); n != 1 {
		t.Fatal("新播出缺集没有通知", n)
	}
	if err := s.CancelFollow(ctx, "bob", "tv", 10); err != nil {
		t.Fatal(err)
	}
	apply(model.FollowProgress{State: "completed", Aired: 3, Owned: 3})
	if n, _ := s.WebsiteUnread(ctx, "bob"); n != 2 {
		t.Fatal("取消后仍通知", n)
	}
	got, err := s.Follow(ctx, "alice", "tv", 10)
	if err != nil || got.Status != "completed" {
		t.Fatal("未自动完成", got, err)
	}
	// 重新打开文件模拟重启，持久化状态继续用于去重。
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecheckFollows(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := reopened.DueFollowChecks(ctx, now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := reopened.SaveFollowCheck(ctx, jobs[0], model.FollowProgress{State: "completed", Aired: 3, Owned: 3, CheckedAt: now}, now+3600); err != nil {
		t.Fatal(err)
	}
	if n, _ := reopened.WebsiteUnread(ctx, "alice"); n != 2 {
		t.Fatal("重启重复通知", n)
	}
	// 新事件在检查期间到达时，旧结果不得覆盖。
	if err := s.RecheckFollows(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFollowCheck(ctx, jobs[0], model.FollowProgress{State: "absent", CheckedAt: now}, now+3600); err != nil {
		t.Fatal(err)
	}
	got, err = s.Follow(ctx, "alice", "tv", 10)
	if err != nil || got.Status != "completed" {
		t.Fatal("旧检查覆盖了新版本", got, err)
	}
	apply(model.FollowProgress{State: "absent"})
	got, err = s.Follow(ctx, "alice", "tv", 10)
	if err != nil || got.Status != "pending" {
		t.Fatal("删除后没有恢复申请", got, err)
	}
}

func TestEmbyFollowInvalidationTargetsOldAndNewSeries(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, id := range []int{10, 20, 30} {
		if err := s.Subscribe(ctx, "alice", model.Follow{Kind: "tv", TMDBID: id, Title: "测试"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EmbyEnsure(ctx, "scope"); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyStage(ctx, "scope", "gen", []model.EmbyItem{{ID: "series", Type: "Series", TMDB: "10"}, {ID: "episode", Type: "Episode", SeriesID: "series", Season: 1, Episode: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, "scope", "gen", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE follow_checks SET due=9999999999`); err != nil {
		t.Fatal(err)
	}
	event := func(id string) model.EmbyEvent {
		t.Helper()
		if err := s.EmbyEnqueue(ctx, "scope", id, "update"); err != nil {
			t.Fatal(err)
		}
		var e model.EmbyEvent
		if err := s.db.QueryRowContext(ctx, `SELECT id,item_id,kind,attempts FROM emby_events WHERE item_id=?`, id).Scan(&e.ID, &e.ItemID, &e.Kind, &e.Attempts); err != nil {
			t.Fatal(err)
		}
		return e
	}
	if err := s.EmbyApplyEvent(ctx, "scope", "gen", event("series"), []model.EmbyItem{{ID: "series", Type: "Series", TMDB: "20"}}, false); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(jobs) != 2 || jobs[0].TMDBID != 10 || jobs[1].TMDBID != 20 {
		t.Fatal("未同时复核旧新TMDB或影响无关剧集", jobs, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE follow_checks SET due=9999999999`); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyApplyEvent(ctx, "scope", "gen", event("episode"), nil, true); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(jobs) != 1 || jobs[0].TMDBID != 20 {
		t.Fatal("删除集未复核所属剧集", jobs, err)
	}
}
