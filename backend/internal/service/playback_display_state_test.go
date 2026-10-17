package service

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
)

func TestPlaybackDisplayStateEventsAreOrderedAndGenerationBound(t *testing.T) {
	for _, atomic := range []bool{false, true} {
		t.Run(map[bool]string{false: "user-only", true: "atomic"}[atomic], func(t *testing.T) {
			s, r, lease, _, now := handoffFixture(t)
			if !atomic {
				s.Pool = nil
			}
			ctx := context.Background()
			paused, playing := true, false
			capture := func(p *bool) PlaybackEvent {
				return s.CapturePlaybackEvent("http://emby", r.EmbyID, "old", "client", "movie", false).WithPlaybackPause(p)
			}
			read := func() model.UserPlaybackLease { return s.UserPlayback.doc.Leases[lease.Record.Key] }
			if read().PlaybackState != "" {
				t.Fatal("link issuance alone supplied playback state")
			}
			older := capture(&playing)
			*now = now.Add(time.Second)
			newer := capture(&paused)
			if err := s.ApplyPlaybackEvent(ctx, newer, false); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyPlaybackEvent(ctx, older, false); err != nil {
				t.Fatal(err)
			}
			if read().PlaybackState != "paused" || read().PlaybackStateAt != newer.observedAt {
				t.Fatal("late progress response overwrote newer pause")
			}
			*now = now.Add(time.Second)
			if err := s.ApplyPlaybackEvent(ctx, capture(nil), false); err != nil {
				t.Fatal(err)
			}
			if read().PlaybackStateAt != newer.observedAt {
				t.Fatal("event without pause data refreshed display evidence")
			}
			if err := s.ApplyPlaybackEvent(ctx, capture(&playing), false); err != nil {
				t.Fatal(err)
			}
			persisted, err := s.db.UserPlayback(ctx)
			if err != nil || persisted.Leases[lease.Record.Key].PlaybackState != "playing" {
				t.Fatal("resume not persisted", err)
			}
			if err := s.ApplyPlaybackEvent(ctx, capture(nil), true); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(time.Second)
			replay, err := s.UserPlayback.Acquire(ctx, r, handoffRequest("old"))
			if err != nil {
				t.Fatal(err)
			}
			defer replay.Close()
			if err := replay.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyPlaybackEvent(ctx, newer, false); err != nil {
				t.Fatal(err)
			}
			if read().PlaybackState != "" {
				t.Fatal("old generation supplied state to replay")
			}
		})
	}
}

func TestPlaybackDisplayStateReusesExactEmbyObservations(t *testing.T) {
	s, r, lease, _, now := handoffFixture(t)
	remote := &phaseOneRemote{memberEmbyClient: s.NewEmby("", "")}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	paused := true
	listed := emby.PlaybackSession{UserID: r.EmbyID, PlaySessionID: "old", LastPlaybackCheckIn: *now}
	listed.NowPlayingItem.ID = "movie"
	listed.PlayState.IsPaused = &paused
	remote.sessions = []emby.PlaybackSession{listed}
	s.reconcilePoolSessions(context.Background())
	if s.UserPlayback.doc.Leases[lease.Record.Key].PlaybackState != "paused" {
		t.Fatal("exact paused observation not projected")
	}
	*now = now.Add(time.Minute)
	listed.PlaySessionID = "other"
	listed.LastPlaybackCheckIn = *now
	paused = false
	remote.sessions = []emby.PlaybackSession{listed}
	s.reconcilePoolSessions(context.Background())
	if s.UserPlayback.doc.Leases[lease.Record.Key].PlaybackState != "paused" {
		t.Fatal("unrelated session changed card state")
	}
}

func TestPlaybackDisplayStateRejectsInvalidEvidence(t *testing.T) {
	now := time.Now()
	for _, at := range []int64{0, now.Add(time.Second).UnixMilli(), now.Add(-91 * time.Second).UnixMilli()} {
		l := model.UserPlaybackLease{}
		if observePlaybackState(&l, "playing", at, now) {
			t.Fatal("invalid display evidence accepted", at)
		}
	}
}
