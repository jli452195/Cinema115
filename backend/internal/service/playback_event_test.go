package service

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
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
