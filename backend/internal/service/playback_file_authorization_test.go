package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type authorizedFileDrive struct {
	*fastCacheFake
	linkError error
	linkReads int
	afterRead func()
}

func (f *authorizedFileDrive) VerifiedPlaybackURL(_ context.Context, file p115.DrivePlaybackFile, cookie, _ string) (string, error) {
	f.linkReads++
	if file.ID == "" || file.SHA1 != strings.Repeat("A", 40) || file.Size != 100 || p115.CookieUserID(cookie) == "" {
		return "", p115.ErrDriveInvalidResponse
	}
	if f.afterRead != nil {
		f.afterRead()
	}
	if f.linkError != nil {
		return "", f.linkError
	}
	return "https://cdn.115.com/verified", nil
}

// Exercise authorization, material lookup, target lookup and persistence
// together. The fake counts remote work, not implementation helper calls.
func TestFileReuseDeferredAuthorization(t *testing.T) {
	for _, mode := range []string{"copy", "search", "upload", "unknown", "cached", "cached-invalid", "directory-invalid", "login-invalid", "identity-invalid", "network", "revoked", "wrong-target", "canceled", "download", "task"} {
		t.Run(mode, func(t *testing.T) {
			s, remote, id := memberFixture(t)
			memberScan(t, s, id)
			r := s.record(id)
			a, err := s.playbackAccount(r)
			if err != nil {
				t.Fatal(err)
			}
			checks := 0
			s.drive = mediaDirectoryDrive{memberDriveClient: s.drive, check: func(_ context.Context, scope, cid, cookie string) error {
				checks++
				if scope != a.ID || cid != a.PlaybackParentCID || cookie != a.Cookie {
					t.Fatal("target snapshot changed")
				}
				if mode == "directory-invalid" || mode == "cached-invalid" {
					return p115.ErrPlaybackDirectory
				}
				return nil
			}}
			cache, base, _ := setupFastCache(t)
			f := &authorizedFileDrive{fastCacheFake: base}
			cache.client = f
			selected := &PlaybackCacheSelection{binding: PlaybackBinding{AccountID: a.ID, ParentCID: a.PlaybackParentCID}, accountRevision: a.Revision, cookie: a.Cookie, uid: a.UID}
			file := p115.DrivePlaybackFile{ID: "123", PickCode: "pick123", CID: a.PlaybackParentCID, Name: "movie.mkv", SHA1: strings.Repeat("A", 40), Size: 100}
			if mode != "unknown" {
				m := PlaybackMaterial{ID: playbackMaterialID(file.SHA1, file.Size), SHA1: file.SHA1, Size: file.Size, Name: file.Name, Origins: []string{DirectLinkKey("pickcode-material", "primary", "pickaaa")}, VerifiedAt: time.Now().UTC()}
				if err := cache.putMaterial(m); err != nil {
					t.Fatal(err)
				}
				if mode != "search" && mode != "upload" && mode != "directory-invalid" {
					if _, err := cache.storeReadyCopy(PlaybackCacheEntry{UID: a.UID, MaterialID: m.ID, File: file, Status: "ready"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "search" {
				f.files = []p115.DrivePlaybackFile{file}
			}
			switch mode {
			case "login-invalid":
				f.linkError = p115.ErrShareLoginRequired
			case "identity-invalid":
				f.linkError = p115.ErrDriveInvalidResponse
			case "network":
				f.linkError = context.DeadlineExceeded
			case "revoked":
				f.afterRead = func() { s.mu.Lock(); v := s.records[id]; v.Suspended = true; s.records[id] = v; s.mu.Unlock() }
			}
			ctx, cancel := context.WithCancel(WithPlaybackFileReuse(context.Background()))
			defer cancel()
			if mode == "task" {
				ctx = WithPoolRequest(ctx, PoolRequest{Task: true})
			}
			u := remote.users[r.EmbyID]
			u.Policy["EnableContentDownloading"] = true
			if mode == "download" {
				settings := s.Settings()
				settings.AllowDownload = true
				if err := s.SetSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
			}
			grant, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, mode == "download", &u)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Close()
			if mode == "download" || mode == "task" {
				if checks != 1 || grant.deferredRead {
					t.Fatal("non-playback bypassed preflight")
				}
				return
			}
			if checks != 0 || grant.Commit(ctx) == nil {
				t.Fatal("unverified request was admitted")
			}
			if mode == "cached" || mode == "cached-invalid" {
				err := grant.ValidateTargetDirectory(ctx)
				if checks != 1 || f.linkReads != 0 {
					t.Fatal("cache hit did not freshly check credentials")
				}
				if mode == "cached-invalid" {
					if err == nil || grant.Commit(ctx) == nil {
						t.Fatal("invalid cached link admitted")
					}
				} else if err != nil || grant.Commit(ctx) != nil {
					t.Fatal("healthy cache rejected", err)
				}
				return
			}
			if mode == "wrong-target" {
				selected.cookie = "UID=999_A"
			}
			if mode == "canceled" {
				cancel()
			}
			_, report, err := cache.ResolvePickCodeWithReport(grant.ResolutionContext(ctx), selected, "pickaaa", "source", "player", "primary")
			failed := mode == "directory-invalid" || mode == "login-invalid" || mode == "identity-invalid" || mode == "network" || mode == "revoked" || mode == "wrong-target" || mode == "canceled"
			if failed {
				if err == nil || grant.Commit(ctx) == nil || f.uploads != 0 {
					t.Fatal("failure committed or uploaded", err)
				}
				if mode == "wrong-target" && (!errors.Is(err, ErrAccountChanged) || f.linkReads != 0) {
					t.Fatal("wrong credentials reached remote")
				}
			} else if err != nil || grant.Commit(ctx) != nil {
				t.Fatal("valid resolution failed", err)
			}
			wantChecks, wantSource, wantUpload := 0, 0, 0
			if mode == "upload" || mode == "unknown" || mode == "directory-invalid" {
				wantChecks = 1
			}
			if mode == "unknown" {
				wantSource = 1
			}
			if mode == "upload" || mode == "unknown" {
				wantUpload = 1
			}
			if checks != wantChecks || f.reads != wantSource || f.uploads != wantUpload {
				t.Fatalf("checks=%d source=%d uploads=%d", checks, f.reads, f.uploads)
			}
			if (mode == "copy" || mode == "search") && (f.linkReads != 1 || report.UsedSource) {
				t.Fatal("reuse did extra source/link work")
			}
		})
	}
}
