package storage

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestFollowEffectiveStatusClassification(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	cases := []struct {
		name, kind, status, state, checkError, want string
		aired, owned                                int
		closed, noCheck                             bool
	}{
		{name: "空日期保持tracking但已播追平", kind: "tv", status: "processing", state: "tracking", aired: 28, owned: 28, want: "caught_up"},
		{name: "待处理也自动归入追平", kind: "tv", status: "pending", state: "caught_up", aired: 28, owned: 28, want: "caught_up"},
		{name: "缺集处理中", kind: "tv", status: "processing", state: "tracking", aired: 29, owned: 28, want: "processing"},
		{name: "缺集待处理", kind: "tv", status: "pending", state: "tracking", aired: 29, owned: 28, want: "pending"},
		{name: "异常不能用旧汇总追平", kind: "tv", status: "processing", state: "caught_up", aired: 28, owned: 28, checkError: "等待复核", want: "processing"},
		{name: "零集不追平", kind: "tv", status: "pending", state: "tracking", want: "pending"},
		{name: "等待播出不追平", kind: "tv", status: "pending", state: "waiting", want: "pending"},
		{name: "无检查记录", kind: "tv", status: "processing", noCheck: true, want: "processing"},
		{name: "关闭优先于追平", kind: "tv", status: "processing", closed: true, state: "tracking", aired: 28, owned: 28, want: "closed"},
		{name: "完成优先于关闭", kind: "tv", status: "completed", closed: true, state: "completed", aired: 28, owned: 28, want: "completed"},
		{name: "电影不追平", kind: "movie", status: "pending", state: "tracking", aired: 1, owned: 1, want: "pending"},
		{name: "未知进度不追平", kind: "tv", status: "pending", state: "unknown", aired: 28, owned: 28, want: "pending"},
	}
	wantCounts := map[string]int{"pending": 0, "processing": 0, "caught_up": 0, "closed": 0, "completed": 0}
	for i, tc := range cases {
		id := i + 1
		f := model.Follow{Kind: tc.kind, TMDBID: id, Title: tc.name}
		for _, user := range []string{"alice", "bob"} {
			if err := s.Subscribe(ctx, user, f); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE follow_requests SET status=?,search_closed=? WHERE kind=? AND tmdb_id=?`, tc.status, tc.closed, tc.kind, id); err != nil {
			t.Fatal(err)
		}
		if tc.noCheck {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM follow_checks WHERE kind=? AND tmdb_id=?`, tc.kind, id); err != nil {
				t.Fatal(err)
			}
		} else if _, err := s.db.ExecContext(ctx, `UPDATE follow_checks SET state=?,aired=?,owned=?,error=? WHERE kind=? AND tmdb_id=?`, tc.state, tc.aired, tc.owned, tc.checkError, tc.kind, id); err != nil {
			t.Fatal(err)
		}
		got, err := s.Follow(ctx, "alice", tc.kind, id)
		if err != nil || got == nil || got.EffectiveStatus != tc.want {
			t.Fatalf("%s: follow=%+v err=%v", tc.name, got, err)
		}
		wantCounts[tc.want]++
	}
	counts, err := s.FollowCounts(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for status, want := range wantCounts {
		items, err := s.AdminFollows(ctx, "", status, "", 0)
		if err != nil || len(items) != want || counts[status] != want {
			t.Fatalf("%s: items=%d count=%d want=%d err=%v", status, len(items), counts[status], want, err)
		}
		for _, item := range items {
			if item.EffectiveStatus != status || item.SubscriberCount != 2 || item.Status == "caught_up" {
				t.Fatalf("classification/raw status/subscribers: %+v", item)
			}
		}
	}
	personal, err := s.FollowCounts(ctx, "alice", "tv", "追平")
	if err != nil || personal["caught_up"] != 2 {
		t.Fatal(personal, err)
	}
	ongoing, err := s.Follows(ctx, "alice", "", "ongoing", 0)
	if err != nil || len(ongoing) != counts["ongoing"] || counts["ongoing"] != len(cases)-2 {
		t.Fatal("个人追更中必须包含已追平", ongoing, counts, err)
	}
}

func TestFollowCaughtUpFilterBeforePagination(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for id := 1; id <= 55; id++ {
		if err := s.Subscribe(ctx, "alice", model.Follow{Kind: "tv", TMDBID: id, Title: "分页测试"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE follow_checks SET state='tracking',aired=28,owned=28 WHERE tmdb_id<=26`); err != nil {
		t.Fatal(err)
	}
	first, err := s.AdminFollows(ctx, "tv", "caught_up", "分页", 0)
	if err != nil || len(first) != model.FollowPageSize+1 {
		t.Fatal(len(first), err)
	}
	last, err := s.AdminFollows(ctx, "tv", "caught_up", "分页", model.FollowPageSize)
	if err != nil || len(last) != 2 {
		t.Fatal(len(last), err)
	}
	seen := make(map[int]bool, 26)
	for _, item := range append(first[:model.FollowPageSize], last...) {
		if item.TMDBID > 26 || seen[item.TMDBID] || item.EffectiveStatus != "caught_up" {
			t.Fatalf("筛选/分页重复或混入其他状态: %+v", item)
		}
		seen[item.TMDBID] = true
	}
	counts, err := s.FollowCounts(ctx, "", "tv", "分页")
	if err != nil || counts["caught_up"] != 26 || counts["pending"] != 29 {
		t.Fatal(counts, err)
	}
}

func TestFollowCaughtUpToMissingTransition(t *testing.T) {
	for _, state := range []string{"tracking", "caught_up"} {
		for _, closed := range []bool{false, true} {
			s, _ := testStore(t)
			ctx := context.Background()
			if err := s.Subscribe(ctx, "alice", model.Follow{Kind: "tv", TMDBID: 282326, Title: "兰香如故"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE follow_requests SET status='processing',search_closed=?`, closed); err != nil {
				t.Fatal(err)
			}
			apply := func(p model.FollowProgress) {
				t.Helper()
				var version int64
				if err := s.db.QueryRowContext(ctx, `SELECT version FROM follow_checks WHERE kind='tv' AND tmdb_id=282326`).Scan(&version); err != nil {
					t.Fatal(err)
				}
				p.CheckedAt = time.Now().Unix()
				if err := s.SaveFollowCheck(ctx, model.FollowCheck{Kind: "tv", TMDBID: 282326, Version: version}, p, p.CheckedAt+3600); err != nil {
					t.Fatal(err)
				}
			}
			assertStatus := func(raw, effective string) model.AdminFollow {
				t.Helper()
				items, err := s.AdminFollows(ctx, "tv", effective, "", 0)
				if err != nil || len(items) != 1 || items[0].Status != raw || items[0].EffectiveStatus != effective {
					t.Fatalf("state=%s closed=%v raw=%s effective=%s items=%+v err=%v", state, closed, raw, effective, items, err)
				}
				return items[0]
			}
			apply(model.FollowProgress{State: state, Aired: 28, Owned: 28})
			if !closed {
				assertStatus("processing", "caught_up")
			}
			apply(model.FollowProgress{Error: "上游临时异常"})
			if !closed {
				assertStatus("processing", "processing")
			}
			apply(model.FollowProgress{State: "tracking", Aired: 29, Owned: 28})
			if closed {
				assertStatus("closed", "closed")
				continue
			}
			item := assertStatus("pending", "pending")
			if err := s.UpdateFollow(ctx, "tv", 282326, "processing", "开始补集", item.Revision); err != nil {
				t.Fatal(err)
			}
			apply(model.FollowProgress{State: "tracking", Aired: 29, Owned: 28})
			assertStatus("processing", "processing")
			apply(model.FollowProgress{State: state, Aired: 29, Owned: 29})
			assertStatus("processing", "caught_up")
			apply(model.FollowProgress{State: "absent"})
			assertStatus("pending", "pending")
			apply(model.FollowProgress{State: "completed", Aired: 30, Owned: 30})
			assertStatus("completed", "completed")
		}
	}
}
