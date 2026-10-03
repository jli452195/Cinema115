package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

// These scenarios use temporary SQLite and simulated time, never live accounts.
func TestPlaybackPhaseOnePrefetchAndConfirmedPromotion(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "spare-capacity", true: "full-pool"}[full], func(t *testing.T) {
			s, r, old, oldPool, _ := handoffFixture(t)
			ctx := context.Background()
			settings := s.Pool.Settings()
			if !full {
				settings.MaxSessions = 2
			}
			if err := s.Pool.Configure(ctx, settings); err != nil {
				t.Fatal(err)
			}
			late := s.CapturePlaybackEvent("http://emby", r.EmbyID, "old", "client", "movie", false)
			next, err := analysisPlayback(t, s, r, "prefetch")
			if err != nil || !old.Current() || !oldPool.Current() {
				t.Fatal("prefetch retired predecessor", err)
			}
			multiUIDCounts(t, s.Pool, []P115Account{oldPool.Account}, 1, 0, 0)
			// Restart between URL issuance and Playing: confirmation needs no
			// request-local predecessor pointer or borrowed lease.
			u, err := NewUserPlayback(ctx, s.db)
			if err != nil {
				t.Fatal(err)
			}
			p, err := NewDynamicPool(ctx, s.db)
			if err != nil {
				t.Fatal(err)
			}
			u.now, p.now = s.UserPlayback.now, s.Pool.now
			s.UserPlayback, s.Pool = u, p
			e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "prefetch", "client", "movie-prefetch", false)
			if err := s.ApplyPlaybackEvent(ctx, e, false); err != nil {
				t.Fatal(err)
			}
			if len(u.List()) != 1 || u.List()[0].Generation != next.Record.Generation || u.doc.Stopped[old.Record.EventKey] == 0 {
				t.Fatal("confirmed successor failed to replace unique idle predecessor")
			}
			multiUIDCounts(t, p, []P115Account{oldPool.Account}, 1, 0, 0)
			for _, stopped := range []bool{false, true} {
				if err := s.ApplyPlaybackEvent(ctx, late, stopped); err != nil {
					t.Fatal(err)
				}
			}
			if len(u.List()) != 1 || p.doc.Leases["pool-prefetch"].Stopped {
				t.Fatal("old events changed successor")
			}
			t.Log("URL alone: two children share one slot; after restart + Playing: confirmed handoff; old events ignored")
		})
	}
}

func TestPlaybackPhaseOneAtomicStopFailure(t *testing.T) {
	s, r, old, oldPool, _ := handoffFixture(t)
	ctx := context.Background()
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "old", "client", "movie", false)
	path := filepath.Join(t.TempDir(), "atomic.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SavePlaybackState(ctx, s.UserPlayback.doc, s.Pool.doc); err != nil {
		t.Fatal(err)
	}
	s.db = db
	connection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `CREATE TRIGGER fail_pool BEFORE UPDATE ON playback_pool BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPlaybackEvent(ctx, e, true); err == nil {
		t.Fatal("injected pool failure accepted")
	}
	persisted, err := db.UserPlayback(ctx)
	if err != nil || len(persisted.Leases) != 1 || !old.Current() || s.Pool.doc.Leases[oldPool.Record.Key].Stopped {
		t.Fatal("failed stop partially published", err)
	}
	if _, err := connection.ExecContext(ctx, "DROP TRIGGER fail_pool"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPlaybackEvent(ctx, e, true); err != nil {
		t.Fatal(err)
	}
	if old.Current() || !s.Pool.doc.Leases[oldPool.Record.Key].Stopped {
		t.Fatal("retry failed to commit stop atomically")
	}
	t.Log("injected SQLite failure rolls back user deletion and pool stop together; retry succeeds")
}

type phaseOneRemote struct {
	memberEmbyClient
	sessions []emby.PlaybackSession
}

func (f *phaseOneRemote) PlaybackSessions(context.Context) ([]emby.PlaybackSession, error) {
	return f.sessions, nil
}

func TestPlaybackPhaseOneBrowsingCannotRenewAndEvidenceIsAnchored(t *testing.T) {
	s, r, old, oldPool, now := handoffFixture(t)
	ctx := context.Background()
	// Add the exact observation identity, as production poolAuthorization does.
	pd := s.Pool.clone()
	l := pd.Leases[oldPool.Record.Key]
	l.ObservationKey = old.Record.ObservationKey
	pd.Leases[l.Key] = l
	if err := s.Pool.save(ctx, pd); err != nil {
		t.Fatal(err)
	}
	remote := &phaseOneRemote{memberEmbyClient: s.NewEmby("", "")}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	initialPool := l.ExpiresAt
	initialUser := s.UserPlayback.doc.Leases[old.Record.Key].ExpiresAt
	*now = now.Add(60 * time.Second)
	listed := emby.PlaybackSession{UserID: r.EmbyID, PlaySessionID: "old", LastActivityDate: *now}
	listed.NowPlayingItem.ID = "movie"
	for _, at := range []time.Time{{}, now.Add(-91 * time.Second), now.Add(time.Second)} {
		listed.LastPlaybackCheckIn = at
		remote.sessions = []emby.PlaybackSession{listed}
		s.Pool.lastReconcile = time.Time{}
		s.reconcilePoolSessions(ctx)
		if s.Pool.doc.Leases[l.Key].ExpiresAt != initialPool || s.UserPlayback.doc.Leases[old.Record.Key].ExpiresAt != initialUser {
			t.Fatal("browsing/missing/stale/future playback evidence renewed lease")
		}
	}
	listed.LastPlaybackCheckIn = *now
	remote.sessions = []emby.PlaybackSession{listed}
	s.Pool.lastReconcile = time.Time{}
	s.reconcilePoolSessions(ctx)
	want := now.Add(5 * time.Minute).UnixMilli()
	if s.Pool.doc.Leases[l.Key].ExpiresAt != want || s.UserPlayback.doc.Leases[old.Record.Key].ExpiresAt != want {
		t.Fatal("fresh playback did not renew both lifecycles")
	}
	*now = now.Add(time.Minute)
	listed.LastActivityDate = *now // Same check-in, fresh browsing only.
	remote.sessions = []emby.PlaybackSession{listed}
	s.reconcilePoolSessions(ctx)
	if s.Pool.doc.Leases[l.Key].ExpiresAt != want || s.UserPlayback.doc.Leases[old.Record.Key].ExpiresAt != want {
		t.Fatal("repeated snapshot extended lease from poll time")
	}
	*now = now.Add(5 * time.Minute)
	s.reconcilePoolSessions(ctx)
	if len(s.UserPlayback.List()) != 0 || s.Pool.AccountStatus(oldPool.Account).Sessions != 0 {
		t.Fatal("lost playback did not expire")
	}
	t.Log("browsing and missing/stale/future evidence never renew; identical snapshots do not extend expiry; lost playback expires in 5 minutes")
}

func TestPlaybackPhaseOnePreparationsDoNotBlockUniquePredecessor(t *testing.T) {
	s, r, _, _, now := handoffFixture(t)
	*now = now.Add(-35 * time.Second)
	pending, err := s.UserPlayback.Acquire(context.Background(), r, handoffRequest("pending"))
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	*now = now.Add(35 * time.Second)
	next, err := s.UserPlayback.Acquire(context.Background(), r, handoffRequest("new"))
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.handoff == nil {
		t.Fatal("unissued preparation made unique predecessor ambiguous")
	}
}

func TestPlaybackPhaseOneConfirmationCannotHealFailedTarget(t *testing.T) {
	s, r, old, oldPool, _ := handoffFixture(t)
	ctx := context.Background()
	settings := s.Pool.Settings()
	settings.MaxSessions = 2
	if err := s.Pool.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := analysisPlayback(t, s, r, "next"); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.report(ctx, oldPool.Account, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "next", "client", "movie-next", false)
	if err := s.ApplyPlaybackEvent(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	if !old.Current() || s.Pool.AccountStatus(oldPool.Account).State != "invalid_credentials" {
		t.Fatal("playback confirmation cleared a newer target failure")
	}
}
