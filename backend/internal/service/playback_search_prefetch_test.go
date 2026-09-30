package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type quickSearchFake struct {
	*fastCacheFake
	quick    func(context.Context, string, string) (*p115.DrivePlaybackFile, error)
	queries  []string
	ordinary int
}

func (f *quickSearchFake) FindPlaybackFileBySHA1Quick(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.queries = append(f.queries, hash)
	return f.quick(ctx, hash, cookie)
}
func (f *quickSearchFake) FindPlaybackFileBySHA1(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.ordinary++
	return f.fastCacheFake.FindPlaybackFileBySHA1(ctx, hash, cookie)
}

func TestQuickPlaybackFirstSearchAndReuse(t *testing.T) {
	for _, mode := range []string{"miss", "hit", "error"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			f := &quickSearchFake{fastCacheFake: base}
			f.quick = func(_ context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
				if cookie != selected.cookie {
					t.Error("target credentials changed")
				}
				if mode == "error" {
					return nil, errors.New("upstream failed")
				}
				if mode == "hit" {
					file := p115.DrivePlaybackFile{ID: "123", CID: "0", PickCode: "pick123", Size: 100, SHA1: hash}
					base.files = append(base.files, file)
					return &file, nil
				}
				return nil, nil
			}
			s.client = f
			for i := 0; i < 2; i++ {
				_, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player")
				if (err != nil) != (mode == "error") {
					t.Fatal(mode, err)
				}
			}
			want := 0
			if mode == "miss" {
				want = 1
			}
			if f.uploads != want || f.prepares != want || len(f.queries) != 1 || f.ordinary != 0 {
				t.Fatal("wrong lookup/upload count", f.uploads, f.prepares, len(f.queries), f.ordinary)
			}
		})
	}
}

func TestQuickPlaybackPendingUsesOrdinarySearch(t *testing.T) {
	for _, mode := range []string{"hit", "miss", "error"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			f := &quickSearchFake{fastCacheFake: base, quick: func(context.Context, string, string) (*p115.DrivePlaybackFile, error) {
				t.Error("quick search used for uncertain write")
				return nil, nil
			}}
			s.client = f
			hash := strings.Repeat("A", 40)
			old := PlaybackCacheEntry{Key: DirectLinkKey("old"), UID: selected.uid, AccountID: selected.binding.AccountID, Status: "uncertain", File: p115.DrivePlaybackFile{SHA1: hash, Size: 100}, RetryNotBefore: time.Now().Add(time.Minute)}
			if err := s.put(old); err != nil {
				t.Fatal(err)
			}
			if mode == "hit" {
				base.files = []p115.DrivePlaybackFile{{ID: "123", CID: "0", PickCode: "pick123", Size: 100, SHA1: hash}}
			}
			if mode == "error" {
				base.searchErr = errors.New("search failed")
			}
			ctx := context.WithValue(context.Background(), playbackSHA1Hint{}, hash)
			_, _, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "source", "player")
			if (err == nil) != (mode == "hit") || f.ordinary != 1 || len(f.queries) != 0 || f.uploads != 0 {
				t.Fatal("pending write was replayed", mode, err)
			}
		})
	}
}

func TestQuickPlaybackSpeculationValidatesSourceAndJoins(t *testing.T) {
	for _, mode := range []string{"match", "wrong-hit", "wrong-miss", "source-error", "cancel", "query-error"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			hint, actual := strings.Repeat("A", 40), strings.Repeat("A", 40)
			if strings.HasPrefix(mode, "wrong-") {
				actual = strings.Repeat("B", 40)
			}
			ctx = context.WithValue(ctx, playbackSHA1Hint{}, hint)
			started, finished := make(chan struct{}), make(chan struct{})
			f := &quickSearchFake{fastCacheFake: base}
			f.quick = func(queryCtx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
				if cookie != selected.cookie {
					t.Error("wrong target cookie")
				}
				if hash != hint {
					return nil, nil
				}
				close(started)
				defer close(finished)
				if mode == "source-error" || mode == "cancel" {
					<-queryCtx.Done()
					return nil, queryCtx.Err()
				}
				if mode == "query-error" {
					return nil, errors.New("query failed")
				}
				if mode == "wrong-hit" {
					return &p115.DrivePlaybackFile{ID: "123", CID: "0", PickCode: "pick123", SHA1: hint, Size: 100}, nil
				}
				return nil, nil
			}
			s.client = f
			load := func() (*p115.DownloadInfo, error) {
				select {
				case <-started:
				case <-ctx.Done():
					return nil, errors.New("query did not overlap source metadata")
				}
				if mode == "source-error" {
					return nil, errors.New("source unavailable")
				}
				if mode == "cancel" {
					cancel()
					return nil, ctx.Err()
				}
				// The speculative miss is complete, but no write is allowed until
				// this function supplies verified source metadata.
				<-finished
				if base.uploads != 0 {
					t.Error("uploaded before validating source")
				}
				return &p115.DownloadInfo{Name: "verified.mkv", Size: 100, SHA1: actual, URL: "https://cdn.115.com/source"}, nil
			}
			_, err := s.resolveInstant(ctx, selected, "source", "player", &PlaybackResolution{}, load)
			failed := mode == "source-error" || mode == "cancel" || mode == "query-error"
			if (err != nil) != failed {
				t.Fatal(mode, err)
			}
			select {
			case <-finished:
			default:
				t.Fatal("query goroutine outlived request")
			}
			wantUploads := 1
			if failed {
				wantUploads = 0
			}
			wantQueries := 1
			if actual != hint {
				wantQueries = 2
			}
			if base.uploads != wantUploads || f.ordinary != 0 || len(f.queries) != wantQueries {
				t.Fatal("wrong query/write count", base.uploads, len(f.queries), f.ordinary)
			}
			if !failed {
				if len(base.files) != 1 || base.files[0].SHA1 != actual || base.files[0].CID != selected.binding.ParentCID {
					t.Fatal("untrusted hint changed uploaded file or destination")
				}
				if len(s.Status().Materials) != 1 || s.Status().Materials[0].SHA1 != actual {
					t.Fatal("hint became trusted material")
				}
			}
		})
	}
}
