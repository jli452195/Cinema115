package service

// Regression scenarios originally used to reproduce per-session accumulation.
// Assertions now cover the stable device capacity model.
import (
	"context"
	"fmt"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func analysisPlayback(t *testing.T, s *MemberPlaybackService, r model.MemberPlayback, id string) (*UserPlaybackLease, error) {
	t.Helper()
	ctx := context.Background()
	q := handoffRequest(id)
	q.Item = "movie-" + id
	u, err := s.UserPlayback.Acquire(ctx, r, q)
	if err != nil {
		return nil, err
	}
	defer u.Close()
	q.handoff, q.userGeneration, q.lifecycleKey = u.handoff, u.Record.Generation, u.Record.EventKey
	p, err := s.Pool.Acquire(WithPoolRequest(ctx, q), "pool-"+id, r.UserID, []P115Account{poolTestAccount("pool", "7")}, poolProbe)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	if p.Record.SlotKey != "" {
		err = (&MediaAuthorization{service: s, userLease: u, poolLease: p}).commitDevicePlayback(ctx)
	} else if u.handoff != nil {
		err = (&MediaAuthorization{service: s, userLease: u, poolLease: p}).commitHandoff(ctx)
	} else if err = p.Commit(ctx); err == nil {
		err = u.Commit(ctx)
	}
	return u, err
}

func TestDeviceAnalysisRapidSwitchesReuseDeviceSlot(t *testing.T) {
	s, r, _, _, now := handoffFixture(t)
	ctx := context.Background()
	settings := s.Pool.Settings()
	settings.MaxSessions = 3
	if err := s.Pool.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(-35 * time.Second) // five seconds since first issuance
	for i := 1; i <= 2; i++ {
		if _, err := analysisPlayback(t, s, r, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(5 * time.Second)
	}
	if _, err := analysisPlayback(t, s, r, "fourth"); err != nil {
		t.Fatal("same device slot was not reused", err)
	}
	if n := s.Pool.AccountStatus(poolTestAccount("pool", "7")).Sessions; n != 1 {
		t.Fatal(n)
	}
	t.Log("same device, same user, same player: rapid switches reuse one UID slot")
	*now = now.Add(100 * time.Second)
	q := handoffRequest("after-silence")
	u, err := s.UserPlayback.Acquire(ctx, r, q)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	t.Log("after silence, the stable device slot remains reusable")
}

func TestDeviceAnalysisNormalStopsReuseResidualSlot(t *testing.T) {
	s, r, old, _, now := handoffFixture(t)
	ctx := context.Background()
	settings := s.Pool.Settings()
	settings.MaxSessions = 3
	if err := s.Pool.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	stop := func(l *UserPlaybackLease) {
		e := PlaybackEvent{key: l.Record.EventKey, userGeneration: l.Record.Generation, poolGenerations: make(map[string]uint64)}
		for key, p := range s.Pool.doc.Leases {
			if p.SessionKey == l.Record.EventKey {
				e.poolGenerations[key] = p.Generation
			}
		}
		if err := s.ApplyPlaybackEvent(ctx, e, true); err != nil {
			t.Fatal(err)
		}
	}
	stop(old)
	for i := 1; i <= 2; i++ {
		*now = now.Add(5 * time.Second)
		u, err := analysisPlayback(t, s, r, fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		stop(u)
	}
	if len(s.UserPlayback.List()) != 0 {
		t.Fatal("stops did not release user records")
	}
	state := s.Pool.AccountStatus(poolTestAccount("pool", "7"))
	if state.Sessions != 1 || state.Residual != 1 {
		t.Fatal(state)
	}
	if _, err := analysisPlayback(t, s, r, "fourth"); err != nil {
		t.Fatal("normal stop did not release/reuse device slot", err)
	}
	t.Log("normal stops release the matching child while the device slot remains reusable")
}

func TestDeviceAnalysisPrefetchPreservesCurrentWithoutPlayingEvent(t *testing.T) {
	s, r, old, _, _ := handoffFixture(t)
	settings := s.Pool.Settings()
	settings.MaxSessions = 2
	if err := s.Pool.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	// The next-item file request has no dedicated prefetch signal. It follows
	// the same acquire/commit path even if no Playing event ever arrives.
	if _, err := analysisPlayback(t, s, r, "prefetch"); err != nil {
		t.Fatal(err)
	}
	if !old.Current() || s.UserPlayback.doc.Stopped[old.Record.EventKey] != 0 {
		t.Fatal("URL-only prefetch retired current playback")
	}
	l, err := s.UserPlayback.Acquire(context.Background(), r, handoffRequest("old"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Log("next-item URL alone preserves current playback and shares its device slot")
}

func TestDeviceAnalysisPrefetchReusesDeviceSlot(t *testing.T) {
	s, r, old, _, _ := handoffFixture(t)
	if _, err := s.UserPlayback.Event(context.Background(), old.Record.EventKey, false, old.Record.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := analysisPlayback(t, s, r, "prefetch"); err != nil {
		t.Fatal("prefetch did not reuse device slot", err)
	}
	if !old.Current() {
		t.Fatal("active current playback changed")
	}
	t.Log("with one UID slot and a current playback heartbeat, next-item prefetch reuses the device slot")
}
