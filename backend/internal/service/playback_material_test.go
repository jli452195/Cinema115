package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type materialFake struct {
	*fastCacheFake
	challenge, failRange, wrongIdentity bool
	rangeReads, donorReads              int
	donorCookie                         string
	afterDonorRead                      func()
}

func (f *materialFake) PlaybackPickCodeSource(_ context.Context, _ string, pc string, cookie string) (*p115.DownloadInfo, error) {
	f.donorReads++
	f.donorCookie = cookie
	if f.afterDonorRead != nil {
		f.afterDonorRead()
	}
	hash := strings.Repeat("A", 40)
	if f.wrongIdentity {
		hash = strings.Repeat("B", 40)
	}
	id := ""
	for _, file := range f.files {
		if file.PickCode == pc {
			id = file.ID
			break
		}
	}
	return &p115.DownloadInfo{FileID: id, Name: "v.mkv", Size: 100, SHA1: hash, URL: "https://cdn.115.com/copy"}, nil
}
func (f *materialFake) PlaybackRangeSHA1(context.Context, *p115.DownloadInfo, int64, int64) (string, error) {
	f.rangeReads++
	if f.failRange {
		return "", errors.New("private-source-cookie")
	}
	return strings.Repeat("C", 40), nil
}
func (f *materialFake) FindPlaybackFileBySHA1(context.Context, string, string) (*p115.DrivePlaybackFile, error) {
	return nil, nil
}
func (f *materialFake) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, scope, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	if f.challenge {
		if _, err := info.ResolveRangeSHA1(ctx, 1, 3); err != nil {
			return nil, err
		}
	}
	return f.fastCacheFake.UploadPlayback(ctx, info, scope, cid, cookie, ticket, progress)
}

func TestPlaybackMaterialPersistsAndReusesAcceptedRangeWithoutSource(t *testing.T) {
	s, fast, selected := setupFastCache(t)
	f := &materialFake{fastCacheFake: fast, challenge: true}
	s.client = f
	ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, DirectLinkKey("origin"))
	sourceReads := 0
	load := func() (*p115.DownloadInfo, error) {
		sourceReads++
		return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/private-link"}, nil
	}
	var report PlaybackResolution
	if _, err := s.resolveInstant(ctx, selected, "private-source-cookie", "player", &report, load); err != nil {
		t.Fatal(err)
	}
	status := s.Status()
	if sourceReads != 1 || f.rangeReads != 1 || len(status.Materials) != 1 || len(status.Materials[0].Ranges) != 1 || status.Entries[0].MaterialID != status.Materials[0].ID {
		t.Fatal("material or accepted range not persisted")
	}
	// Public snapshots and persistence must not contain session data.
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-source-cookie", "SEID=", "private-link", "sign_key", "sign_val"} {
		if strings.Contains(string(raw), secret) || strings.Contains(s.store.Value(), secret) {
			t.Fatal("material leaked credentials")
		}
	}
	status.Materials[0].Ranges[0].SHA1 = "changed"
	if s.Status().Materials[0].Ranges[0].SHA1 == "changed" {
		t.Fatal("status aliases stored ranges")
	}
	restarted, err := NewPlaybackCache(ctx, s.store.(*SessionStore), s.accounts, f, s.logs)
	if err != nil {
		t.Fatal(err)
	}
	// A new target has no cloud file and uses saved metadata/range after restart.
	target := *selected
	target.binding.AccountID = "another-account"
	target.uid, target.cookie = "33", "UID=33_A"
	loadUnavailable := func() (*p115.DownloadInfo, error) { t.Fatal("read source despite complete material"); return nil, nil }
	report = PlaybackResolution{}
	if _, err := restarted.resolveInstant(ctx, &target, "", "player", &report, loadUnavailable); err != nil {
		t.Fatal(err)
	}
	if report.UsedSource || f.rangeReads != 1 || f.uploads != 2 {
		t.Fatal("did not reuse saved range for another target")
	}
}

func TestPlaybackMaterialUsesPinnedDonorAndPreservesDestination(t *testing.T) {
	s, fast, selected := setupFastCache(t)
	f := &materialFake{fastCacheFake: fast}
	s.client = f
	ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, DirectLinkKey("origin"))
	load := func() (*p115.DownloadInfo, error) {
		return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
	}
	if _, err := s.resolveInstant(ctx, selected, "source", "player", &PlaybackResolution{}, load); err != nil {
		t.Fatal(err)
	}
	target := *selected
	target.binding.AccountID, target.uid, target.cookie = "another", "33", "UID=33_A"
	// This snapshot was captured before network I/O; subsequent edits cannot switch it.
	target.donors = s.Snapshot().selections[cacheTestUser].donors
	f.afterDonorRead = func() {
		newCookie := "UID=22_A; SEID=changed"
		if err := s.accounts.Update(selected.binding.AccountID, 3, "朋友", &newCookie); err != nil {
			t.Fatal(err)
		}
	}
	f.challenge = true
	report := PlaybackResolution{}
	if _, err := s.resolveInstant(ctx, &target, "", "player", &report, func() (*p115.DownloadInfo, error) { t.Fatal("unexpected primary read"); return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if f.donorReads != 1 || f.donorCookie != selected.cookie || report.UsedSource || len(s.Status().Materials[0].Ranges) != 1 {
		t.Fatal("donor credentials or range cache incorrect")
	}
	entries := s.Status().Entries
	if len(entries) != 2 || entries[1].AccountID != "another" || entries[1].UID != "33" {
		t.Fatal("donor changed the destination")
	}
}

func TestPlaybackMaterialRejectsWrongDonorAndDoesNotCommitFailedChallenge(t *testing.T) {
	for _, mode := range []string{"wrong-identity", "failed-upload", "deleted-donor"} {
		t.Run(mode, func(t *testing.T) {
			s, fast, selected := setupFastCache(t)
			f := &materialFake{fastCacheFake: fast}
			s.client = f
			ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, DirectLinkKey("origin"))
			load := func() (*p115.DownloadInfo, error) {
				return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
			}
			if _, err := s.resolveInstant(ctx, selected, "source", "player", &PlaybackResolution{}, load); err != nil {
				t.Fatal(err)
			}
			if mode == "deleted-donor" {
				if err := s.accounts.Delete(selected.binding.AccountID, 3); err != nil {
					t.Fatal(err)
				}
			}
			target := *selected
			target.binding.AccountID, target.uid, target.cookie = "another", "33", "UID=33_A"
			target.donors = s.Snapshot().selections[cacheTestUser].donors
			f.challenge, f.wrongIdentity = true, mode == "wrong-identity"
			if mode == "failed-upload" {
				f.uploadErr = p115.ErrInstantUnsupported
			}
			if _, err := s.resolveInstant(ctx, &target, "", "player", &PlaybackResolution{}, load); err == nil {
				t.Fatal("invalid source/upload accepted")
			}
			if len(s.Status().Materials[0].Ranges) != 0 {
				t.Fatal("failed challenge persisted")
			}
			if mode != "failed-upload" && f.rangeReads != 0 {
				t.Fatal("read unverified donor")
			}
			if mode == "deleted-donor" && f.donorReads != 0 {
				t.Fatal("read deleted account")
			}
		})
	}
}

func TestPlaybackMaterialRangeMustMatchExactlyAndCanBeForgottenIndependently(t *testing.T) {
	s, fast, selected := setupFastCache(t)
	f := &materialFake{fastCacheFake: fast, challenge: true}
	s.client = f
	ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, DirectLinkKey("origin"))
	load := func() (*p115.DownloadInfo, error) {
		return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
	}
	if _, err := s.resolveInstant(ctx, selected, "source", "player", &PlaybackResolution{}, load); err != nil {
		t.Fatal(err)
	}
	material := s.Status().Materials[0]
	info, _, _, err := s.preparePlaybackMaterial(ctx, selected, &PlaybackResolution{}, func() (*p115.DownloadInfo, error) { return nil, errors.New("source offline") })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := info.ResolveRangeSHA1(ctx, 2, 4); !errors.Is(err, p115.ErrPlaybackRangeUnavailable) {
		t.Fatal("different range reused stored hash", err)
	}
	if len(s.Status().Materials[0].Ranges) != 1 {
		t.Fatal("missing range changed material")
	}
	if err := s.ForgetMaterial(ctx, material.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Status().Materials) != 0 || len(s.Status().Entries) != 1 || len(f.files) != 1 {
		t.Fatal("forget material removed copy/cloud file")
	}
}
