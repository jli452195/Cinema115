package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

func TestRedirect115TargetFirst(t *testing.T) {
	for _, mode := range []string{"hit", "prepare", "miss", "miss-other-sha1", "source-unavailable", "query-error", "identity-error", "wrong-uid", "known-conflict", "ambiguous", "pending"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			f := &fusedPlaybackFake{fastCacheFake: base}
			s.client = f
			hash := strings.Repeat("A", 40)
			file := p115.DrivePlaybackFile{ID: "123", CID: "0", PickCode: "pick123", Size: 100, SHA1: hash, Name: "target.mkv"}
			base.files = []p115.DrivePlaybackFile{file}
			sourceCookie := "source"
			ctx := WithRedirect115SHA1(context.Background(), strings.ToLower(hash))
			switch mode {
			case "hit":
				sourceCookie = "" // No primary credential needed for a target hit.
			case "prepare":
				ctx = PreparePlaybackFileContext(ctx)
			case "miss", "miss-other-sha1", "source-unavailable":
				base.files = nil
				if mode == "miss-other-sha1" {
					ctx = WithRedirect115SHA1(ctx, strings.Repeat("B", 40))
				}
				if mode == "source-unavailable" {
					sourceCookie = ""
				}
			case "query-error":
				base.searchErr = p115.ErrShareLoginRequired
			case "identity-error":
				f.verifyErr = p115.ErrDriveInvalidResponse
			case "wrong-uid":
				selected.uid = "999"
			case "known-conflict", "ambiguous":
				count := 2
				if mode == "known-conflict" {
					count = 1
				}
				for i := 0; i < count; i++ {
					m := PlaybackMaterial{SHA1: hash, Size: int64(200 + i), Name: "known.mkv", VerifiedAt: time.Now().UTC()}
					if mode == "known-conflict" {
						m.SHA1 = strings.Repeat("B", 40)
						m.Origins = []string{DirectLinkKey("pickcode-material", "primary", "pickaaa")}
					}
					m.ID = playbackMaterialID(m.SHA1, m.Size)
					if err := s.putMaterial(m); err != nil {
						t.Fatal(err)
					}
				}
			case "pending":
				if err := s.put(PlaybackCacheEntry{Key: DirectLinkKey("pending"), UID: selected.uid, AccountID: selected.binding.AccountID, Status: "uncertain", File: file}); err != nil {
					t.Fatal(err)
				}
			}
			link, report, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", sourceCookie, "player", "primary")
			failed := mode == "source-unavailable" || mode == "query-error" || mode == "identity-error" || mode == "wrong-uid"
			if (err != nil) != failed {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
			if failed {
				if base.uploads != 0 || len(s.Status().Materials) != 0 || len(s.Status().Entries) != 0 {
					t.Fatal("failed lookup persisted a candidate or uploaded")
				}
				if base.reads != 0 {
					t.Fatal("failure queried primary")
				}
				return
			}
			if mode == "hit" || mode == "prepare" {
				if base.reads != 0 || base.uploads != 0 || report.UsedSource {
					t.Fatal("target hit read primary or uploaded")
				}
				if mode == "hit" && (link == "" || f.candidates != 1 || f.verified != 1 || f.metadata != 0) {
					t.Fatal("wrong target hit request budget")
				}
				if mode == "prepare" && (link != "" || f.candidates != 0 || f.verified != 0 || f.metadata != 1) {
					t.Fatal("preparation fetched a URL")
				}
				materials := s.Status().Materials
				if len(materials) != 1 || materials[0].Name != file.Name || len(materials[0].Origins) != 0 || materials[0].VerifiedAt.IsZero() {
					t.Fatal("external origin persisted or target material missing")
				}
				// A new STRM PickCode reuses the verified data, never the primary.
				if _, _, err := s.ResolvePickCodeWithReport(ctx, selected, "otherpc", "", "player", "primary"); err != nil {
					t.Fatal(err)
				}
				if f.candidates+f.metadata != 1 || base.reads != 0 {
					t.Fatal("repeated playback searched again")
				}
			} else if base.reads != 1 {
				t.Fatal("source fallback missing")
			}
			if mode == "miss" && (f.candidates != 1 || base.uploads != 1) {
				t.Fatal("miss queried twice or failed to upload")
			}
			if mode == "miss-other-sha1" && (f.candidates != 2 || base.uploads != 1 || base.files[0].SHA1 != hash) {
				t.Fatal("different source SHA1 reused an unrelated miss")
			}
			if mode == "pending" && f.candidates != 0 {
				t.Fatal("uncertain write used quick lookup")
			}
		})
	}
}

func TestRedirect115TargetSQLiteRestart(t *testing.T) {
	ctx := WithRedirect115SHA1(context.Background(), strings.Repeat("A", 40))
	old, base, selected := setupFastCache(t)
	base.files = []p115.DrivePlaybackFile{{ID: "123", CID: "0", PickCode: "pick123", Size: 100, SHA1: strings.Repeat("A", 40), Name: "target.mkv"}}
	f := &fusedPlaybackFake{fastCacheFake: base}
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "playback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cache, err := NewSQLitePlaybackCache(ctx, db, old.store.(*SessionStore), old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	cache.Close()
	restarted, err := NewSQLitePlaybackCache(ctx, db, old.store.(*SessionStore), old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, _, err := restarted.ResolvePickCodeWithReport(ctx, selected, "otherpc", "", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	state := restarted.Status()
	if f.candidates != 1 || f.verified != 2 || base.reads != 0 || base.uploads != 0 || len(state.Materials) != 1 || len(state.Entries) != 1 || len(state.Materials[0].Origins) != 0 {
		t.Fatal("restart lost target verification or created external origin")
	}
}

type cancelRedirectSearch struct{ *fusedPlaybackFake }

func (f *cancelRedirectSearch) FindPlaybackCandidateBySHA1(ctx context.Context, _, _ string) (*p115.DrivePlaybackFile, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRedirect115TargetCancellation(t *testing.T) {
	s, base, selected := setupFastCache(t)
	s.client = &cancelRedirectSearch{&fusedPlaybackFake{fastCacheFake: base}}
	ctx, cancel := context.WithTimeout(WithRedirect115SHA1(context.Background(), strings.Repeat("A", 40)), 20*time.Millisecond)
	defer cancel()
	_, _, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "source", "player", "primary")
	if !errors.Is(err, context.DeadlineExceeded) || base.reads != 0 || base.uploads != 0 {
		t.Fatal("cancellation did not stop lookup", err)
	}
}
