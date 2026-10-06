package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type play115RangeFake struct {
	*materialFake
	fused   int
	wrongID bool
}

func (f *play115RangeFake) PlaybackPickCodeRangeSource(context.Context, string, string, string) (*p115.DownloadInfo, error) {
	f.fused++
	id := "123"
	if f.wrongID {
		id = "456"
	}
	return &p115.DownloadInfo{FileID: id, Name: "source.mkv", SHA1: strings.Repeat("A", 40), Size: 100, URL: "https://cdn.115.com/source"}, nil
}

func TestPlay115DeclaredUploadAndChallenge(t *testing.T) {
	for _, mode := range []string{"direct", "challenge", "source-missing", "wrong-id", "rejected", "completion-failed", "known-conflict"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			f := &play115RangeFake{materialFake: &materialFake{fastCacheFake: base}}
			s.client = f
			ctx := WithPlay115Source(context.Background(), Play115Source{FileID: "123", SHA1: strings.Repeat("A", 40), Size: 100, Name: "declared.mkv"})
			cookie := ""
			if mode == "challenge" || mode == "wrong-id" || mode == "source-missing" {
				f.challenge = true
				cookie = "source"
			}
			if mode == "source-missing" {
				cookie = ""
			}
			if mode == "wrong-id" {
				f.wrongID = true
			}
			if mode == "rejected" {
				base.uploadErr = errors.New("rejected")
			}
			if mode == "completion-failed" {
				base.completeErr = true
			}
			if mode == "known-conflict" {
				m := PlaybackMaterial{ID: playbackMaterialID(strings.Repeat("B", 40), 200), SHA1: strings.Repeat("B", 40), Size: 200, Name: "old.mkv", Origins: []string{DirectLinkKey("pickcode-material", "primary", "pickaaa")}}
				if err := s.putMaterial(m); err != nil {
					t.Fatal(err)
				}
			}
			_, report, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", cookie, "player", "primary")
			failed := mode == "wrong-id" || mode == "source-missing" || mode == "rejected" || mode == "completion-failed"
			if (err != nil) != failed {
				t.Fatal(mode, err)
			}
			if base.reads != 0 || f.donorReads != 0 {
				t.Fatal("eager source metadata query")
			}
			if failed {
				if len(s.Status().Materials) != 0 {
					t.Fatal("failed declaration became trusted material")
				}
				return
			}
			if len(base.files) != 1 || base.files[0].Name != "declared.mkv" || base.files[0].SHA1 != strings.Repeat("A", 40) || base.files[0].Size != 100 {
				t.Fatal("declaration replaced by source")
			}
			m, ok := s.materialByID(playbackMaterialID(strings.Repeat("A", 40), 100))
			if !ok || m.VerifiedAt.IsZero() || len(m.Origins) != 0 {
				t.Fatal("missing verified material or poisoned origin")
			}
			if mode == "challenge" {
				if f.fused != 1 || f.rangeReads != 1 || len(m.Ranges) != 1 || !report.UsedSource {
					t.Fatal("challenge did not use fused lazy source")
				}
				// A second UID reuses accepted ranges without source credentials.
				target := *selected
				target.uid = "33"
				target.cookie = "UID=33_A"
				target.binding.AccountID = "other"
				if _, _, err := s.ResolvePickCodeWithReport(ctx, &target, "pickaaa", "", "player", "primary"); err != nil {
					t.Fatal(err)
				}
				if f.fused != 1 || f.rangeReads != 1 {
					t.Fatal("accepted range not reused")
				}
			} else if report.UsedSource || f.fused != 0 {
				t.Fatal("direct upload read source")
			}
		})
	}
}

func TestPlay115TargetHitAndChangedSize(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &fusedPlaybackFake{fastCacheFake: base}
	s.client = f
	declared := Play115Source{FileID: "999", SHA1: strings.Repeat("A", 40), Size: 100, Name: "declared.mkv"}
	base.files = []p115.DrivePlaybackFile{{ID: "123", CID: "0", PickCode: "pick123", SHA1: declared.SHA1, Size: 100, Name: "target.mkv"}}
	ctx := WithPlay115Source(context.Background(), declared)
	if _, _, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	if f.candidates != 1 || f.verified != 1 || base.reads != 0 || base.uploads != 0 {
		t.Fatal("wrong target-hit request budget")
	}
	declared.Size = 101
	if _, _, err := s.ResolvePickCodeWithReport(WithPlay115Source(context.Background(), declared), selected, "pickaaa", "", "player", "primary"); err == nil {
		t.Fatal("changed size reused old source mapping")
	}
	if f.verified != 1 || base.uploads != 0 {
		t.Fatal("conflicting target used or uploaded")
	}
}

func TestPlay115UncertainUploadConfirmedMaterial(t *testing.T) {
	s, base, selected := setupFastCache(t)
	base.uploadErr = p115.ErrInstantUncertain
	f := &confirmationFake{fastCacheFake: base, visibleAt: 2, accountCookie: selected.cookie}
	s.client = f
	ctx := WithPlay115Source(context.Background(), Play115Source{FileID: "123", SHA1: strings.Repeat("A", 40), Size: 100, Name: "declared.mkv"})
	if _, _, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	materials := s.Status().Materials
	if len(materials) != 1 || materials[0].VerifiedAt.IsZero() || len(materials[0].Origins) != 0 || len(materials[0].Ranges) != 0 || base.reads != 0 || base.uploads != 1 {
		t.Fatal("confirmation lost material or replayed write")
	}
}
