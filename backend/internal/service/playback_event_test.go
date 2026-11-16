package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestPoolCapturedStopSurvivesMissingUserSlotAndRejectsReplay(t *testing.T) {
	s, _, id := memberFixture(t)
	r := s.record(id)
	p := s.Pool
	now := time.Now()
	eventKey := DirectLinkKey(r.ServerID, r.UserID, "session", "movie")
	p.doc.Leases["lease"] = model.PlaybackPoolLease{Key: "lease", UserID: r.UserID, SessionKey: eventKey, Generation: 1, UID: "7", Issued: true, ExpiresAt: now.Add(time.Hour).UnixMilli()}
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "session", "client", "movie", false)
	if !e.Matched() || e.userGeneration != 0 {
		t.Fatal("orphan pool lease not captured")
	}
	if err := s.ApplyPlaybackEvent(context.Background(), e, true); err != nil {
		t.Fatal(err)
	}
	if !p.doc.Leases["lease"].Stopped {
		t.Fatal("missing user slot blocked pool stop")
	}
	l := p.doc.Leases["lease"]
	l.Generation = 2
	l.Stopped = false
	p.doc.Leases["lease"] = l
	if err := s.ApplyPlaybackEvent(context.Background(), e, true); err != nil {
		t.Fatal(err)
	}
	if p.doc.Leases["lease"].Stopped {
		t.Fatal("late stop changed replay generation")
	}
}

func TestPoolMissingSessionFallbackDoesNotMatchOtherToken(t *testing.T) {
	s, _, id := memberFixture(t)
	r := s.record(id)
	ctx := context.Background()
	req := PoolRequest{Client: "client", Session: "session", Item: "movie", Source: "source"}
	l, err := s.UserPlayback.Acquire(ctx, r, req)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if s.CapturePlaybackEvent("http://emby", r.EmbyID, "", "other-client", "movie", true).Matched() {
		t.Fatal("cross-token fallback")
	}
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "", "client", "movie", true)
	if !e.Matched() {
		t.Fatal("missing session fallback not captured")
	}
	if err := s.ApplyPlaybackEvent(ctx, e, true); err != nil {
		t.Fatal(err)
	}
	if len(s.UserPlayback.List()) != 0 {
		t.Fatal("user slot not released")
	}
}

func TestPlaybackMultipleSessionsStopIsolation(t *testing.T) {
	s, _, id := memberFixture(t)
	r := s.record(id)
	ctx := context.Background()
	leases := make([]*UserPlaybackLease, 0, 2)
	for _, session := range []string{"first", "second"} {
		l, err := s.UserPlayback.Acquire(ctx, r, PoolRequest{Client: "client", Session: session, Item: "movie"})
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		if err := l.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		leases = append(leases, l)
		s.Pool.doc.Leases[session] = model.PlaybackPoolLease{Key: session, UserID: r.UserID, SessionKey: l.Record.EventKey, Generation: l.Record.Generation, Issued: true, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
	}
	if e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "", "client", "movie", true); e.Matched() {
		t.Fatal("ambiguous fallback chose a session")
	}
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "first", "client", "movie", false)
	if err := s.ApplyPlaybackEvent(ctx, e, true); err != nil {
		t.Fatal(err)
	}
	if leases[0].Current() || !leases[1].Current() || !s.Pool.doc.Leases["first"].Stopped || s.Pool.doc.Leases["second"].Stopped {
		t.Fatal("stopping first session changed another session")
	}
	if err := s.ReleaseUserPlayback(ctx, leases[1].Record.Key, leases[1].Record.Generation); err != nil {
		t.Fatal(err)
	}
	if leases[1].Current() || !s.Pool.doc.Leases["second"].Stopped {
		t.Fatal("admin release failed")
	}
}

func TestPlaybackEventPersistsOnlyChangedDocuments(t *testing.T) {
	for _, tc := range []struct {
		name       string
		advance    time.Duration
		staleUser  bool
		stalePool  bool
		failPool   bool
		userWrites int
		poolWrites int
	}{
		{name: "frequent_heartbeats", advance: time.Second, userWrites: 2},
		{name: "renew_both", advance: time.Minute, userWrites: 1, poolWrites: 1},
		{name: "pool_only", advance: time.Minute, staleUser: true, poolWrites: 1},
		{name: "stale_generations", advance: time.Minute, staleUser: true, stalePool: true},
		{name: "failed_renewal", advance: time.Minute, failPool: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, userLease, poolLease, now := handoffFixture(t)
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "playback-event.db")
			db, err := storage.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			beforeUser, beforePool := s.UserPlayback.clone(), s.Pool.clone()
			if err := db.SavePlaybackState(ctx, beforeUser, beforePool); err != nil {
				t.Fatal(err)
			}
			s.db, s.UserPlayback.store, s.Pool.store = db, db, db
			connection, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = connection.Close() })
			// 临时库内记录实际 UPDATE 次数，避免仅比较最终 JSON 而漏掉重复写入。
			for _, query := range []string{
				`CREATE TABLE event_writes(document TEXT NOT NULL)`,
				`CREATE TRIGGER count_user AFTER UPDATE ON user_playback_leases BEGIN INSERT INTO event_writes VALUES('user'); END`,
				`CREATE TRIGGER count_pool AFTER UPDATE ON playback_pool BEGIN INSERT INTO event_writes VALUES('pool'); END`,
			} {
				if _, err := connection.ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			if tc.failPool {
				if _, err := connection.ExecContext(ctx, `CREATE TRIGGER fail_pool BEFORE UPDATE ON playback_pool BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "old", "client", "movie", false)
			if !e.Matched() || e.poolGenerations[poolLease.Record.Key] == 0 {
				t.Fatal("fixture event did not match user and pool")
			}
			if tc.staleUser {
				e.userGeneration++
			}
			if tc.stalePool {
				e.poolGenerations[poolLease.Record.Key]++
			}
			pulses := max(tc.userWrites, 1)
			for i := 0; i < pulses; i++ {
				*now = now.Add(tc.advance)
				err := s.ApplyPlaybackEvent(ctx, e, false)
				if (err != nil) != tc.failPool {
					t.Fatalf("playback event error = %v, want failure %v", err, tc.failPool)
				}
				if !tc.failPool && !tc.staleUser {
					u, err := db.UserPlayback(ctx)
					if err != nil || u.Leases[userLease.Record.Key].LastPlaybackAt != now.UnixMilli() {
						t.Fatal("heartbeat evidence was not persisted on every pulse", err)
					}
				}
				if tc.userWrites > 1 {
					// 相同毫秒、相同状态的重复事件没有新证据，无需再次保存。
					if err := s.ApplyPlaybackEvent(ctx, e, false); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, want := range []struct {
				document string
				writes   int
			}{{"user", tc.userWrites}, {"pool", tc.poolWrites}} {
				var got int
				if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_writes WHERE document=?`, want.document).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want.writes {
					t.Fatalf("%s writes = %d, want %d", want.document, got, want.writes)
				}
			}
			persistedUser, err := db.UserPlayback(ctx)
			if err != nil {
				t.Fatal(err)
			}
			persistedPool, err := db.PlaybackPool(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(persistedUser, s.UserPlayback.doc) || !reflect.DeepEqual(persistedPool, s.Pool.doc) {
				t.Fatal("persisted documents differ from published memory")
			}
			if tc.userWrites == 0 && !reflect.DeepEqual(beforeUser, s.UserPlayback.doc) {
				t.Fatal("unchanged or failed user document was published")
			}
			if tc.poolWrites == 0 && !reflect.DeepEqual(beforePool, s.Pool.doc) {
				t.Fatal("unchanged or failed pool document was published")
			}
		})
	}
}
