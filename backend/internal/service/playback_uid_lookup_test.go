package service

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type fusedPlaybackFake struct {
	*fastCacheFake
	candidates, verified, metadata int
	lastCookie                     string
	verifyErr                      error
}

func (f *fusedPlaybackFake) FindPlaybackCandidateBySHA1(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.candidates++
	return f.fastCacheFake.FindPlaybackFileBySHA1(ctx, hash, cookie)
}
func (f *fusedPlaybackFake) FindPlaybackFileBySHA1Quick(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.metadata++
	return f.fastCacheFake.FindPlaybackFileBySHA1(ctx, hash, cookie)
}
func (f *fusedPlaybackFake) VerifiedPlaybackURL(ctx context.Context, file p115.DrivePlaybackFile, cookie, ua string) (string, error) {
	f.verified++
	f.lastCookie = cookie
	if f.verifyErr != nil {
		return "", f.verifyErr
	}
	return f.fastCacheFake.DriveDownloadURLWithUA(ctx, file.ID, file.PickCode, cookie, ua)
}

func TestPlaybackUIDLookupSurvivesNewCredentialHandle(t *testing.T) {
	s, base, selected := setupFastCache(t)
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player"); err != nil {
		t.Fatal(err)
	}
	f := &fusedPlaybackFake{fastCacheFake: base}
	s.client = f
	target := *selected
	target.binding.AccountID = "independent-config"
	target.cookie = "UID=22_A; SEID=new"
	target.accountRevision++
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), &target, "pickaaa", "source", "player"); err != nil {
		t.Fatal(err)
	}
	if f.verified != 1 || f.candidates != 0 || f.metadata != 0 || f.lastCookie != target.cookie || f.uploads != 1 {
		t.Fatal("lookup depended on config or reused credentials")
	}
	other := target
	other.uid = "99"
	other.cookie = "UID=99_A"
	if len(s.entriesByTarget(other.uid, base.files[0].SHA1)) != 0 {
		t.Fatal("cross UID ownership")
	}
}

func TestPlaybackCandidateIsVerifiedBeforePersisting(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, base, selected := setupFastCache(t)
		base.files = []p115.DrivePlaybackFile{{ID: "123", CID: "0", PickCode: "pick123", Size: 100, SHA1: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Name: "v.mkv"}}
		f := &fusedPlaybackFake{fastCacheFake: base}
		s.client = f
		if fail {
			f.verifyErr = p115.ErrDriveInvalidResponse
		}
		_, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player")
		if (err != nil) != fail || f.candidates != 1 || f.verified != 1 || f.metadata != 0 || f.uploads != 0 {
			t.Fatal("wrong fused path", err)
		}
		if fail && len(s.Status().Entries) != 0 {
			t.Fatal("unverified candidate persisted")
		}
	}
}

func TestPlaybackUIDNewestOnlyBeforeRecoverySearch(t *testing.T) {
	s, f, selected, old := setupRecovery(t)
	// An older local file is not blindly downloaded. A complete current
	// search can recover directly when all historical IDs are absent.
	alias := old
	alias.SourceKey = ""
	alias.Key = DirectLinkKey("older-copy")
	alias.File.ID = "800"
	alias.File.PickCode = "pick800"
	alias.LastUsed = time.Now().Add(-time.Hour)
	if err := s.put(alias); err != nil {
		t.Fatal(err)
	}
	s.sourceMappings = nil
	old.SourceKey = ""
	if err := s.put(old); err != nil {
		t.Fatal(err)
	}
	f.files = nil
	_, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player")
	if err != nil || f.verified != 0 || f.searches != 1 || f.attempts[old.File.ID] != 1 || f.attempts[alias.File.ID] != 0 || f.uploads != 2 {
		t.Fatal("walked stale history", err, f.attempts)
	}
}
