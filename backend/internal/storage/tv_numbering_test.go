package storage

import (
	"context"
	"errors"
	"strm115/backend/internal/model"
	"testing"
	"time"
)

func TestNumberingSaveAtomicFollowBaselineAndConflict(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	if err := s.Subscribe(ctx, "alice", model.Follow{Kind: "tv", TMDBID: 80462, Title: "千绪的上学路"}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.DueFollowChecks(ctx, time.Now().Unix())
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	if err := s.SaveFollowCheck(ctx, jobs[0], model.FollowProgress{State: "tracking", Aired: 29, Owned: 12}, 0); err != nil {
		t.Fatal(err)
	}
	oldJobs, _ := s.DueFollowChecks(ctx, time.Now().Unix())
	r := TVNumberingRecord{GroupID: "group12", Name: "Season", Type: 6, SavedAt: 1, Snapshot: []byte("durable snapshot")}
	if err := s.SaveTVNumbering(ctx, 80462, r, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTVNumbering(ctx, 80462, r, 0); !errors.Is(err, ErrNumberingConflict) {
		t.Fatal(err)
	}
	if err := s.SaveFollowCheck(ctx, oldJobs[0], model.FollowProgress{State: "tracking", Aired: 29, Owned: 12}, 0); err != nil {
		t.Fatal(err)
	}
	jobs, _ = s.DueFollowChecks(ctx, time.Now().Unix())
	if err := s.SaveFollowCheck(ctx, jobs[0], model.FollowProgress{State: "completed", Aired: 12, Owned: 12}, 0); err != nil {
		t.Fatal(err)
	}
	if n, err := s.WebsiteUnread(ctx, "alice"); err != nil || n != 0 {
		t.Fatal("choice change generated notification", n, err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	record, err := reopened.TVNumbering(ctx, 80462)
	if err != nil || record.Revision != 1 || string(record.Snapshot) != "durable snapshot" {
		t.Fatal(record, err)
	}
	if err = s.SaveTVNumbering(ctx, 80462, TVNumberingRecord{Name: "TMDB 默认", Snapshot: []byte{}}, 1); err != nil {
		t.Fatal(err)
	}
	record, _ = s.TVNumbering(ctx, 80462)
	if record.Revision != 2 || record.GroupID != "" {
		t.Fatal(record)
	}
}

func TestNumberingChangeRechecksSearchesButKeepsCommittedGeneration(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for index, status := range []string{"queued", "running", "no_need", "generating"} {
		t.Run(status, func(t *testing.T) {
			id := 100 + index
			if err := s.SubscribeWithAutomation(ctx, "alice", model.Follow{Kind: "tv", TMDBID: id, Title: "测试剧集"}, nil, true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, "UPDATE follow_bot_jobs SET status=? WHERE tmdb_id=?", status, id); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveTVNumbering(ctx, id, TVNumberingRecord{Name: "TMDB 默认", Snapshot: []byte{}}, 0); err != nil {
				t.Fatal(err)
			}
			var actual string
			if err := s.db.QueryRowContext(ctx, "SELECT status FROM follow_bot_jobs WHERE tmdb_id=?", id).Scan(&actual); err != nil {
				t.Fatal(err)
			}
			if status == "generating" {
				if actual != status {
					t.Fatal("committed generation changed", actual)
				}
			} else if actual != "checking" {
				t.Fatal("search was not invalidated", actual)
			}
		})
	}
}
