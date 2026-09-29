package storage

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"strm115/backend/internal/model"
)

func TestFollowMigrationDeduplicationIsolationAndPagination(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO website_users(id,username,password_hash,created_at) VALUES('reader','reader','hash','date'); DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; DROP TABLE follow_settings; DROP TABLE user_follows; DROP TABLE follow_requests; PRAGMA user_version=8;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if u, err := s.WebsiteUser(ctx, "reader"); err != nil || u.Username != "reader" {
		t.Fatal("升级丢失原有用户", err)
	}
	f := model.Follow{Kind: "tv", TMDBID: 100, Title: "测试剧集", Date: "2026-01-01"}
	// 同一个用户并发重复提交，以及不同用户提交同一影视，都只创建一份影视申请。
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Subscribe(ctx, "reader", f); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := s.Subscribe(ctx, "other", f); err != nil {
		t.Fatal(err)
	}
	var requests, subscriptions int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM follow_requests").Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_follows").Scan(&subscriptions); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || subscriptions != 2 {
		t.Fatal("重复创建申请", requests, subscriptions)
	}
	before, err := s.Follow(ctx, "reader", "tv", 100)
	if err != nil || before == nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, "reader", f); err != nil {
		t.Fatal(err)
	}
	after, err := s.Follow(ctx, "reader", "tv", 100)
	if err != nil || after == nil || before.SubscribedAt != after.SubscribedAt {
		t.Fatal("重复提交修改了申请时间", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.CancelFollow(ctx, "reader", "tv", 100); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Follow(ctx, "other", "tv", 100); err != nil || got == nil {
		t.Fatal("取消影响了其他用户", err)
	}
	if got, err := s.Follow(ctx, "reader", "tv", 100); err != nil || got != nil {
		t.Fatal("取消未生效", err)
	}
	if err := s.Subscribe(ctx, "reader", f); err != nil {
		t.Fatal(err)
	}
	for id := 101; id <= 125; id++ {
		if err := s.Subscribe(ctx, "reader", model.Follow{Kind: "movie", TMDBID: id, Title: fmt.Sprint(id)}); err != nil {
			t.Fatal(err)
		}
		// Pagination needs distinct chronological fixtures; rapid writes can
		// share a wall-clock timestamp and legitimately use the ID tie-breaker.
		if _, err := s.db.ExecContext(ctx, `UPDATE user_follows SET subscribed_at=? WHERE user_id='reader' AND kind='movie' AND tmdb_id=?`, fmt.Sprintf("2026-01-01T00:00:%02dZ", id-101), id); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Follows(ctx, "reader", "movie", "ongoing", 0)
	if err != nil || len(first) != 25 {
		t.Fatal("分页预读错误", len(first), err)
	}
	last, err := s.Follows(ctx, "reader", "movie", "ongoing", 24)
	if err != nil || len(last) != 1 || last[0].TMDBID != 101 {
		t.Fatal("分页排序错误", last, err)
	}
	other, err := s.Follows(ctx, "other", "", "ongoing", 0)
	if err != nil || len(other) != 1 || other[0].Kind != "tv" {
		t.Fatal("列表越权或类型过滤错误", other, err)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}
