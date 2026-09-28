package service

import (
	"context"
	"testing"
)

func TestPlaybackMappingSurvivesRestartAndSeparatesCredentials(t *testing.T) {
	s, f, selected := setupFastCache(t)
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player", "source-id"); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPlaybackCache(context.Background(), s.store.(*SessionStore), s.accounts, f, s.logs)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, r, err := restarted.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "newUA", "source-id"); err != nil || r.UsedSource {
		t.Fatal("restart reread source", err)
	}
	if f.reads != 1 || f.uploads != 1 || f.ua != "newUA" {
		t.Fatal("persistent mapping not reused")
	}
	changed := *selected
	changed.cookie = "UID=22_B"
	changed.accountRevision++
	if _, r, err := restarted.ResolvePickCodeWithReport(context.Background(), &changed, "pickaaa", "src", "newUA", "source-id"); err != nil || r.UsedSource {
		t.Fatal("credential change should reuse metadata, not credentials", err)
	}
	if f.reads != 1 || f.uploads != 1 || f.cookie != changed.cookie {
		t.Fatal("credential isolation failed")
	}
}
