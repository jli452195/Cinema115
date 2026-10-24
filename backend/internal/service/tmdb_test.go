package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/storage"
)

type cacheTMDBFake struct {
	calls       atomic.Int32
	detailCalls atomic.Int32
	mu          sync.Mutex
	episodes    []tmdb.Episode
	failure     bool
	entered     chan struct{}
	release     chan struct{}
}

type englishTMDBFake struct {
	cacheTMDBFake
	englishCalls atomic.Int32
	englishError error
}

func (f *englishTMDBFake) EnglishTitle(_ context.Context, kind string, _ int) (string, error) {
	f.englishCalls.Add(1)
	if f.englishError != nil {
		return "", f.englishError
	}
	if kind == "movie" {
		return "Movie", nil
	}
	return "", nil
}

func TestEnglishTitleCachePersistsIncludingMissingTranslation(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := &englishTMDBFake{}
	for _, kind := range []string{"movie", "tv"} {
		want := ""
		if kind == "movie" {
			want = "Movie"
		}
		for i := 0; i < 3; i++ {
			// New service instances must reuse the same disk cache, even empty.
			title, err := NewTMDBService(source, db).EnglishTitle(ctx, kind, 42)
			if err != nil || title != want {
				t.Fatal(title, err)
			}
		}
	}
	if source.englishCalls.Load() != 2 {
		t.Fatalf("expected one lookup per work: %d", source.englishCalls.Load())
	}
	s := NewTMDBService(source, db)
	s.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if title, err := s.EnglishTitle(ctx, "movie", 42); err != nil || title != "Movie" || source.englishCalls.Load() != 3 {
		t.Fatal("expired title was not refreshed", title, err)
	}
	source.englishError = errors.New("upstream unavailable")
	s.now = func() time.Time { return time.Now().Add(62 * 24 * time.Hour) }
	if title, err := s.EnglishTitle(ctx, "movie", 42); err != nil || title != "Movie" || source.englishCalls.Load() != 4 {
		t.Fatal("refresh failure discarded a known title", title, err)
	}
}

func (f *cacheTMDBFake) Genres(context.Context, string) ([]tmdb.Genre, error)     { return nil, nil }
func (f *cacheTMDBFake) Browse(context.Context, tmdb.Options) (*tmdb.Page, error) { return nil, nil }
func (f *cacheTMDBFake) SearchPeople(context.Context, string, int) (*tmdb.PersonPage, error) {
	return &tmdb.PersonPage{Items: []tmdb.Person{}}, nil
}
func (f *cacheTMDBFake) PersonDetails(context.Context, int) (*tmdb.PersonDetail, error) {
	return &tmdb.PersonDetail{Items: []tmdb.Item{}}, nil
}
func (f *cacheTMDBFake) CollectionDetails(context.Context, int) (*tmdb.CollectionDetail, error) {
	return nil, nil
}
func (f *cacheTMDBFake) Recommendations(context.Context, string, int) (*tmdb.Page, error) {
	return nil, nil
}
func (f *cacheTMDBFake) Details(context.Context, string, int) (*tmdb.Detail, error) {
	f.detailCalls.Add(1)
	return &tmdb.Detail{IMDBID: "tt0807675", Seasons: []tmdb.SeasonSummary{{Number: 1}}}, nil
}

func TestTMDBDetailV2IgnoresLegacyCacheWithoutIMDb(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy, err := encodeTMDB(&tmdb.Detail{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveTMDBCache(ctx, "detail:tv:34793", legacy, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	source := &cacheTMDBFake{}
	service := NewTMDBService(source, db)
	detail, err := service.Details(ctx, "tv", 34793)
	if err != nil {
		t.Fatal(err)
	}
	if detail.IMDBID != "tt0807675" || source.detailCalls.Load() != 1 {
		t.Fatalf("v2 详情没有跳过旧缓存: detail=%+v calls=%d", detail, source.detailCalls.Load())
	}
	data, _, err := db.TMDBCache(ctx, "detail:v2:tv:34793")
	if err != nil || len(data) == 0 {
		t.Fatalf("v2 详情缓存未保存: bytes=%d err=%v", len(data), err)
	}
}
func (f *cacheTMDBFake) Season(ctx context.Context, id, number int) (*tmdb.SeasonDetail, error) {
	f.calls.Add(1)
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.release:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure {
		return nil, errors.New("模拟 TMDB 连接失败")
	}
	return &tmdb.SeasonDetail{ID: id, Number: number, Episodes: append([]tmdb.Episode{}, f.episodes...)}, nil
}
func TestTMDBCachePersistsAndRefreshReplacesSchedule(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "media.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	source := &cacheTMDBFake{episodes: []tmdb.Episode{{Number: 1, Date: "2026-09-14"}, {Number: 2, Date: "2026-09-15"}, {Number: 3, Date: ""}}}
	s := NewTMDBService(source, db)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	first, err := s.Schedule(ctx, 1396, 1)
	if err != nil || len(first.Episodes) != 3 {
		t.Fatal(first, err)
	}
	data, _, err := db.TMDBCache(ctx, "schedule:1396:1")
	if err != nil || len(data) == 0 {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = NewTMDBService(source, db)
	s.now = func() time.Time { return now }
	if _, err := s.Schedule(ctx, 1396, 1); err != nil || source.calls.Load() != 1 {
		t.Fatal("重启后应命中持久化缓存", err)
	}
	now = now.Add(2 * time.Hour)
	old, err := s.Schedule(ctx, 1396, 1)
	if err != nil || !old.CacheStale || source.calls.Load() != 1 {
		t.Fatal("过期缓存应立即返回", old, err)
	}
	source.failure = true
	if _, err := s.Schedule(tmdb.WithFresh(ctx), 1396, 1); err == nil {
		t.Fatal("刷新失败未返回错误")
	}
	retained, _ := s.Schedule(ctx, 1396, 1)
	if retained.FetchedAt != first.FetchedAt || len(retained.Episodes) != 3 {
		t.Fatal("失败不能覆盖缓存或延长有效期")
	}
	source.failure = false
	source.episodes = []tmdb.Episode{{Number: 1, Date: "2026-09-14"}, {Number: 2, Date: "2026-10-01"}}
	updated, err := s.Schedule(tmdb.WithFresh(ctx), 1396, 1)
	if err != nil || updated.CacheStale || len(updated.Episodes) != 2 || updated.Episodes[1].Date != "2026-10-01" {
		t.Fatal("必须替换删除、改期后的完整播出表", updated, err)
	}
	batch, err := s.Schedules(ctx, 1396)
	if err != nil || len(batch) != 1 || len(batch[0].Episodes) != 2 {
		t.Fatal(batch, err)
	}
	if empty, err := s.Schedules(ctx, 999); err != nil || len(empty) != 0 {
		t.Fatal("冷缓存批量读取不能访问外网", err)
	}
}
func TestTMDBCacheCoalescesAndCancelsWaiter(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := &cacheTMDBFake{entered: make(chan struct{}, 1), release: make(chan struct{}), episodes: []tmdb.Episode{{Number: 1}}}
	s := NewTMDBService(source, db)
	var wg sync.WaitGroup
	leader := make(chan error, 1)
	wg.Add(1)
	go func() { defer wg.Done(); _, err := s.Schedule(context.Background(), 1, 1); leader <- err }()
	<-source.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Schedule(ctx, 1, 1); err == nil {
		t.Fatal("取消的等待不能继续请求")
	}
	const count = 8
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Schedule(context.Background(), 1, 1)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	close(source.release)
	wg.Wait()
	if err := <-leader; err != nil {
		t.Fatal(err)
	}
	if source.calls.Load() != 1 {
		t.Fatalf("同一缓存键应合并请求，实际 %d", source.calls.Load())
	}
}
