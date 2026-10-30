package service

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/utils/torrentmeta"
)

func gardenTorrentFixture(t *testing.T, body []byte) (*GardenService, *gardenDetailFake, DownloadResourceInput) {
	t.Helper()
	meta, err := torrentmeta.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	in := DownloadResourceInput{Source: "garden", Provider: "dmhy", ProviderID: "688777", Magnet: "magnet:?xt=urn:btih:" + meta.InfoHash}
	f := &gardenDetailFake{hash: meta.InfoHash, incomplete: true, names: []string{"truncated/E99.mkv"}, links: []garden.DetailLink{
		{URL: in.Magnet},
		{URL: "https://dl.dmhy.org/2023/01/21/" + meta.InfoHash + ".torrent"},
	}}
	s := NewGardenService(f)
	s.spacing, s.torrents.spacing = 0, 0
	return s, f, in
}

func TestGardenTorrentFallbackAndCompleteListFastPath(t *testing.T) {
	body, _ := mikanOrganizeTorrent(t)
	for _, mode := range []string{"partial", "empty", "mikan", "complete", "no link", "unsupported link", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			s, detail, in := gardenTorrentFixture(t, body)
			switch mode {
			case "empty":
				detail.incomplete, detail.names = false, nil
			case "mikan":
				detail.provider, detail.post = "mikan", strings.Repeat("a", 40)
				in.Provider, in.ProviderID = detail.provider, detail.post
				detail.links[1].URL = "https://mikanani.me/Download/20261024/" + detail.post + ".torrent"
			case "complete":
				detail.incomplete = false
			case "no link":
				detail.links = nil
			case "unsupported link":
				detail.links[1].URL = "https://animes.garden/example.torrent"
			case "deleted":
				detail.deleted = true
			}
			reads := 0
			s.torrents.Client = gardenTorrentTransport{reader: animeClaimTorrentFunc(func(_ context.Context, provider, hash, endpoint string) ([]byte, error) {
				reads++
				wantPost := detail.hash
				if mode == "mikan" {
					wantPost = detail.post
				}
				if provider != in.Provider || hash != wantPost || endpoint != detail.links[1].URL {
					t.Error("wrong original torrent source")
				}
				return body, nil
			})}
			files, err := s.Files(context.Background(), in)
			if err != nil || len(detail.paths) != 1 {
				t.Fatal("detail read failed", err, detail.paths)
			}
			switch mode {
			case "partial", "empty", "mikan":
				if reads != 1 || len(files.Files) != 2 || len(files.Subtitles) != 1 || files.Files[0].ID != "show/[Group] Show [01].mkv" || files.Files[0].Size != 1200 || files.Subtitles[0].VideoID != files.Files[0].ID {
					t.Fatal("full torrent manifest, root, sizes or subtitles lost", reads, files)
				}
			case "complete":
				if reads != 0 || len(files.Files) != 1 || files.Files[0].ID != detail.names[0] {
					t.Fatal("complete list caused a torrent read", reads, files)
				}
			default:
				if reads != 0 || len(files.Files) != 0 || files.Reason == "" {
					t.Fatal("partial list accepted without full metadata", reads, files)
				}
			}
		})
	}
}

func TestGardenTorrentFailureCanRetry(t *testing.T) {
	body, _ := mikanOrganizeTorrent(t)
	for _, mode := range []string{"unavailable", "malformed", "hash conflict"} {
		t.Run(mode, func(t *testing.T) {
			s, _, in := gardenTorrentFixture(t, body)
			reads := 0
			s.torrents.Client = gardenTorrentTransport{reader: animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
				reads++
				if reads == 1 {
					switch mode {
					case "unavailable":
						return nil, &animefeed.HTTPError{Site: "dmhy", Status: 404}
					case "malformed":
						return []byte("<html>unavailable</html>"), nil
					default:
						return []byte(strings.ReplaceAll(string(body), "Show", "Film")), nil
					}
				}
				return body, nil
			})}
			if files, err := s.Files(context.Background(), in); err == nil || len(files.Files) != 0 || len(s.torrents.cache) != 0 {
				t.Fatal("failed metadata was accepted or cached", files, err)
			}
			if files, err := s.Files(context.Background(), in); err != nil || len(files.Files) != 2 || reads != 2 {
				t.Fatal("failed metadata prevented retry", files, reads, err)
			}
		})
	}
}

func TestGardenTorrentConcurrentReadersShareHashCache(t *testing.T) {
	body, _ := mikanOrganizeTorrent(t)
	s, detail, in := gardenTorrentFixture(t, body)
	var reads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	s.torrents.Client = gardenTorrentTransport{reader: animeClaimTorrentFunc(func(ctx context.Context, _, _, _ string) ([]byte, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return body, nil
		}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	decoded, err := hex.DecodeString(detail.hash)
	if err != nil {
		t.Fatal(err)
	}
	alias := in
	alias.Magnet = "magnet:?xt=urn:btih:" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded)
	var workers sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		input := in
		if i%2 == 0 {
			input = alias
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			files, err := s.Files(ctx, input)
			if err == nil && len(files.Files) != 2 {
				err = errors.New("incomplete result")
			}
			errs <- err
		}()
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Error(ctx.Err())
	}
	close(release)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A different provider for the same hash still uses the verified origin.
	alias.Provider, alias.ProviderID = "mikan", strings.Repeat("a", 40)
	if _, err := s.Files(ctx, alias); err != nil || reads.Load() != 1 || len(detail.paths) != 1 || len(s.cache) != 1 {
		t.Fatal("hash aliases caused duplicate reads", reads.Load(), detail.paths, err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := s.Files(canceled, in); !errors.Is(err, context.Canceled) || reads.Load() != 1 {
		t.Fatal("cached result ignored cancellation", err)
	}
}

func TestGardenTorrentReadsPastOneThousandNonMediaFiles(t *testing.T) {
	files := make([]any, 0, 1203)
	for i := 0; i < 1200; i++ {
		files = append(files, map[string]any{"length": int64(1), "path": []string{"scans", fmt.Sprintf("%04d.jpg", i)}})
	}
	for _, name := range []string{"Show.S01E01.mkv", "Show.S01E02.mkv", "Show.S01E02.zh-CN.ass"} {
		files = append(files, map[string]any{"length": int64(1), "path": []string{name}})
	}
	body, err := bencode.Marshal(map[string]any{"info": map[string]any{"name": "show", "files": files, "piece length": int64(1 << 20), "pieces": strings.Repeat("p", 20)}})
	if err != nil {
		t.Fatal(err)
	}
	s, _, in := gardenTorrentFixture(t, body)
	s.torrents.Client = gardenTorrentTransport{reader: animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) { return body, nil })}
	out, err := s.Files(context.Background(), in)
	if err != nil || len(out.manifest) != 1203 || len(out.Files) != 2 || len(out.Subtitles) != 1 || out.Files[1].Episode != 2 {
		t.Fatal("manifest was truncated before media filtering", len(out.manifest), out, err)
	}
}
