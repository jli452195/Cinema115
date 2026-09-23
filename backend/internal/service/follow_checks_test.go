package service

import (
	"context"
	"fmt"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"testing"
	"time"
)

type followCheckMetadata struct {
	detail   *tmdb.Detail
	schedule *tmdb.Schedule
	calls    int
}

func (f *followCheckMetadata) Details(context.Context, string, int) (*tmdb.Detail, error) {
	f.calls++
	return f.detail, nil
}
func (f *followCheckMetadata) Schedule(_ context.Context, _ int, season int) (*tmdb.Schedule, error) {
	if season == 0 {
		return nil, fmt.Errorf("不得请求特别篇")
	}
	return f.schedule, nil
}

func TestFollowWorkerEmbyWebhookAndDuplicateVersionDeletion(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	if err := f.db.Subscribe(ctx, "alice", model.Follow{Kind: "tv", TMDBID: 100, Title: "测试剧集"}); err != nil {
		t.Fatal(err)
	}
	metadata := &followCheckMetadata{detail: &tmdb.Detail{Item: tmdb.Item{ID: 100, Kind: "tv"}, Status: "Ended", Seasons: []tmdb.SeasonSummary{{Number: 0, EpisodeCount: 100}, {Number: 1, EpisodeCount: 2}}}, schedule: &tmdb.Schedule{Number: 1, Episodes: []tmdb.ScheduledEpisode{{Number: 1, Date: "2020-01-01"}, {Number: 2, Date: "2020-01-02"}}}}
	s := &FollowService{Store: f.db, TMDB: metadata, Library: f.service}
	work, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.RunChecks(work) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("追更后台未退出")
		}
	}()
	wait := func(state string) {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			p, err := f.db.FollowProgress(ctx, []model.Follow{{Kind: "tv", TMDBID: 100}})
			if err != nil {
				t.Fatal(err)
			}
			if p["tv:100"] != nil && p["tv:100"].State == state {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("未自动达到 %s", state)
	}
	wait("completed")
	remove := func(id string) {
		f.mu.Lock()
		for i, item := range f.items {
			if item.ID == id {
				f.items = append(f.items[:i], f.items[i+1:]...)
				break
			}
		}
		f.mu.Unlock()
		f.apply(t, f.event(t, id, "delete"))
	}
	remove("ep-2")
	// 另一版本的合并集仍包含第 2 集，实际索引比对仍完成。
	p, err := s.EvaluateFollow(ctx, model.FollowCheck{Kind: "tv", TMDBID: 100}, time.Now())
	if err != nil || p.State != "completed" || p.Owned != 2 {
		t.Fatal(p, err)
	}
	remove("ep-1")
	wait("absent")
	got, err := f.db.Follow(ctx, "alice", "tv", 100)
	if err != nil || got.Status != "pending" {
		t.Fatal(got, err)
	}
	if unread, err := f.db.WebsiteUnread(ctx, "alice"); err != nil || unread != 2 {
		t.Fatal("应只有完成、恢复追踪两条通知", unread, err)
	}
}

type followCheckLibrary struct {
	state string
	items []model.EmbyItem
}

func (f *followCheckLibrary) Matches(_ context.Context, q []model.EmbyMatch, _ bool) ([]model.EmbyMatch, error) {
	q[0].State = f.state
	q[0].Items = f.items
	return q, nil
}

func TestFollowAiredEpisodesAndVersions(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, status, state string
		episodeDate         string
		items               []model.EmbyItem
		stale, empty        bool
		want                string
		owned, aired        int
		wantErr             bool
	}{
		{name: "电影入库", state: "present", want: "completed"},
		{name: "未入库无需请求TMDB", state: "absent", want: "absent"},
		{name: "未知索引不完成", state: "unknown", wantErr: true},
		{name: "多版本合并集号", status: "Returning Series", state: "present", episodeDate: "2026-09-14", items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1}, {Type: "Episode", Season: 1, Episode: 1}, {Type: "Episode", Season: 1, Episode: 2}}, want: "caught_up", owned: 2, aired: 2},
		{name: "合并集文件完结", status: "Ended", state: "present", episodeDate: "2026-09-14", items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1, EndEpisode: 2}}, want: "completed", owned: 2, aired: 2},
		{name: "未来集不算缺集", status: "Returning Series", state: "present", episodeDate: "2026-09-16", items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1}}, want: "caught_up", owned: 1, aired: 1},
		{name: "未来集使完结仍未完成", status: "Ended", state: "present", episodeDate: "2026-09-16", items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1}}, want: "caught_up", owned: 1, aired: 1},
		{name: "播出后恢复缺集", status: "Returning Series", state: "present", episodeDate: "2026-09-15", items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1}}, want: "tracking", owned: 1, aired: 2},
		{name: "只有特别篇不算入库", status: "Ended", state: "present", items: []model.EmbyItem{{Type: "Episode", Season: 0, Episode: 1}}, want: "absent"},
		{name: "日期未知保持追更", status: "Ended", state: "present", items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1}}, want: "tracking", owned: 1, aired: 1},
		{name: "日期格式错误", status: "Ended", state: "present", episodeDate: "2026/09/02", items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1}}, wantErr: true},
		{name: "过期信息不完成", status: "Ended", state: "present", episodeDate: "2026-09-14", stale: true, items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1, EndEpisode: 2}}, wantErr: true},
		{name: "空季数据不完成", status: "Ended", state: "present", episodeDate: "2026-09-14", empty: true, items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1, EndEpisode: 2}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := &followCheckMetadata{detail: &tmdb.Detail{Item: tmdb.Item{ID: 10, Kind: "tv"}, Status: tc.status, Seasons: []tmdb.SeasonSummary{{Number: 0, EpisodeCount: 100}, {Number: 1, EpisodeCount: 2}}}, schedule: &tmdb.Schedule{Number: 1, Episodes: []tmdb.ScheduledEpisode{{Number: 1, Date: "2026-09-01"}, {Number: 2, Date: tc.episodeDate}}}}
			metadata.detail.CacheStale = tc.stale
			if tc.empty {
				metadata.schedule.Episodes = nil
			}
			s := &FollowService{TMDB: metadata, Library: &followCheckLibrary{state: tc.state, items: tc.items}}
			kind := "tv"
			if tc.name == "电影入库" {
				kind = "movie"
			}
			p, err := s.EvaluateFollow(context.Background(), model.FollowCheck{Kind: kind, TMDBID: 10}, now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && (p.State != tc.want || p.Owned != tc.owned || p.Aired != tc.aired) {
				t.Fatalf("progress=%+v", p)
			}
			if (tc.state == "absent" || tc.name == "只有特别篇不算入库" || kind == "movie") && metadata.calls != 0 {
				t.Fatal("不应请求元数据")
			}
		})
	}
}
