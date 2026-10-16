package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/model"
)

func TestEmbyPostersUsePublishedIDsCacheAndScope(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	var mu sync.Mutex
	calls := make(map[string]int)
	image := []byte("\x89PNG\r\n\x1a\nsmall-poster")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		if r.Header.Get("X-Emby-Token") != "test-api-key" || r.URL.Query().Get("api_key") != "" {
			t.Error("poster credentials must stay in the upstream header")
		}
		q := r.URL.Query()
		if q.Get("maxWidth") != "160" || q.Get("maxHeight") != "240" || q.Get("quality") != "75" || q.Get("format") != "webp" {
			t.Error("poster request must be a bounded thumbnail", q)
		}
		switch r.URL.Path {
		case "/emby/Items/missing/Images/Primary":
			w.WriteHeader(http.StatusNotFound)
		case "/emby/Items/html/Images/Primary":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("<html>not an image</html>"))
		case "/emby/Items/redirect/Images/Primary":
			http.Redirect(w, r, "/should-not-follow", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(image)
		}
	}))
	defer upstream.Close()
	f.service.mu.Lock()
	f.service.config.URL = upstream.URL
	f.service.mu.Unlock()
	scope := embyScope(f.service.snapshot())
	if err := f.db.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	rows := make([]model.EmbyItem, 0, 6)
	for _, id := range []string{"series", "parallel", "missing", "html", "redirect"} {
		rows = append(rows, model.EmbyItem{ID: id, Type: "Series"})
	}
	rows = append(rows, model.EmbyItem{ID: "episode", Type: "Episode", SeriesID: "series"})
	if err := f.db.EmbyStage(ctx, scope, "published", rows); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyPublish(ctx, scope, "published", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyStage(ctx, scope, "staging", []model.EmbyItem{{ID: "staged", Type: "Series"}}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cache, err := imagecache.NewEmby(root, f.service.PosterRequest)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Posters = cache
	read := func(id string) error {
		asset, err := f.service.OpenPoster(ctx, scope, id)
		if err != nil {
			return err
		}
		defer asset.File.Close()
		data, err := io.ReadAll(asset.File)
		if err != nil {
			return err
		}
		if string(data) != string(image) || asset.ETag == "" || asset.ContentType != "image/png" {
			t.Error("poster cache returned invalid content", asset)
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		if err := read("series"); err != nil {
			t.Fatal(err)
		}
	}
	// A new cache instance must reuse persisted bytes without contacting Emby.
	f.service.Posters, err = imagecache.NewEmby(root, f.service.PosterRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := read("series"); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errorsSeen := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); errorsSeen <- read("parallel") }()
	}
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := read("missing"); !errors.Is(err, imagecache.ErrNotFound) {
			t.Fatal("missing posters must be negatively cached", err)
		}
		for _, id := range []string{"html", "redirect"} {
			if err := read(id); err == nil {
				t.Fatal("invalid content or redirect was accepted", id)
			}
		}
	}
	for _, id := range []string{"staged", "episode", "unknown"} {
		if err := read(id); !errors.Is(err, imagecache.ErrNotFound) {
			t.Fatal("poster outside published series accepted", id, err)
		}
	}
	if err := read("../series"); !errors.Is(err, imagecache.ErrInput) {
		t.Fatal("unsafe ID accepted", err)
	}
	f.service.mu.Lock()
	f.service.config.LibraryIDs = []string{"changed"}
	f.service.mu.Unlock()
	if err := read("series"); !errors.Is(err, imagecache.ErrNotFound) {
		t.Fatal("old scope reused cached poster", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 5 {
		t.Fatal("unexpected metadata, staging or redirect requests", calls)
	}
	for path, count := range calls {
		if count != 1 {
			t.Fatal("poster fetched more than once", path, count)
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if strings.Contains(string(data), "test-api-key") {
			t.Error("credentials persisted in image cache")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
