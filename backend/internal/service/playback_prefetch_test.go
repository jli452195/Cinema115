package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type prefetchRoundTrip func(*http.Request) (*http.Response, error)

func (f prefetchRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type searchPrefetchFake struct {
	*fastCacheFake
	source  *p115.ShareClient
	started <-chan struct{}
	mode    string
	cancel  context.CancelFunc
}

func (f *searchPrefetchFake) PlaybackPickCodeSource(ctx context.Context, account, pc, cookie string) (*p115.DownloadInfo, error) {
	return f.source.PlaybackPickCodeSource(ctx, account, pc, cookie)
}

func (f *searchPrefetchFake) FindPlaybackFileBySHA1(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.started:
	}
	switch f.mode {
	case "search-error":
		return nil, errors.New("search failed")
	case "cancel":
		f.cancel()
		return nil, ctx.Err()
	case "hit":
		file := p115.DrivePlaybackFile{ID: "123", PickCode: "pick123", Size: 100, SHA1: hash}
		f.files = append(f.files, file)
		return &file, nil
	default:
		return nil, nil
	}
}

func (f *searchPrefetchFake) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, scope, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	if info.PrefetchRangeSource != nil {
		return nil, errors.New("prefetch hook was not consumed")
	}
	return f.fastCacheFake.UploadPlayback(ctx, info, scope, cid, cookie, ticket, progress)
}

func TestSourcePrefetchOverlapsTargetSearchAndJoinsOnExit(t *testing.T) {
	for _, mode := range []string{"hit", "miss", "search-error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			started, exited := make(chan struct{}), make(chan struct{})
			source := p115.NewShareClient()
			source.HTTP.Transport = prefetchRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Cookie") != "UID=11_A" {
					t.Error("wrong source account")
				}
				if r.URL.Path == "/files/supervision" {
					body := fmt.Sprintf(`{"state":true,"data":{"file_id":"12","file_name":"v.mkv","file_size":"100","file_sha1":"%s"}}`, strings.Repeat("A", 40))
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				}
				if r.URL.Path != "/app/chrome/downurl" {
					t.Error("unexpected speculative request")
				}
				close(started)
				defer close(exited)
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			s.client = &searchPrefetchFake{fastCacheFake: base, source: source, started: started, mode: mode, cancel: cancel}
			_, _, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "UID=11_A", "player", "source")
			if (err != nil) != (mode == "search-error" || mode == "cancel") {
				t.Fatal(mode, err)
			}
			wantUploads := 0
			if mode == "miss" {
				wantUploads = 1
			}
			if base.uploads != wantUploads {
				t.Fatal("unexpected upload", base.uploads)
			}
			select {
			case <-exited:
			default:
				t.Fatal("prefetch outlived playback")
			}
		})
	}
}

func TestPlaybackPrefetchOnlyForFreshMaterialWithoutRangesOrCopies(t *testing.T) {
	for _, mode := range []string{"fresh", "saved-material", "saved-range", "known-copy"} {
		t.Run(mode, func(t *testing.T) {
			s, _, selected := setupPlaybackCache(t)
			hash := strings.Repeat("A", 40)
			origin := DirectLinkKey("origin")
			material := PlaybackMaterial{ID: playbackMaterialID(hash, 100), Name: "movie.mkv", Size: 100, SHA1: hash, VerifiedAt: time.Now()}
			if mode == "saved-material" {
				material.Origins = []string{origin}
			}
			if mode == "saved-range" {
				material.Ranges = []PlaybackRange{{Start: 1, End: 3, SHA1: hash, VerifiedAt: time.Now()}}
			}
			if mode != "fresh" {
				if err := s.putMaterial(material); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "known-copy" {
				if err := s.put(PlaybackCacheEntry{Key: DirectLinkKey("copy"), AccountID: "donor", Status: "ready", File: p115.DrivePlaybackFile{SHA1: hash, Size: 100, ID: "123", PickCode: "abc123"}}); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, origin)
			info, _, _, err := s.preparePlaybackMaterial(ctx, selected, &PlaybackResolution{}, func() (*p115.DownloadInfo, error) {
				reads++
				return &p115.DownloadInfo{Name: "movie.mkv", SHA1: hash, Size: 100, URL: "https://cdn.115.com/private"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if (info.PrefetchRangeSource != nil) != (mode == "fresh") {
				t.Fatal("wrong speculation policy", mode)
			}
			if mode == "saved-material" && reads != 0 {
				t.Fatal("read primary despite cached material")
			}
		})
	}
}
