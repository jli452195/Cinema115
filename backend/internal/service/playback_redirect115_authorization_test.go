package service

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type redirectAuthorizedDrive struct {
	*authorizedFileDrive
	queries      int
	beforeUpload func()
	prepare      func(context.Context) error
}

func (f *redirectAuthorizedDrive) PreparePlaybackAccount(ctx context.Context, scope, cookie string) (*p115.PlaybackUploadTicket, error) {
	if f.prepare != nil {
		if err := f.prepare(ctx); err != nil {
			return nil, err
		}
	}
	return f.fastCacheFake.PreparePlaybackAccount(ctx, scope, cookie)
}

func (f *redirectAuthorizedDrive) FindPlaybackFileBySHA1Quick(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.queries++
	return f.fastCacheFake.FindPlaybackFileBySHA1(ctx, hash, cookie)
}

func (f *redirectAuthorizedDrive) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, scope, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	f.beforeUpload()
	return f.fastCacheFake.UploadPlayback(ctx, info, scope, cid, cookie, ticket, progress)
}

func TestRedirect115DeferredDirectoryAuthorization(t *testing.T) {
	testDeclaredDirectoryAuthorization(t, false)
}

func TestPlay115DeferredDirectoryAuthorization(t *testing.T) {
	testDeclaredDirectoryAuthorization(t, true)
}

func testDeclaredDirectoryAuthorization(t *testing.T, play115 bool) {
	for _, mode := range []string{"hit", "hit-directory-invalid", "miss", "miss-directory-invalid", "miss-ticket-error", "miss-revoked-preflight", "query-error", "identity-error", "revoked", "download"} {
		t.Run(mode, func(t *testing.T) {
			s, remote, id := memberFixture(t)
			memberScan(t, s, id)
			r := s.record(id)
			a, err := s.playbackAccount(r)
			if err != nil {
				t.Fatal(err)
			}
			cache, base, _ := setupFastCache(t)
			f := &redirectAuthorizedDrive{authorizedFileDrive: &authorizedFileDrive{fastCacheFake: base}}
			cache.client = f
			checks := 0
			directoryChecked := make(chan struct{})
			if mode == "miss-ticket-error" || mode == "miss-revoked-preflight" {
				f.prepare = func(ctx context.Context) error {
					select {
					case <-directoryChecked:
					case <-ctx.Done():
						return ctx.Err()
					}
					if mode == "miss-ticket-error" {
						return p115.ErrShareLoginRequired
					}
					s.mu.Lock()
					v := s.records[id]
					v.Suspended = true
					s.records[id] = v
					s.mu.Unlock()
					return nil
				}
			}
			s.drive = mediaDirectoryDrive{memberDriveClient: s.drive, check: func(_ context.Context, scope, cid, cookie string) error {
				checks++
				defer close(directoryChecked)
				if scope != a.ID || cid != a.PlaybackParentCID || cookie != a.Cookie {
					t.Fatal("directory check changed target")
				}
				if strings.HasPrefix(mode, "miss") {
					if f.queries != 1 || f.uploads != 0 {
						t.Fatal("directory checked before miss or after upload")
					}
				}
				if strings.HasSuffix(mode, "directory-invalid") {
					return p115.ErrPlaybackDirectory
				}
				return nil
			}}
			f.beforeUpload = func() {
				if checks != 1 {
					t.Fatal("write lacked exactly one directory check")
				}
			}
			file := p115.DrivePlaybackFile{ID: "123", PickCode: "pick123", CID: a.PlaybackParentCID, Name: "movie.mkv", SHA1: strings.Repeat("A", 40), Size: 100}
			if !strings.HasPrefix(mode, "miss") {
				base.files = []p115.DrivePlaybackFile{file}
			}
			if mode == "query-error" {
				base.searchErr = p115.ErrShareLoginRequired
			}
			if mode == "identity-error" {
				f.linkError = p115.ErrDriveInvalidResponse
			}
			if mode == "revoked" {
				f.afterRead = func() { s.mu.Lock(); v := s.records[id]; v.Suspended = true; s.records[id] = v; s.mu.Unlock() }
			}
			ctx := WithRedirect115SHA1(WithPlaybackFileReuse(context.Background()), file.SHA1)
			if play115 {
				ctx = WithPlay115Source(ctx, Play115Source{FileID: "999", SHA1: file.SHA1, Size: file.Size, Name: file.Name})
			}
			user := remote.users[r.EmbyID]
			if mode == "download" {
				user.Policy["EnableContentDownloading"] = true
				settings := s.Settings()
				settings.AllowDownload = true
				if err := s.SetSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
			}
			grant, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, mode == "download", &user)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Close()
			if mode != "download" && grant.Commit(ctx) == nil {
				t.Fatal("unverified grant committed")
			}
			selected := &PlaybackCacheSelection{binding: PlaybackBinding{AccountID: a.ID, ParentCID: a.PlaybackParentCID}, accountRevision: a.Revision, cookie: a.Cookie, uid: a.UID}
			link, report, err := cache.ResolvePickCodeWithReport(grant.ResolutionContext(ctx), selected, "pickaaa", "source", "player", "primary")
			failed := mode == "miss-directory-invalid" || mode == "miss-ticket-error" || mode == "miss-revoked-preflight" || mode == "query-error" || mode == "identity-error" || mode == "revoked"
			if failed {
				if err == nil || link != "" || f.uploads != 0 || len(cache.Status().Entries) != 0 {
					t.Fatal("failed read/write admitted or persisted", err)
				}
				// A ticket failure can follow a valid directory authorization.
				// The caller must not commit after resolution returns an error.
				if mode != "miss-ticket-error" && grant.Commit(ctx) == nil {
					t.Fatal("invalid authorization committed")
				}
			} else if err != nil || grant.Commit(ctx) != nil {
				t.Fatal("valid resolution failed", err)
			}
			wantChecks, wantReads, wantUploads := 0, 0, 0
			if strings.HasPrefix(mode, "miss") {
				wantChecks, wantReads = 1, 1
				if play115 {
					wantReads = 0
				}
			}
			if mode == "download" {
				wantChecks = 1
			}
			if mode == "miss" {
				wantUploads = 1
			}
			if checks != wantChecks || base.reads != wantReads || base.uploads != wantUploads || f.queries != 1 {
				t.Fatalf("checks=%d primary=%d uploads=%d queries=%d", checks, base.reads, base.uploads, f.queries)
			}
			if !failed && !strings.HasPrefix(mode, "miss") && report.UsedSource {
				t.Fatal("target hit used primary")
			}
			if (mode == "hit" || mode == "hit-directory-invalid") && (!grant.deferredRead || !grant.targetRead) {
				t.Fatal("file read incorrectly proved directory health")
			}
			if !strings.HasPrefix(mode, "miss") && base.prepares != 0 {
				t.Fatal("target hit or rejected lookup prepared an upload")
			}
		})
	}
}
