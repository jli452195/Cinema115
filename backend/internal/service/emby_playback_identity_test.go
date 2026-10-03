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
	if err := s.RememberDevice("http://emby", "private-player-token", "owner", "private-device", "player"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(store.Value(), "private-player-token") || strings.Contains(store.Value(), "private-device") {
		t.Fatal("raw token or device persisted")
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
	device := reopened.Device("http://emby", "private-player-token")
	if device == "" {
		t.Fatal("device lost on restart")
	}
	if err := reopened.RememberDevice("http://emby", "new-token", "owner", "private-device", "player"); err != nil {
		t.Fatal(err)
	}
	if reopened.Device("http://emby", "new-token") != device {
		t.Fatal("relogin changed device")
	}
	if err := reopened.RememberDevice("http://emby", "other-player", "owner", "private-device", "other-app"); err != nil {
		t.Fatal(err)
	}
	if reopened.Device("http://emby", "other-player") == device || reopened.Device("http://other-server", "new-token") != "" {
		t.Fatal("device scope collision")
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
