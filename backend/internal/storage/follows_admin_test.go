package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestFollowClosedMigrationFromFourteen(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE follow_requests DROP COLUMN search_closed; PRAGMA user_version=14;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var version, closed int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO follow_requests(kind,tmdb_id,title,poster_url,release_date,created_at) VALUES('tv',1,'迁移测试','','','now')`); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT search_closed FROM follow_requests WHERE tmdb_id=1`).Scan(&closed); err != nil || version != schemaVersion || closed != 0 {
		t.Fatalf("migration version=%d closed=%d err=%v", version, closed, err)
	}
}

func TestFollowAdminMigrationAtomicNotificationsAndCycles(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	f := model.Follow{Kind: "tv", TMDBID: 100, Title: "测试剧集"}
	if err := s.Subscribe(ctx, "legacy", f); err != nil {
		t.Fatal(err)
	}
	// 还原版本 9 的表结构，升级不能丢失已存在的申请和订阅。
	if _, err := s.db.ExecContext(ctx, `DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; DROP TABLE follow_settings; ALTER TABLE follow_requests DROP COLUMN note; ALTER TABLE follow_requests DROP COLUMN updated_at; ALTER TABLE follow_requests DROP COLUMN revision; ALTER TABLE follow_requests DROP COLUMN cycle; PRAGMA user_version=9;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := s.AdminFollows(ctx, "tv", "pending", "测试", 0)
	if err != nil || len(list) != 1 || list[0].SubscriberCount != 1 || list[0].UpdatedAt == "" {
		t.Fatal("升级丢失追更", list, err)
	}
	if err := s.UpdateFollow(ctx, "tv", 100, "processing", "查找中", list[0].Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFollow(ctx, "tv", 100, "pending", "旧页面覆盖", list[0].Revision); !errors.Is(err, ErrFollowConflict) {
		t.Fatal("未拒绝旧版本覆盖", err)
	}
	got, err := s.Follow(ctx, "legacy", "tv", 100)
	if err != nil || got.Status != "processing" || got.Note != "查找中" {
		t.Fatal("用户无法读取管理员说明", got, err)
	}
	list, err = s.AdminFollows(ctx, "", "processing", "100", 0)
	if err != nil || len(list) != 1 {
		t.Fatal("筛选失败", err)
	}
	n := &model.TelegramNotification{BotID: "123", ChatID: 42, Text: "追更通知"}
	f.TMDBID = 101
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SubscribeWithNotification(ctx, "alice", f, n); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := s.SubscribeWithNotification(ctx, "bob", f, n); err != nil {
		t.Fatal(err)
	}
	pending, err := s.DueTelegramNotifications(ctx, "123", 42, time.Now())
	if err != nil || len(pending) != 1 || pending[0].TaskID != "follow:tv:101:1" {
		t.Fatal("重复提交或新增用户导致重复通知", pending, err)
	}
	list, err = s.AdminFollows(ctx, "tv", "", "101", 0)
	if err != nil || len(list) != 1 || list[0].SubscriberCount != 2 {
		t.Fatal("没有合并申请人数", err)
	}
	if err := s.CancelFollow(ctx, "alice", "tv", 101); err != nil {
		t.Fatal(err)
	}
	pending, err = s.DueTelegramNotifications(ctx, "123", 42, time.Now())
	if err != nil || len(pending) != 1 {
		t.Fatal("一人取消影响其他人的通知", err)
	}
	if err := s.CancelFollow(ctx, "bob", "tv", 101); err != nil {
		t.Fatal(err)
	}
	pending, err = s.DueTelegramNotifications(ctx, "123", 42, time.Now())
	if err != nil || len(pending) != 0 {
		t.Fatal("最后一人取消后仍有待发通知", err)
	}
	if err := s.UpdateFollow(ctx, "tv", 101, "processing", "不应保存", list[0].Revision); !errors.Is(err, ErrFollowConflict) {
		t.Fatal("允许处理无订阅申请", err)
	}
	if err := s.SubscribeWithNotification(ctx, "bob", f, n); err != nil {
		t.Fatal(err)
	}
	pending, err = s.DueTelegramNotifications(ctx, "123", 42, time.Now())
	if err != nil || len(pending) != 1 || pending[0].TaskID != "follow:tv:101:2" {
		t.Fatal("重新开启追更未创建新轮次通知", pending, err)
	}
	// 通知入队失败时整个申请回滚，不能出现订阅已保存但通知丢失。
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_follow_notification BEFORE INSERT ON telegram_notifications BEGIN SELECT RAISE(ABORT,'test'); END;`); err != nil {
		t.Fatal(err)
	}
	f.TMDBID = 102
	if err := s.SubscribeWithNotification(ctx, "alice", f, n); err == nil {
		t.Fatal("通知写入失败未返回错误")
	}
	if got, err := s.Follow(ctx, "alice", "tv", 102); err != nil || got != nil {
		t.Fatal("事务失败仍保留订阅", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM follow_requests WHERE tmdb_id=102").Scan(&count); err != nil || count != 0 {
		t.Fatal("事务失败仍保留影视申请", err)
	}
}

func TestAdminCloseFollowStopsSearchAndPreservesLibraryTracking(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if err := s.SaveFollowSettings(ctx, model.FollowSettings{AutoEnabled: true}); err != nil {
		t.Fatal(err)
	}
	follow := model.Follow{Kind: "tv", TMDBID: 200, Title: "关闭测试剧集"}
	for _, user := range []string{"alice", "bob"} {
		if err := s.SubscribeWithConfiguredAutomation(ctx, user, follow, nil); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.AdminFollows(ctx, "tv", "pending", "200", 0)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if err := s.UpdateFollow(ctx, "tv", 200, "closed", "暂未找到可用资源", list[0].Revision); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"alice", "bob"} {
		got, err := s.Follow(ctx, user, "tv", 200)
		if err != nil || got == nil || got.Status != "closed" || got.Note != "暂未找到可用资源" {
			t.Fatalf("%s did not see closed follow: %+v %v", user, got, err)
		}
		notifications, err := s.WebsiteNotifications(ctx, user, 0)
		if err != nil || len(notifications) != 1 || notifications[0].Message != "追更已关闭：暂未找到可用资源" {
			t.Fatalf("%s close notification: %+v %v", user, notifications, err)
		}
	}
	var jobStatus string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM follow_bot_jobs WHERE kind='tv' AND tmdb_id=200`).Scan(&jobStatus); err != nil || jobStatus != "cancelled" {
		t.Fatalf("closed search job status=%q err=%v", jobStatus, err)
	}
	if err := s.SubscribeWithConfiguredAutomation(ctx, "charlie", follow, nil); err != nil {
		t.Fatal("closed follow should still accept subscribers", err)
	}
	charlie, err := s.Follow(ctx, "charlie", "tv", 200)
	if err != nil || charlie == nil || charlie.Status != "closed" {
		t.Fatalf("new subscriber did not inherit closed state: %+v %v", charlie, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM follow_bot_jobs WHERE kind='tv' AND tmdb_id=200`).Scan(&jobStatus); err != nil || jobStatus != "cancelled" {
		t.Fatalf("new subscriber restarted closed search: %q %v", jobStatus, err)
	}
	counts, err := s.FollowCounts(ctx, "alice", "", "")
	if err != nil || counts["closed"] != 1 || counts["ongoing"] != 0 {
		t.Fatalf("closed counts=%v err=%v", counts, err)
	}
	closedItems, err := s.Follows(ctx, "alice", "", "closed", 0)
	if err != nil || len(closedItems) != 1 || closedItems[0].Status != "closed" {
		t.Fatalf("closed list=%+v err=%v", closedItems, err)
	}
	ongoingItems, err := s.Follows(ctx, "alice", "", "ongoing", 0)
	if err != nil || len(ongoingItems) != 0 {
		t.Fatalf("closed follow leaked into ongoing: %+v %v", ongoingItems, err)
	}

	checks, err := s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(checks) != 1 {
		t.Fatalf("closed follow should keep library check: %+v %v", checks, err)
	}
	if err := s.SaveFollowCheck(ctx, checks[0], model.FollowProgress{State: "completed", CheckedAt: time.Now().Unix()}, time.Now().Add(6*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Follow(ctx, "alice", "tv", 200)
	if err != nil || got.Status != "completed" {
		t.Fatalf("closed follow did not complete after library match: %+v %v", got, err)
	}
	if err := s.RecheckFollows(ctx); err != nil {
		t.Fatal(err)
	}
	checks, err = s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(checks) != 1 {
		t.Fatal(checks, err)
	}
	if err := s.SaveFollowCheck(ctx, checks[0], model.FollowProgress{State: "absent", CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	got, err = s.Follow(ctx, "alice", "tv", 200)
	if err != nil || got.Status != "closed" {
		t.Fatalf("removed library item should return to closed: %+v %v", got, err)
	}

	list, err = s.AdminFollows(ctx, "tv", "closed", "200", 0)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if err := s.SaveFollowSettings(ctx, model.FollowSettings{AutoEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFollow(ctx, "tv", 200, "pending", "重新查找资源", list[0].Revision); err != nil {
		t.Fatal(err)
	}
	got, err = s.Follow(ctx, "alice", "tv", 200)
	if err != nil || got.Status != "pending" {
		t.Fatalf("follow was not reopened: %+v %v", got, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM follow_bot_jobs WHERE kind='tv' AND tmdb_id=200`).Scan(&jobStatus); err != nil || jobStatus != "checking" {
		t.Fatalf("reopened search job status=%q err=%v", jobStatus, err)
	}
	notifications, err := s.WebsiteNotifications(ctx, "alice", 0)
	if err != nil || len(notifications) != 3 || notifications[0].Message != "追更已恢复处理：重新查找资源" {
		t.Fatalf("reopen notification: %+v %v", notifications, err)
	}
}
