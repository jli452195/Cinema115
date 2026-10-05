package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type shareHintFake struct {
	*fastCacheFake
	direct                   bool
	targetReads, sourceReads int
}

func (f *shareHintFake) DownloadInfoWithUA(_ context.Context, _ *p115.ShareInfo, _, cookie, _ string) (*p115.DownloadInfo, error) {
	if cookie != "source-private" {
		f.targetReads++
		if f.direct {
			return &p115.DownloadInfo{URL: "https://cdn.115.com/direct"}, nil
		}
		return nil, p115.ErrShareNotReadable
	}
	f.sourceReads++
	return &p115.DownloadInfo{Name: "movie.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
}

func (f *shareHintFake) DownloadInstantInfoWithUA(ctx context.Context, share *p115.ShareInfo, id, cookie, ua string) (*p115.DownloadInfo, error) {
	return f.DownloadInfoWithUA(ctx, share, id, cookie, ua)
}

func TestShareSHA1HintFallbackTrustAndTargetIsolation(t *testing.T) {
	for _, mode := range []string{"direct", "verified", "old-format", "unknown", "ambiguous", "unverified", "origin-conflict"} {
		t.Run(mode, func(t *testing.T) {
			s, fast, first := setupFastCache(t)
			ctx := context.Background()
			if _, _, err := s.ResolvePickCodeWithReport(ctx, first, "pickaaa", "source-private", "player", "primary"); err != nil {
				t.Fatal(err)
			}
			f := &shareHintFake{fastCacheFake: fast, direct: mode == "direct"}
			s.client = f
			target := *first
			target.binding.AccountID = "second"
			target.binding.ParentCID = "456"
			target.uid, target.cookie = "789", "UID=789_A; SEID=second"
			f.files = nil
			share := &p115.ShareInfo{Code: "new-share", Password: "pass"}
			origin := DirectLinkKey("share-material", share.Code, share.Password, "123")
			hash := strings.Repeat("A", 40)
			switch mode {
			case "old-format":
				hash = ""
			case "unknown":
				hash = strings.Repeat("B", 40)
			case "ambiguous":
				m := s.Status().Materials[0]
				m.Size++
				m.ID = playbackMaterialID(m.SHA1, m.Size)
				m.Origins = nil
				if err := s.putMaterial(m); err != nil {
					t.Fatal(err)
				}
			case "unverified":
				m := s.Status().Materials[0]
				if err := s.ForgetMaterial(ctx, m.ID); err != nil {
					t.Fatal(err)
				}
				m.VerifiedAt = time.Time{}
				m.Origins = nil
				if err := s.putMaterial(m); err != nil {
					t.Fatal(err)
				}
			case "origin-conflict":
				m := s.Status().Materials[0]
				m.SHA1 = strings.Repeat("B", 40)
				m.ID = playbackMaterialID(m.SHA1, m.Size)
				m.Origins = []string{origin}
				if err := s.putMaterial(m); err != nil {
					t.Fatal(err)
				}
			}
			_, report, err := s.ResolveWithReport(WithPlaybackSHA1Hint(ctx, hash), &target, share, "123", "source-private", "player")
			if err != nil {
				t.Fatal(err)
			}
			wantReads := 1
			if mode == "direct" || mode == "verified" {
				wantReads = 0
			}
			if f.targetReads != 1 || f.sourceReads != wantReads || report.UsedSource != (wantReads == 1) {
				t.Fatalf("wrong account order: target=%d source=%d used=%v", f.targetReads, f.sourceReads, report.UsedSource)
			}
			if mode == "direct" {
				if f.uploads != 1 {
					t.Fatal("direct share caused upload")
				}
				return
			}
			if f.uploads != 2 {
				t.Fatal("target copy missing")
			}
			entries := s.Status().Entries
			found := false
			for _, e := range entries {
				if e.UID == target.uid {
					found = true
					if e.AccountID != "second" || e.ParentCID != "456" || e.File.SHA1 != strings.Repeat("A", 40) {
						t.Fatal("hint changed destination or file")
					}
				}
			}
			if !found {
				t.Fatal("target copy not isolated")
			}
			if mode == "verified" {
				if m, _ := s.lookupMaterial(origin, ""); m.ID != "" {
					t.Fatal("untrusted hint became source association")
				}
			}
		})
	}
}
