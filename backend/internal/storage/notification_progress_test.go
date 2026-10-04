package storage

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestFollowBaselineResubscribeAndRealChanges(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	f := model.Follow{Kind: "tv", TMDBID: 94664, Title: "测试剧集"}
	if err := s.Subscribe(ctx, "alice", f); err != nil {
		t.Fatal(err)
	}
	apply := func(p model.FollowProgress) model.FollowCheck {
		t.Helper()
		if err := s.RecheckFollows(ctx); err != nil {
			t.Fatal(err)
		}
		jobs, err := s.DueFollowChecks(ctx, time.Now().Unix())
		if err != nil || len(jobs) != 1 {
			t.Fatal(jobs, err)
		}
		p.CheckedAt = time.Now().Unix()
		if err := s.SaveFollowCheck(ctx, jobs[0], p, time.Now().Add(time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
		return jobs[0]
	}
	wantUnread := func(n int) {
		t.Helper()
		got, err := s.WebsiteUnread(ctx, "alice")
		if err != nil || got != n {
			t.Fatalf("unread=%d want=%d err=%v", got, n, err)
		}
	}
	caughtUp := model.FollowProgress{State: "caught_up", Aired: 61, Owned: 61}
	apply(model.FollowProgress{Error: "尚未就绪"})
	apply(caughtUp)
	wantUnread(0)
	for i := 0; i < 4; i++ {
		if err := s.CancelFollow(ctx, "alice", f.Kind, f.TMDBID); err != nil {
			t.Fatal(err)
		}
		if err := s.Subscribe(ctx, "alice", f); err != nil {
			t.Fatal(err)
		}
		progress, err := s.FollowProgress(ctx, []model.Follow{f})
		if err != nil || progress["tv:94664"].Owned != 61 {
			t.Fatal("重新订阅丢失进度", progress, err)
		}
		apply(caughtUp)
		wantUnread(0)
	}
	// 新集、丢集、恢复相同集数都是真实事件，不能按文案/集数永久去重。
	apply(model.FollowProgress{State: "caught_up", Aired: 62, Owned: 62})
	wantUnread(1)
	apply(model.FollowProgress{State: "tracking", Aired: 62, Owned: 61})
	wantUnread(2)
	job := apply(model.FollowProgress{State: "caught_up", Aired: 62, Owned: 62})
	wantUnread(3)
	if err := s.SaveFollowCheck(ctx, job, caughtUp, 0); err != nil {
		t.Fatal(err)
	}
	wantUnread(3)
	apply(model.FollowProgress{Error: "临时失败"})
	apply(model.FollowProgress{State: "caught_up", Aired: 62, Owned: 62})
	wantUnread(3)
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = reopened
	apply(model.FollowProgress{State: "caught_up", Aired: 62, Owned: 62})
	wantUnread(3)
	apply(model.FollowProgress{State: "completed", Aired: 62, Owned: 62})
	wantUnread(4)
	if err := s.CancelFollow(ctx, "alice", f.Kind, f.TMDBID); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, "alice", f); err != nil {
		t.Fatal(err)
	}
	got, err := s.Follow(ctx, "alice", f.Kind, f.TMDBID)
	if err != nil || got.Status != "completed" {
		t.Fatal("重新订阅丢失完成状态", got, err)
	}
	apply(model.FollowProgress{State: "completed", Aired: 62, Owned: 62})
	wantUnread(4)
}

func TestReadNotificationIDsOnlySelectedOwnerAndIdempotent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, user := range []string{"alice", "bob", "alice", "alice"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO website_notifications(user_id,kind,tmdb_id,title,message,created_at) VALUES(?,'tv',1,'标题','通知',1)`, user); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReadWebsiteNotificationIDs(ctx, "alice", []int64{1, 1, 2, 3, 999}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WebsiteUnread(ctx, "alice"); err != nil || got != 1 {
		t.Fatal(got, err)
	}
	if got, err := s.WebsiteUnread(ctx, "bob"); err != nil || got != 1 {
		t.Fatal("越权已读", got, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE website_notifications SET read_at=123 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadWebsiteNotificationIDs(ctx, "alice", []int64{1, 3}); err != nil {
		t.Fatal(err)
	}
	var readAt int64
	if err := s.db.QueryRowContext(ctx, `SELECT read_at FROM website_notifications WHERE id=1`).Scan(&readAt); err != nil || readAt != 123 {
		t.Fatal("重复已读改写时间", readAt, err)
	}
}
