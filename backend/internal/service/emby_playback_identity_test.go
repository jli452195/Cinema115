package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbyPlaybackIdentityPersistenceAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identities.enc")
	store, err := NewSessionStore(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewEmbyPlaybackIdentities(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remember("http://emby", "private-player-token", "owner"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(store.Value(), "private-player-token") {
		t.Fatal("raw token persisted")
	}
	if err := s.Remember("http://emby", "private-player-token", "friend"); err == nil {
		t.Fatal("identity replaced")
	}
	reopenedStore, err := NewSessionStore(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewEmbyPlaybackIdentities(reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.UserID("http://emby", "private-player-token") != "owner" {
		t.Fatal("restart lost identity")
	}
	if reopened.UserID("http://other-emby", "private-player-token") != "" || reopened.UserID("http://emby", "other-token") != "" {
		t.Fatal("server/token isolation failed")
	}
	key := embyIdentityKey("http://emby", "private-player-token")
	reopened.entries[key] = embyPlaybackIdentity{UserID: "owner", Created: time.Now().Add(-181 * 24 * time.Hour)}
	if reopened.UserID("http://emby", "private-player-token") != "" {
		t.Fatal("expired identity accepted")
	}
}
