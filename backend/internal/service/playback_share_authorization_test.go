package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"strm115/backend/internal/client/p115"
)

type shareFailureDrive struct {
	*shareHintFake
	err error
}

func (f *shareFailureDrive) DownloadInfoWithUA(context.Context, *p115.ShareInfo, string, string, string) (*p115.DownloadInfo, error) {
	f.targetReads++
	return nil, f.err
}

func TestDeferredShareFailureDoesNotProbeOrUpload(t *testing.T) {
	for _, failure := range []error{p115.ErrShareLoginRequired, p115.ErrSharePassword, p115.ErrShareThrottled, context.DeadlineExceeded, context.Canceled, &p115.HTTPError{Status: http.StatusBadGateway}} {
		t.Run(failure.Error(), func(t *testing.T) {
			s, remote, id := memberFixture(t)
			memberScan(t, s, id)
			r := s.record(id)
			a, err := s.playbackAccount(r)
			if err != nil {
				t.Fatal(err)
			}
			s.drive = mediaDirectoryDrive{memberDriveClient: s.drive, check: func(context.Context, string, string, string) error {
				t.Fatal("share error triggered directory probe")
				return nil
			}}
			ctx := WithPlaybackDirectShare(context.Background(), true)
			u := remote.users[r.EmbyID]
			grant, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, false, &u)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Close()
			cache, fast, _ := setupFastCache(t)
			f := &shareFailureDrive{shareHintFake: &shareHintFake{fastCacheFake: fast}, err: failure}
			cache.client = f
			selected := &PlaybackCacheSelection{binding: PlaybackBinding{AccountID: a.ID, ParentCID: a.PlaybackParentCID}, accountRevision: a.Revision, cookie: a.Cookie, uid: a.UID}
			_, _, err = cache.ResolveWithReport(grant.ResolutionContext(ctx), selected, &p115.ShareInfo{Code: "share", Password: "pass"}, "123", "source-private", "player")
			if !errors.Is(err, failure) || f.targetReads != 1 || f.sourceReads != 0 || f.uploads != 0 {
				t.Fatal("failure lost classification or caused fallback", err)
			}
			if grant.Commit(ctx) == nil {
				t.Fatal("failed direct read committed")
			}
		})
	}
}

func TestDeferredSharePoolHealthIsNotDirectoryEvidence(t *testing.T) {
	for _, lateFailure := range []bool{false, true} {
		p, _ := poolTest(t)
		ctx := context.Background()
		a := poolTestAccount("a", "1")
		if err := p.report(ctx, a, nil); err != nil {
			t.Fatal(err)
		}
		before := p.doc.Health[a.ID]
		lease, err := p.Acquire(ctx, "session", "user", []P115Account{a}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if lateFailure {
			if err := p.report(ctx, a, p115.ErrPlaybackDirectory); err != nil {
				t.Fatal(err)
			}
			if err := lease.Commit(ctx); !errors.Is(err, ErrAccountChanged) {
				t.Fatal("new directory failure ignored", err)
			}
			if p.doc.Health[a.ID].State != "invalid_directory" {
				t.Fatal("share success cleared directory failure")
			}
		} else {
			if err := lease.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if p.doc.Health[a.ID] != before {
				t.Fatal("share read renewed full health evidence")
			}
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeferredShareAuthorizationDirectoryGate(t *testing.T) {
	for _, mode := range []string{"direct", "cached", "fallback", "directory-failed", "credentials-changed"} {
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
					t.Fatal("directory check changed target")
				}
				if mode == "directory-failed" {
					return p115.ErrPlaybackDirectory
				}
				return nil
			}}
			ctx := WithPlaybackDirectShare(context.Background(), true)
			u := remote.users[r.EmbyID]
			grant, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, false, &u)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Close()
			if checks != 0 || !grant.deferredRead {
				t.Fatal("share performed preflight")
			}
			if err := grant.Commit(ctx); err == nil {
				t.Fatal("unresolved share was committed")
			}
			if mode == "cached" {
				if err := grant.ValidateTargetDirectory(ctx); err != nil {
					t.Fatal(err)
				}
				if checks != 1 || grant.deferredRead {
					t.Fatal("cache hit missed fresh probe")
				}
				if err := grant.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				return
			}
			cache, fast, _ := setupFastCache(t)
			fake := &shareHintFake{fastCacheFake: fast, direct: mode == "direct"}
			cache.client = fake
			selected := &PlaybackCacheSelection{binding: PlaybackBinding{AccountID: a.ID, ParentCID: a.PlaybackParentCID}, accountRevision: a.Revision, cookie: a.Cookie, uid: a.UID}
			if mode == "credentials-changed" {
				selected.cookie = "UID=999_A"
			}
			_, _, err = cache.ResolveWithReport(grant.ResolutionContext(ctx), selected, &p115.ShareInfo{Code: "share", Password: "pass"}, "123", "source-private", "player")
			switch mode {
			case "direct":
				if err != nil || checks != 0 || fake.targetReads != 1 || fake.sourceReads != 0 || fake.uploads != 0 {
					t.Fatal("direct read made unnecessary checks or writes", err)
				}
				if err := grant.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			case "fallback":
				if err != nil || checks != 1 || fake.targetReads != 1 || fake.sourceReads != 1 || fake.uploads != 1 {
					t.Fatal("fallback skipped directory gate", err)
				}
			case "directory-failed":
				if !errors.Is(err, p115.ErrPlaybackDirectory) || checks != 1 || fake.sourceReads != 0 || fake.uploads != 0 {
					t.Fatal("invalid directory reached source/write", err)
				}
				var target *playbackTargetError
				if !errors.As(err, &target) {
					t.Fatal("directory error lost target classification")
				}
			case "credentials-changed":
				if !errors.Is(err, ErrAccountChanged) || checks != 0 || fake.targetReads != 0 {
					t.Fatal("changed target reached cloud", err)
				}
			}
		})
	}
}
