package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func handoffFixture(t *testing.T) (*MemberPlaybackService, model.MemberPlayback, *UserPlaybackLease, *PoolLease, *time.Time) {
	t.Helper()
	s, _, id := memberFixture(t)
	r := s.record(id)
	r.Source = "assigned"
	now := time.Now()
	s.UserPlayback.now = func() time.Time { return now }
	s.Pool.now = func() time.Time { return now }
	settings := s.Pool.Settings()
	settings.MaxSessions = 1
	if err := s.Pool.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserPlayback.Acquire(context.Background(), r, handoffRequest("old"))
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := WithPoolRequest(context.Background(), PoolRequest{Device: "trusted-device", lifecycleKey: u.Record.EventKey, userGeneration: u.Record.Generation})
	p, err := s.Pool.Acquire(ctx, "pool-old", r.UserID, []P115Account{poolTestAccount("pool", "7")}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(40 * time.Second)
	return s, r, u, p, &now
}

func handoffRequest(session string) PoolRequest {
	return PoolRequest{Device: "trusted-device", Client: "client", Session: session, PlaySession: session, Item: "movie", Source: "source"}
}

func prepareHandoff(t *testing.T, s *MemberPlaybackService, r model.MemberPlayback) *MediaAuthorization {
	t.Helper()
	ctx := context.Background()
	u, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("new"))
	if err != nil || u.handoff == nil {
		t.Fatal("missing candidate", err)
	}
	// These tests exercise confirmed handoff; URL-only behavior has separate tests.
	if _, err := s.UserPlayback.Event(ctx, u.Record.EventKey, false, u.Record.Generation); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.Close() })
	req := PoolRequest{handoff: u.handoff, lifecycleKey: u.Record.EventKey, userGeneration: u.Record.Generation}
	p, err := s.Pool.Acquire(WithPoolRequest(ctx, req), "pool-new", r.UserID, []P115Account{poolTestAccount("pool", "7")}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return &MediaAuthorization{service: s, userLease: u, poolLease: p}
}

func TestPlaybackHandoffFullPoolCommitRestartAndLateEvents(t *testing.T) {
	s, r, old, oldPool, now := handoffFixture(t)
	ctx := context.Background()
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "old", "client", "movie", false)
	a := prepareHandoff(t, s, r)
	if a.poolLease.Record.BorrowedKey != oldPool.Record.Key || s.Pool.AccountStatus(a.poolLease.Account).Sessions != 1 || !old.Current() {
		t.Fatal("preparation lost old session or double counted")
	}
	// Slow file preparation may cross the quick-window boundary. No new
	// activity occurred, so the original eligible decision remains valid.
	*now = now.Add(30 * time.Second)
	if err := a.commitHandoff(ctx); err != nil {
		t.Fatal(err)
	}
	if old.Current() || oldPool.Current() || !a.userLease.Current() || s.Pool.AccountStatus(a.poolLease.Account).Sessions != 1 {
		t.Fatal("handoff did not transfer exactly one slot")
	}
	if _, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("old")); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("old session reissued", err)
	}
	if err := s.ApplyPlaybackEvent(ctx, e, true); err != nil {
		t.Fatal(err)
	}
	if !a.userLease.Current() || s.Pool.doc.Leases[a.poolLease.Record.Key].Stopped {
		t.Fatal("late stop affected new playback")
	}
	if s.CapturePlaybackEvent("http://emby", r.EmbyID, "", "client", "movie", true).Matched() {
		t.Fatal("ambiguous late fallback matched successor")
	}
	u, err := NewUserPlayback(ctx, s.db)
	if err != nil || len(u.List()) != 1 {
		t.Fatal("restart lost user transaction", err)
	}
	p, err := NewDynamicPool(ctx, s.db)
	if err != nil || p.AccountStatus(a.poolLease.Account).Sessions != 1 || len(p.doc.Leases) != 1 {
		t.Fatal("restart lost pool transaction", err)
	}
}

func TestPlaybackHandoffAbortPreservesOld(t *testing.T) {
	for _, failure := range []string{"prepare", "heartbeat", "database", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			s, r, old, oldPool, now := handoffFixture(t)
			a := prepareHandoff(t, s, r)
			ctx := context.Background()
			if failure == "heartbeat" {
				*now = now.Add(time.Second)
				if _, err := s.UserPlayback.Event(ctx, old.Record.EventKey, false, old.Record.Generation); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "database" {
				if err := s.db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if failure != "prepare" && a.commitHandoff(ctx) == nil {
				t.Fatal("invalid handoff committed")
			}
			if !old.Current() || !oldPool.Current() || s.UserPlayback.doc.Stopped[old.Record.EventKey] != 0 {
				t.Fatal("failed handoff modified old playback")
			}
			if failure != "database" {
				if err := a.poolLease.Close(); err != nil {
					t.Fatal(err)
				}
				if err := a.userLease.Close(); err != nil {
					t.Fatal(err)
				}
				if len(s.UserPlayback.List()) != 1 || len(s.Pool.doc.Leases) != 1 {
					t.Fatal("failed preparation leaked records")
				}
			}
		})
	}
}

func TestPlaybackHandoffCandidateIsolation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		alter   func(*PoolRequest, *model.MemberPlayback)
		elapsed time.Duration
		want    bool
	}{
		{"quick", func(*PoolRequest, *model.MemberPlayback) {}, 40 * time.Second, true},
		{"fresh", func(*PoolRequest, *model.MemberPlayback) {}, 10 * time.Second, false},
		{"stale", func(*PoolRequest, *model.MemberPlayback) {}, 100 * time.Second, true},
		{"other device", func(q *PoolRequest, _ *model.MemberPlayback) { q.Device = "other" }, 40 * time.Second, false},
		{"no device", func(q *PoolRequest, _ *model.MemberPlayback) { q.Device = "" }, 40 * time.Second, false},
		{"no session", func(q *PoolRequest, _ *model.MemberPlayback) { q.PlaySession = "" }, 40 * time.Second, false},
		{"download", func(q *PoolRequest, _ *model.MemberPlayback) { q.Download = true }, 40 * time.Second, false},
		{"task", func(q *PoolRequest, _ *model.MemberPlayback) { q.Task = true }, 40 * time.Second, false},
		{"source", func(_ *PoolRequest, r *model.MemberPlayback) { r.Source = "primary" }, 40 * time.Second, false},
		{"user", func(_ *PoolRequest, r *model.MemberPlayback) { r.UserID = "other"; r.EmbyID = "other" }, 40 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, _, _, now := handoffFixture(t)
			*now = now.Add(tc.elapsed - 40*time.Second)
			q := handoffRequest("new")
			tc.alter(&q, &r)
			l, err := s.UserPlayback.Acquire(context.Background(), r, q)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			if (l.handoff != nil) != tc.want {
				t.Fatal("unexpected handoff eligibility")
			}
		})
	}
}

func TestPlaybackHandoffMultipleCandidatesAndDeviceLock(t *testing.T) {
	s, r, _, _, now := handoffFixture(t)
	ctx := context.Background()
	second, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("second"))
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(100 * time.Second)
	third, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("third"))
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if third.handoff != nil {
		t.Fatal("multiple sessions guessed")
	}
	if err := s.UserPlayback.lockDevice(ctx, "device"); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := s.UserPlayback.lockDevice(wait, "device"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("same device not serialized", err)
	}
	if err := s.UserPlayback.lockDevice(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	s.UserPlayback.unlockDevice("other")
	s.UserPlayback.unlockDevice("device")
	if err := s.UserPlayback.lockDevice(ctx, "device"); err != nil {
		t.Fatal(err)
	}
	s.UserPlayback.unlockDevice("device")
}

func TestPlaybackHandoffCrossUIDRetainsResidual(t *testing.T) {
	s, r, old, oldPool, _ := handoffFixture(t)
	ctx := context.Background()
	if err := s.Pool.SetMode(ctx, "pool", "disabled"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("new"))
	if err != nil || u.handoff == nil {
		t.Fatal(err)
	}
	defer u.Close()
	if _, err := s.UserPlayback.Event(ctx, u.Record.EventKey, false, u.Record.Generation); err != nil {
		t.Fatal(err)
	}
	req := PoolRequest{handoff: u.handoff, lifecycleKey: u.Record.EventKey, userGeneration: u.Record.Generation}
	p, err := s.Pool.Acquire(WithPoolRequest(ctx, req), "pool-new", r.UserID, []P115Account{poolTestAccount("pool", "7"), poolTestAccount("other", "8")}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a := &MediaAuthorization{service: s, userLease: u, poolLease: p}
	if err := a.commitHandoff(ctx); err != nil {
		t.Fatal(err)
	}
	residual := s.Pool.doc.Leases[oldPool.Record.Key]
	if old.Current() || p.Account.UID != "8" || !residual.Stopped || residual.UID != "7" || residual.ExpiresAt > s.Pool.now().Add(2*time.Minute).UnixMilli() {
		t.Fatal("cross UID moved or lost residual")
	}
}

func TestPlaybackHandoffConcurrentCommitAndStop(t *testing.T) {
	s, r, old, _, _ := handoffFixture(t)
	a := prepareHandoff(t, s, r)
	ctx := context.Background()
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "old", "client", "movie", false)
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	var commitErr, stopErr error
	go func() { defer wg.Done(); <-start; commitErr = a.commitHandoff(ctx) }()
	go func() { defer wg.Done(); <-start; stopErr = s.ApplyPlaybackEvent(ctx, e, true) }()
	close(start)
	wg.Wait()
	if stopErr != nil {
		t.Fatal(stopErr)
	}
	if commitErr != nil && !errors.Is(commitErr, ErrUserPlaybackStopped) {
		t.Fatal(commitErr)
	}
	if old.Current() {
		t.Fatal("stopped predecessor still active")
	}
	if commitErr == nil {
		l := s.Pool.doc.Leases[a.poolLease.Record.Key]
		if !a.userLease.Current() || !l.Issued || l.Stopped {
			t.Fatal("stop corrupted committed successor")
		}
	} else if s.Pool.doc.Leases[a.poolLease.Record.Key].Issued {
		t.Fatal("failed successor issued")
	}
}

func TestPlaybackHandoffPendingRestartAndExpiry(t *testing.T) {
	s, r, _, _, now := handoffFixture(t)
	a := prepareHandoff(t, s, r)
	ctx := context.Background()
	p, err := NewDynamicPool(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return *now }
	if p.AccountStatus(a.poolLease.Account).Sessions != 1 {
		t.Fatal("restart double counted borrowed slot")
	}
	*now = now.Add(96 * time.Second)
	if p.AccountStatus(a.poolLease.Account).Sessions != 1 {
		t.Fatal("expired preparation removed predecessor")
	}
	u, err := NewUserPlayback(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	u.now = p.now
	if len(u.List()) != 1 {
		t.Fatal("expired preparation retained user record")
	}
}

func TestPlaybackHandoffNormalStopReplayAndForcedRelease(t *testing.T) {
	s, r, _, _, _ := handoffFixture(t)
	a := prepareHandoff(t, s, r)
	ctx := context.Background()
	if err := a.commitHandoff(ctx); err != nil {
		t.Fatal(err)
	}
	e := s.CapturePlaybackEvent("http://emby", r.EmbyID, "new", "client", "movie", false)
	if err := s.ApplyPlaybackEvent(ctx, e, true); err != nil {
		t.Fatal(err)
	}
	if a.userLease.Current() || !s.Pool.doc.Leases[a.poolLease.Record.Key].Stopped {
		t.Fatal("normal stop failed")
	}
	replay, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("new"))
	if err != nil {
		t.Fatal("normal stop blocked replay", err)
	}
	defer replay.Close()
	if err := s.ReleaseUserPlayback(ctx, replay.Record.Key, replay.Record.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("new")); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("forced release allowed old playback", err)
	}
}

func TestPlaybackHandoffBrowsingIsNotPlaybackEvidence(t *testing.T) {
	s, _, old, _, now := handoffFixture(t)
	*now = now.Add(60 * time.Second)
	ctx := context.Background()
	keys := map[string]bool{old.Record.ObservationKey: true}
	if err := s.UserPlayback.Reconcile(ctx, keys); err != nil {
		t.Fatal(err)
	}
	l := s.UserPlayback.doc.Leases[old.Record.Key]
	if !handoffIdle(l, *now) {
		t.Fatal("general activity prevented handoff")
	}
	missingSession := l
	missingSession.ObservationKey = ""
	if handoffIdle(missingSession, *now) {
		t.Fatal("predecessor without session ID eligible")
	}
	if err := s.UserPlayback.reconcilePlayback(ctx, keys, map[string]int64{old.Record.ObservationKey: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if handoffIdle(s.UserPlayback.doc.Leases[old.Record.Key], *now) {
		t.Fatal("actual playback evidence ignored")
	}
}
