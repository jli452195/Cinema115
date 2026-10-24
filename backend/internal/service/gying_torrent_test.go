package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/gying"
)

func gyingOrganizeFixture(t *testing.T, body []byte, source DownloadResourceInput) (*GyingService, DownloadResourceInput, *atomic.Int32) {
	t.Helper()
	hash, _ := btresource.MagnetHash(source.Magnet)
	source.Source, source.Provider, source.ProviderID, source.TorrentURL = "gying", "tv", "Show", ""
	reads := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/dbt/Torrent/") {
			t.Error("cached ticket was not reused", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		reads.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "gying.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewGyingService(store)
	if err != nil {
		t.Fatal(err)
	}
	s.client, err = gying.New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	s.doc = gyingDocument{Version: 1, BaseURL: server.URL, Password: "test-password"}
	s.cacheSearchLocked("tv:1", gying.SearchResponse{Items: []gying.SearchItem{{ID: "tv:Show", Links: []gying.ResourceLink{{HasTorrent: true, TorrentPath: "/dbt/Torrent/" + strings.Repeat("c", 44), URL: "magnet:?xt=urn:btih:" + hash}}}}}, time.Now())
	return s, source, reads
}

func TestGyingTorrentCacheConcurrentIdentityAndConfiguration(t *testing.T) {
	body, source := mikanOrganizeTorrent(t)
	s, source, reads := gyingOrganizeFixture(t, body, source)
	hash, _ := btresource.MagnetHash(source.Magnet)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := s.ReadTorrent(context.Background(), "tv", "Show", hash); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if reads.Load() != 1 {
		t.Fatal("duplicate torrent reads", reads.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ReadTorrent(ctx, "tv", "Show", hash); !errors.Is(err, context.Canceled) || reads.Load() != 1 {
		t.Fatal("canceled cache hit was accepted", err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if s.torrents != nil {
		t.Fatal("configuration clear retained torrent session/cache")
	}
	if _, err := s.ReadTorrent(context.Background(), "tv", "Show", hash); !errors.Is(err, gying.ErrNotConfigured) || reads.Load() != 1 {
		t.Fatal("cleared site still usable", err)
	}
}

func TestGyingTorrentInvalidResponsesDoNotPoisonCache(t *testing.T) {
	valid, source := mikanOrganizeTorrent(t)
	hash, _ := btresource.MagnetHash(source.Magnet)
	for _, body := range [][]byte{[]byte("<html>expired</html>"), valid} {
		s, _, _ := gyingOrganizeFixture(t, body, source)
		expected := hash
		if string(body) == string(valid) {
			expected = strings.Repeat("b", 40)
			entry := s.searchCache["tv:1"]
			entry.result.Items[0].Links[0].URL = "magnet:?xt=urn:btih:" + expected
			s.searchCache["tv:1"] = entry
		}
		if _, err := s.ReadTorrent(context.Background(), "tv", "Show", expected); !errors.Is(err, ErrOrganizeInput) || len(s.torrents.cache) != 0 {
			t.Fatal("error page or mismatched torrent cached", err)
		}
	}
}
