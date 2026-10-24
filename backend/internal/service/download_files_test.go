package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/releaseparse"
)

func TestDownloadFileSpecJSONContract(t *testing.T) {
	// Resource specs are flattened in the API consumed by the shared picker.
	const raw = `{"resolution":"1080p","languages":["zh-Hans","zh-Hant"],"subtitleMode":"soft","codec":"h265","container":"mkv","bitDepth":"10","fansub":"Group"}`
	var got DownloadFileSpec
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	want := DownloadFileSpec{Spec: releaseparse.Spec{Resolution: "1080p", Languages: []string{"zh-Hans", "zh-Hant"}, SubtitleMode: "soft", Codec: "h265", Container: "mkv", BitDepth: "10"}, Fansub: "Group"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resource specs did not decode into the shared type: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil || string(encoded) != raw {
		t.Fatalf("resource spec JSON changed: %s, %v", encoded, err)
	}
}

func mikanOrganizeTorrent(t *testing.T) ([]byte, DownloadResourceInput) {
	t.Helper()
	info := map[string]any{"name": "show", "piece length": int64(1 << 20), "pieces": strings.Repeat("p", 20), "files": []any{
		map[string]any{"length": int64(1200), "path": []string{"[Group] Show [02].mkv"}},
		map[string]any{"length": int64(1200), "path": []string{"[Group] Show [01].mkv"}},
		map[string]any{"length": int64(40), "path": []string{"[Group] Show [01].zh-CN.ass"}},
		map[string]any{"length": int64(80), "path": []string{"disc.iso"}},
	}}
	rawInfo, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	body, err := bencode.Marshal(map[string]any{"info": bencode.Bytes(rawInfo)})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha1.Sum(rawInfo)
	// A post filename is deliberately different from the real info hash.
	post := strings.Repeat("a", 40)
	return body, DownloadResourceInput{Source: "mikan", ProviderID: post, TorrentURL: "https://mikanani.me/Download/20261024/" + post + ".torrent", Magnet: "magnet:?xt=urn:btih:" + hex.EncodeToString(hash[:])}
}

func TestTorrentOrganizeFilesAndWorkerReuse(t *testing.T) {
	for _, site := range []string{"mikan", "gying"} {
		t.Run(site, func(t *testing.T) { testTorrentOrganizeFilesAndWorkerReuse(t, site) })
	}
}

func testTorrentOrganizeFilesAndWorkerReuse(t *testing.T, site string) {
	body, source := mikanOrganizeTorrent(t)
	s, drive, adder, tracker, _, in := gardenFixture(t)
	reads := 0
	readCount := func() int { return reads }
	s.Resources = NewDownloadResources(nil, NewMikanService(nil, nil), animeClaimTorrentFunc(func(_ context.Context, provider, post, url string) ([]byte, error) {
		reads++
		if provider != "mikan" || post != source.ProviderID || url != source.TorrentURL {
			t.Error("wrong torrent source")
		}
		return body, nil
	}))
	if site == "gying" {
		var gyingSite *GyingService
		var gyingReads *atomic.Int32
		gyingSite, source, gyingReads = gyingOrganizeFixture(t, body, source)
		s.Resources.(*DownloadResources).Gying = gyingSite
		readCount = func() int { return int(gyingReads.Load()) }
	}
	files, err := s.Resources.Files(context.Background(), source)
	if err != nil || len(files.Files) != 2 || len(files.Subtitles) != 1 || files.Files[0].Episode != 1 || files.Files[0].Size != 1200 || files.Files[0].ID != "show/[Group] Show [01].mkv" || files.Subtitles[0].VideoID != files.Files[0].ID {
		t.Fatal("torrent paths, sizes, ordering or subtitles lost", files, err)
	}
	if drive.creates != 0 || drive.renames != 0 || drive.moves != 0 || len(drive.listCalls) != 0 || adder.url != "" {
		t.Fatal("reading the manifest accessed 115")
	}
	in.DownloadResourceInput = source
	in.Files[0].ID = files.Files[0].ID
	// Mutating a response must not corrupt the cached metadata used by preview.
	files.Files[0].ID = "tampered.mkv"
	plan, err := s.Preview(context.Background(), in)
	if err != nil || len(plan.Items) != 2 || readCount() != 1 || !strings.HasSuffix(source.Magnet, plan.Download.Hash) {
		t.Fatal("preview did not reuse verified metadata", plan, readCount(), err)
	}
	job, err := s.Start(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := s.live[job.ID]
	tracker.resultID = "801"
	tracker.beforeRead = func() {
		drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
		drive.dirs["801"] = []p115.ShareFile{
			{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200},
			{ID: "902", Name: "[Group] Show [01].zh-CN.ass", Size: 40},
			{ID: "903", Name: "[Group] Show [02].mkv", Size: 1200},
		}
	}
	if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" {
		t.Fatal("shared worker did not complete torrent organization", p.job, done, err)
	}
	if readCount() != 1 || tracker.calls != 1 || adder.url != source.Magnet || len(drive.dirs["801"]) != 1 || drive.dirs["801"][0].ID != "903" {
		t.Fatal("duplicate reads/submission or unselected file moved", readCount(), tracker.calls, adder.url, drive.dirs)
	}
	if _, err := s.Start(plan.ID); err != nil || readCount() != 1 {
		t.Fatal("idempotent retry failed", err)
	}
}

func TestMikanOrganizeRejectsUntrustedSourcesAndCachedHashConflicts(t *testing.T) {
	body, source := mikanOrganizeTorrent(t)
	reads := 0
	s := NewDownloadResources(nil, NewMikanService(nil, nil), animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		reads++
		return body, nil
	}))
	for _, change := range []func(*DownloadResourceInput){
		func(in *DownloadResourceInput) { in.Source = "unknown" },
		func(in *DownloadResourceInput) { in.Source = "" },
		func(in *DownloadResourceInput) { in.Provider = "dmhy" },
		func(in *DownloadResourceInput) { in.TorrentURL = "http://127.0.0.1/a.torrent" },
		func(in *DownloadResourceInput) { in.TorrentURL += "?token=secret" },
		func(in *DownloadResourceInput) { in.ProviderID = strings.Repeat("b", 40) },
		func(in *DownloadResourceInput) { in.Magnet = "ed2k://invalid" },
	} {
		in := source
		change(&in)
		if _, err := s.Files(context.Background(), in); !errors.Is(err, ErrOrganizeInput) || reads != 0 {
			t.Fatal("untrusted source caused a read", reads, err)
		}
	}
	if _, err := s.Files(context.Background(), source); err != nil || reads != 1 {
		t.Fatal(err, reads)
	}
	conflict := source
	conflict.Magnet = "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)
	if _, err := s.Files(context.Background(), conflict); !errors.Is(err, ErrOrganizeInput) || reads != 1 {
		t.Fatal("cache bypassed per-caller identity validation", err, reads)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Files(ctx, source); !errors.Is(err, context.Canceled) || reads != 1 {
		t.Fatal("canceled request read metadata", err, reads)
	}
}

func TestMikanOrganizeConcurrentReadersShareOneTorrent(t *testing.T) {
	body, source := mikanOrganizeTorrent(t)
	var reads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	s := NewDownloadResources(nil, NewMikanService(nil, nil), animeClaimTorrentFunc(func(ctx context.Context, _, _, _ string) ([]byte, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return body, nil
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := s.Files(ctx, source)
			errors <- err
		}()
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 1 {
		t.Fatal("duplicate torrent requests", reads.Load())
	}
}

func TestMikanOrganizeMalformedTorrentIsNotCached(t *testing.T) {
	body, source := mikanOrganizeTorrent(t)
	reads := 0
	s := NewDownloadResources(nil, NewMikanService(nil, nil), animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		reads++
		if reads == 1 {
			return []byte("<html>captcha</html>"), nil
		}
		return body, nil
	}))
	s.mikan.spacing = 0
	if _, err := s.Files(context.Background(), source); !errors.Is(err, ErrOrganizeInput) {
		t.Fatal("malformed torrent accepted", err)
	}
	if _, err := s.Files(context.Background(), source); err != nil || reads != 2 {
		t.Fatal("failed metadata poisoned cache", reads, err)
	}
}
