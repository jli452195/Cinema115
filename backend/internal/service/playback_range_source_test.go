package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type fusedRangeFake struct {
	*materialFake
	fusedReads      int
	wrongHash       bool
	account, cookie string
}

func (f *fusedRangeFake) PlaybackPickCodeRangeSource(_ context.Context, account, pc, cookie string) (*p115.DownloadInfo, error) {
	f.fusedReads++
	f.account, f.cookie = account, cookie
	hash := strings.Repeat("A", 40)
	if f.wrongHash {
		hash = strings.Repeat("B", 40)
	}
	id := "12"
	for _, file := range f.files {
		if file.PickCode == pc {
			id = file.ID
			break
		}
	}
	return &p115.DownloadInfo{FileID: id, Name: "v.mkv", Size: 100, SHA1: hash, URL: "https://cdn.115.com/source"}, nil
}

func TestKnownMaterialLoadsFusedSourceOnlyForUncachedChallenge(t *testing.T) {
	for _, mode := range []string{"missing-range", "cached-range", "different-range", "wrong-hash", "no-challenge"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			f := &fusedRangeFake{materialFake: &materialFake{fastCacheFake: base, challenge: mode != "no-challenge"}, wrongHash: mode == "wrong-hash"}
			s.client = f
			hash := strings.Repeat("A", 40)
			m := PlaybackMaterial{ID: playbackMaterialID(hash, 100), Name: "v.mkv", Size: 100, SHA1: hash, VerifiedAt: time.Now().UTC()}
			if mode == "cached-range" || mode == "different-range" {
				start, end := int64(1), int64(3)
				if mode == "different-range" {
					start, end = 2, 4
				}
				m.Ranges = []PlaybackRange{{Start: start, End: end, SHA1: strings.Repeat("C", 40), VerifiedAt: time.Now().UTC()}}
			}
			if err := s.putMaterial(m); err != nil {
				t.Fatal(err)
			}
			ctx := WithPlaybackSHA1Hint(context.Background(), hash)
			_, report, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "UID=11_A", "player", "main")
			if (err != nil) != f.wrongHash {
				t.Fatal(mode, err)
			}
			wantFused, wantRange := 1, 1
			if mode == "cached-range" || mode == "no-challenge" {
				wantFused, wantRange = 0, 0
			}
			if mode == "wrong-hash" {
				wantRange = 0
			}
			if f.fusedReads != wantFused || f.rangeReads != wantRange || f.donorReads != 0 || f.reads != 0 {
				t.Fatal("redundant source query or missing hash check", f.fusedReads, f.rangeReads, f.donorReads, f.reads)
			}
			if wantFused > 0 && (f.account != "main" || f.cookie != "UID=11_A") {
				t.Fatal("source credentials changed")
			}
			if err == nil && report.UsedSource != (wantFused > 0) {
				t.Fatal("incorrect source report")
			}
			if mode == "wrong-hash" && len(s.Status().Materials[0].Ranges) != 0 {
				t.Fatal("unaccepted hash saved")
			}
		})
	}
}

func TestDonorRangeUsesCombinedResponseAndChecksFileID(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &fusedRangeFake{materialFake: &materialFake{fastCacheFake: base}}
	s.client = f
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "UID=11_A", "player", "main"); err != nil {
		t.Fatal(err)
	}
	target := *selected
	target.binding.AccountID, target.uid, target.cookie = "another", "33", "UID=33_A"
	target.donors = s.Snapshot().selections[cacheTestUser].donors
	f.challenge = true
	before := f.donorReads
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), &target, "pickaaa", "", "player", "main"); err != nil {
		t.Fatal(err)
	}
	if f.fusedReads != 1 || f.donorReads != before || f.cookie != selected.cookie || f.account != selected.binding.AccountID {
		t.Fatal("donor did not use pinned combined lookup")
	}
}
