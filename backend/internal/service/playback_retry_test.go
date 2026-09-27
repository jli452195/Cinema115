package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestPlaybackLegacyUncertainDoesNotBlockSHA1Miss(t *testing.T) {
	for _, state := range []string{"uncertain", "creating", "uploading", "deleting"} {
		t.Run(state, func(t *testing.T) {
			s, f, selected := setupFastCache(t)
			old := PlaybackCacheEntry{Key: strings.Repeat("b", 64), AccountID: selected.binding.AccountID, UID: selected.uid, ParentCID: "old-folder", Status: state, File: p115.DrivePlaybackFile{SHA1: strings.Repeat("a", 40)}, Message: "旧版结果未确认"}
			if err := s.put(old); err != nil {
				t.Fatal(err)
			}
			ctx, finish := s.logs.BeginRequest(context.Background(), true)
			_, report, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "src", "player")
			finish()
			if err != nil || f.uploads != 1 || f.creates != 0 || report.FileCacheID == "" {
				t.Fatal("legacy record blocked retry", err)
			}
			rows := s.logs.Snapshot()
			if len(rows) != 1 || rows[0].Details == nil {
				t.Fatal("retry logs not grouped")
			}
			for _, want := range []string{old.Key, "状态=" + state, "SHA1=" + strings.Repeat("A", 40), old.Message} {
				if !strings.Contains(rows[0].Details.Steps, want) {
					t.Fatal("missing diagnostic", want)
				}
			}
		})
	}
}

func TestPlaybackUncertainCooldownExpiresAndSurvivesRestart(t *testing.T) {
	s, f, selected := setupFastCache(t)
	f.uploadErr = p115.ErrInstantUncertain
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err == nil {
		t.Fatal("failure hidden")
	}
	restarted, err := NewPlaybackCache(context.Background(), s.store.(*SessionStore), s.accounts, f, s.logs)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	deadline := restarted.Status().Entries[0].RetryNotBefore
	if deadline.IsZero() || time.Until(deadline) > playbackRetryCooldown {
		t.Fatal("invalid persisted cooldown")
	}
	f.uploadErr = nil
	for i := 0; i < 2; i++ {
		if _, _, err := restarted.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err == nil || !strings.Contains(err.Error(), "秒后重试") {
			t.Fatal("cooldown not applied", err)
		}
	}
	if f.uploads != 1 || !deadline.Equal(restarted.Status().Entries[0].RetryNotBefore) {
		t.Fatal("cooldown retry wrote or extended deadline")
	}
	e := restarted.Status().Entries[0]
	e.RetryNotBefore = time.Now().Add(-time.Second)
	if err := restarted.put(e); err != nil {
		t.Fatal(err)
	}
	if _, _, err := restarted.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err != nil {
		t.Fatal("expired cooldown blocked retry", err)
	}
	if f.uploads != 2 || restarted.Status().Entries[0].Status != "ready" || !restarted.Status().Entries[0].RetryNotBefore.IsZero() {
		t.Fatal("retry did not complete")
	}
}

func TestPlaybackUncertainSearchErrorNeverUploads(t *testing.T) {
	s, f, selected := setupFastCache(t)
	old := PlaybackCacheEntry{Key: strings.Repeat("a", 64), AccountID: selected.binding.AccountID, UID: selected.uid, Status: "uncertain", File: p115.DrivePlaybackFile{SHA1: strings.Repeat("A", 40)}}
	if err := s.put(old); err != nil {
		t.Fatal(err)
	}
	f.searchErr = errors.New("network")
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err == nil {
		t.Fatal("search failure hidden")
	}
	if f.uploads != 0 || f.prepares != 0 || s.Status().Entries[0] != old {
		t.Fatal("search failure mutated state")
	}
}
