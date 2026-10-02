package service

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestInviteAloneAuthorizesRegistrationBeforePlaybackSetup(t *testing.T) {
	s, remote, _ := memberFixture(t)
	ctx := context.Background()
	// A verified Emby service suffices; no registration switch or 302 setup is needed.
	s.ProxyStatus = nil
	s.settings = model.MemberPlaybackSettings{}
	if err := s.db.SaveMemberPlaybackSettings(ctx, s.settings); err != nil {
		t.Fatal(err)
	}
	auth := NewPortalAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	invites, err := auth.GenerateInvites(ctx, "admin", "", 2, 7, "personal")
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.Register(ctx, "invited-reader", "password123", invites[0].Code)
	if err != nil {
		t.Fatal(err)
	}
	if s.record(user.ID).Source != "personal" || remote.users[user.EmbyID].Policy["EnableMediaPlayback"] != false {
		t.Fatal("registration bypassed personal binding")
	}
	if err := s.db.RevokePortalInvite(ctx, invites[1].ID); err != nil {
		t.Fatal(err)
	}
	expiredCode := "00112233445566778899aabbccddeeff"
	hash, err := inviteHash(expiredCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.CreatePortalInvites(ctx, []model.PortalInvite{{ID: "expired", CodeHash: hash, ExpiresAt: time.Now().Add(-time.Minute).Unix(), PlaybackSource: "personal"}}); err != nil {
		t.Fatal(err)
	}
	created := remote.created
	for _, code := range []string{"", "invalid", "ffeeddccbbaa99887766554433221100", invites[0].Code, invites[1].Code, expiredCode} {
		if _, err := auth.Register(ctx, "another-reader", "password123", code); !errors.Is(err, storage.ErrPortalInviteInvalid) {
			t.Fatalf("invalid invitation accepted: %v", err)
		}
	}
	if remote.created != created {
		t.Fatal("invalid invitation created an external account")
	}
}

func Test302TakeoverLifecycle(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	r := s.record(id)
	primary, err := s.accounts.AddPrimary("main")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=999_A; SEID=main"
	if err := s.accounts.Update(primary, 1, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "proxy.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewEmbyProxy(ctx, store, s.accounts, func(EmbyProxyConfig) http.Handler { return http.NotFoundHandler() })
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	s.ProxyStatus = proxy.Status
	before := remote.users[r.EmbyID].Policy
	// Neither profile discovery nor scheduled sync may take over while 302 is off.
	if _, err := s.AdminMemberDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, remote.users[r.EmbyID].Policy) {
		t.Fatal("offline sync modified Emby")
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("offline proxy authorized")
	}
	config := EmbyProxyConfig{Enabled: true, URL: "http://emby", Port: proxyFreePort(t)}
	revision := s.Settings().Revision
	if err := s.ApplyProxy(ctx, proxy, config); err != nil {
		t.Fatal(err)
	}
	if !s.Settings().Enabled || s.Settings().Revision != revision+1 {
		t.Fatal("302 did not activate takeover")
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("activation reused unchecked permissions")
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err != nil {
		t.Fatal("valid account not automatically playable", err)
	}
	config.Enabled = false
	if err := s.ApplyProxy(ctx, proxy, config); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, remote.users[r.EmbyID].Policy) {
		t.Fatal("shutdown rewrote Emby permissions")
	}
	// An administrator stop remains effective across reactivation.
	blocked := true
	if err := s.AdminUpdate(ctx, id, &blocked, "", false); err != nil {
		t.Fatal(err)
	}
	config.Enabled = true
	if err := s.ApplyProxy(ctx, proxy, config); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("reactivation removed admin stop")
	}
	blocked = false
	if err := s.AdminUpdate(ctx, id, &blocked, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err != nil {
		t.Fatal("unblocked valid account not restored", err)
	}
}

func TestInvitedUsersAutomaticallyPlayWhenSourceReady(t *testing.T) {
	s, _, _ := memberFixture(t)
	ctx := context.Background()
	primary, err := s.accounts.AddPrimary("main")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=999_A; SEID=main"
	if err := s.accounts.Update(primary, 1, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	auth := NewPortalAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	for _, source := range []string{"assigned", "personal"} {
		invites, err := auth.GenerateInvites(ctx, "admin", "", 1, 7, source)
		if err != nil {
			t.Fatal(err)
		}
		u, err := auth.Register(ctx, "new-"+source, "password123", invites[0].Code)
		if err != nil {
			t.Fatal(err)
		}
		r := s.record(u.ID)
		_, err = s.Authorize(ctx, r.EmbyURL, r.EmbyID)
		if source == "assigned" {
			if r.Source != "primary" || err != nil {
				t.Fatal("main source not automatically enabled", r.Stage, err)
			}
		} else {
			if err == nil {
				t.Fatal("unbound personal invitation fell back to main")
			}
			memberScan(t, s, u.ID)
			if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err != nil {
				t.Fatal("binding did not automatically enable playback", err)
			}
		}
	}
}

func TestIndependentMediaPermissions(t *testing.T) {
	for _, administrator := range []bool{false, true} {
		for _, playback := range []bool{true} {
			for _, download := range []bool{false, true} {
				s, remote, id := memberFixture(t)
				ctx := context.Background()
				memberScan(t, s, id)
				r := s.record(id)
				if administrator {
					// Administrators use an explicitly configured main account.
					primary, err := s.accounts.AddPrimary("main")
					if err != nil {
						t.Fatal(err)
					}
					cookie := "UID=999_A; SEID=main"
					if err := s.accounts.Update(primary, 1, "main", &cookie); err != nil {
						t.Fatal(err)
					}
					u := remote.users[r.EmbyID]
					u.Policy["IsAdministrator"] = true
					remote.users[r.EmbyID] = u
					if err := s.SetSource(ctx, id, "primary", ""); err != nil {
						t.Fatal(err)
					}
				}
				settings := s.Settings()
				settings.Enabled, settings.AllowDownload = false, download // legacy input cannot disable 302 playback
				if err := s.SetSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
				if err := s.SyncUser(ctx, id); err != nil {
					t.Fatal(err)
				}
				policy := remote.users[r.EmbyID].Policy
				if policy["EnableMediaPlayback"] != playback || policy["EnableContentDownloading"] != download {
					t.Fatalf("admin=%v playback=%v download=%v policy=%v", administrator, playback, download, policy)
				}
				for _, key := range []string{"EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing", "EnableSyncTranscoding", "EnableMediaConversion"} {
					if policy[key] != false {
						t.Fatalf("unsafe policy %s", key)
					}
				}
				for _, action := range []bool{false, true} {
					want := playback
					if action {
						want = download
					}
					if _, err := s.AuthorizeMedia(ctx, r.EmbyURL, r.EmbyID, action); (err == nil) != want {
						t.Fatalf("admin=%v playback=%v download=%v action=%v err=%v", administrator, playback, download, action, err)
					}
				}
				// Refreshing policies repeatedly must not treat our own playback-off policy as an admin block.
				if err := s.SyncUser(ctx, id); err != nil {
					t.Fatal(err)
				}
				if s.record(id).AdminBlocked {
					t.Fatal("independent action toggle blocked member")
				}
				blocked := true
				if err := s.AdminUpdate(ctx, id, &blocked, "", false); err != nil {
					t.Fatal(err)
				}
				for _, action := range []bool{false, true} {
					if _, err := s.AuthorizeMedia(ctx, r.EmbyURL, r.EmbyID, action); err == nil {
						t.Fatal("blocked user retained access")
					}
				}
			}
		}
	}
}

func TestMediaMetadataDoesNotInvalidatePermissions(t *testing.T) {
	s, _, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	before := s.Settings()
	before.PublicURL = "https://new-entry.example.com"
	if err := s.SetSettings(ctx, before); err != nil {
		t.Fatal(err)
	}
	if s.Settings().Revision != before.Revision {
		t.Fatal("metadata save changed media revision")
	}
	r := s.record(id)
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err != nil {
		t.Fatal(err)
	}
	stored, err := s.db.MemberPlaybackSettings(ctx)
	if err != nil || stored.PublicURL != before.PublicURL {
		t.Fatal("settings not persisted", err)
	}
}

func TestDownloadBindingRevocation(t *testing.T) {
	s, _, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	settings := s.Settings()
	settings.Enabled, settings.AllowDownload = false, true
	if err := s.SetSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	if _, err := s.AuthorizeMedia(ctx, r.EmbyURL, r.EmbyID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Suspend(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeMedia(ctx, r.EmbyURL, r.EmbyID, true); err == nil {
		t.Fatal("download fell back after personal unbind")
	}
}
