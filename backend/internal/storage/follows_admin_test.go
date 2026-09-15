package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestFollowAdminMigrationAtomicNotificationsAndCycles(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	f := model.Follow{Kind: "tv", TMDBID: 100, Title: "测试剧集"}
	if err := s.Subscribe(ctx, "legacy", f); err != nil {
		t.Fatal(err)
	}
	// 还原版本 9 的表结构，升级不能丢失已存在的申请和订阅。
	if _, err := s.db.ExecContext(ctx, `DROP TABLE follow_settings; ALTER TABLE follow_requests DROP COLUMN note; ALTER TABLE follow_requests DROP COLUMN updated_at; ALTER TABLE follow_requests DROP COLUMN revision; ALTER TABLE follow_requests DROP COLUMN cycle; PRAGMA user_version=9;`); err != nil {
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
