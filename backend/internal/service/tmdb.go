package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"
	"sync"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/storage"
)

type TMDBSource interface {
	SearchPeople(context.Context, string, int) (*tmdb.PersonPage, error)
	PersonDetails(context.Context, int) (*tmdb.PersonDetail, error)
	CollectionDetails(context.Context, int) (*tmdb.CollectionDetail, error)
	Genres(context.Context, string) ([]tmdb.Genre, error)
	Browse(context.Context, tmdb.Options) (*tmdb.Page, error)
	Details(context.Context, string, int) (*tmdb.Detail, error)
	Season(context.Context, int, int) (*tmdb.SeasonDetail, error)
	Recommendations(context.Context, string, int) (*tmdb.Page, error)
}
type TMDBCacheStore interface {
	TMDBCache(context.Context, string) ([]byte, int64, error)
	SaveTMDBCache(context.Context, string, []byte, int64) error
	TMDBSchedules(context.Context, int) ([]storage.TMDBCacheRecord, error)
}
type tmdbFlight struct {
	done    chan struct{}
	data    []byte
	fetched int64
	err     error
}
type tmdbPrewarmKey struct{}
type TMDBService struct {
	source  TMDBSource
	db      TMDBCacheStore
	mu      sync.Mutex
	flights map[string]*tmdbFlight
	slots   chan struct{}
	now     func() time.Time
}

func NewTMDBService(source TMDBSource, db TMDBCacheStore) *TMDBService {
	// 此通道明确作为四个网络请求名额，限制跨页面和预热任务的总并发。
	return &TMDBService{source: source, db: db, flights: make(map[string]*tmdbFlight), slots: make(chan struct{}, 4), now: time.Now}
}
func (s *TMDBService) Genres(ctx context.Context, kind string) ([]tmdb.Genre, error) {
	return s.source.Genres(ctx, kind)
}
func (s *TMDBService) Browse(ctx context.Context, o tmdb.Options) (*tmdb.Page, error) {
	return s.source.Browse(ctx, o)
}
func (s *TMDBService) CollectionDetails(ctx context.Context, id int) (*tmdb.CollectionDetail, error) {
	return s.source.CollectionDetails(ctx, id)
}
func (s *TMDBService) SearchPeople(ctx context.Context, query string, page int) (*tmdb.PersonPage, error) {
	return s.source.SearchPeople(ctx, query, page)
}
func (s *TMDBService) PersonDetails(ctx context.Context, id int) (*tmdb.PersonDetail, error) {
	return s.source.PersonDetails(ctx, id)
}
func (s *TMDBService) Recommendations(ctx context.Context, kind string, id int) (*tmdb.Page, error) {
	return s.source.Recommendations(ctx, kind, id)
}

func encodeTMDB(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("编码 TMDB 缓存: %w", err)
	}
	var output bytes.Buffer
	w := gzip.NewWriter(&output)
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("压缩 TMDB 缓存: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("结束 TMDB 压缩: %w", err)
	}
	return output.Bytes(), nil
}
func decodeTMDB[T any](data []byte) (*T, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("打开 TMDB 压缩缓存: %w", err)
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, (8<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("读取 TMDB 压缩缓存: %w", err)
	}
	if len(raw) > 8<<20 {
		return nil, fmt.Errorf("TMDB 缓存超过大小限制")
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解析 TMDB 缓存: %w", err)
	}
	return &result, nil
}

// 请求合并不创建游离协程；首个请求负责加载，其他请求可独立取消等待。
func (s *TMDBService) fetch(ctx context.Context, key string, observed int64, load func(context.Context) (any, error)) ([]byte, int64, error) {
	s.mu.Lock()
	if flight, ok := s.flights[key]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-flight.done:
			return flight.data, flight.fetched, flight.err
		}
	}
	flight := &tmdbFlight{done: make(chan struct{})}
	s.flights[key] = flight
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.flights, key); close(flight.done); s.mu.Unlock() }()
	// 读取缓存与登记请求之间，另一个请求可能刚完成写入。
	latest, fetched, err := s.db.TMDBCache(ctx, key)
	if err != nil {
		flight.err = err
		return nil, 0, err
	}
	if len(latest) > 0 && fetched != observed {
		flight.data = latest
		flight.fetched = fetched
		return latest, fetched, nil
	}
	if warm, _ := ctx.Value(tmdbPrewarmKey{}).(bool); warm {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			flight.err = ctx.Err()
			return nil, 0, flight.err
		case <-timer.C:
		}
	}
	select {
	case <-ctx.Done():
		flight.err = ctx.Err()
		return nil, 0, flight.err
	case s.slots <- struct{}{}:
	}
	defer func() { <-s.slots }()
	requestCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	value, err := load(tmdb.WithFresh(requestCtx))
	if err == nil {
		flight.data, err = encodeTMDB(value)
	}
	if err == nil {
		flight.fetched = s.now().Unix()
		err = s.db.SaveTMDBCache(requestCtx, key, flight.data, flight.fetched)
	}
	flight.err = err
	return flight.data, flight.fetched, flight.err
}
func cachedTMDB[T any](ctx context.Context, s *TMDBService, key string, ttl time.Duration, load func(context.Context) (any, error)) (*T, tmdb.CacheInfo, error) {
	data, fetched, err := s.db.TMDBCache(ctx, key)
	if err != nil {
		return nil, tmdb.CacheInfo{}, err
	}
	warm, _ := ctx.Value(tmdbPrewarmKey{}).(bool)
	var cached *T
	if len(data) > 0 {
		cached, _ = decodeTMDB[T](data)
	}
	if schedule, ok := any(cached).(*tmdb.Schedule); ok && schedule != nil {
		ttl = scheduleTTL(schedule, s.now(), ttl)
	}
	stale := fetched == 0 || s.now().Sub(time.Unix(fetched, 0)) >= ttl
	if cached != nil && !tmdb.WantsFresh(ctx) && (!warm || !stale) {
		return cached, tmdb.CacheInfo{FetchedAt: fetched, CacheStale: stale}, nil
	}
	data, fetched, err = s.fetch(ctx, key, fetched, load)
	if err != nil {
		return nil, tmdb.CacheInfo{}, err
	}
	result, err := decodeTMDB[T](data)
	return result, tmdb.CacheInfo{FetchedAt: fetched}, err
}

// 很久以前播完的季降低刷新频率；日期待定和近期播出仍按小时检查。
func scheduleTTL(value *tmdb.Schedule, now time.Time, fallback time.Duration) time.Duration {
	if len(value.Episodes) == 0 {
		return fallback
	}
	for _, episode := range value.Episodes {
		date, err := time.Parse("2006-01-02", episode.Date)
		if err != nil || !date.Before(now.AddDate(0, 0, -30)) {
			return fallback
		}
	}
	return 7 * 24 * time.Hour
}
func (s *TMDBService) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	if (kind != "tv" && kind != "movie") || id < 1 {
		return nil, tmdb.ErrInput
	}
	// v2 includes IMDb identifiers used by independent resource-search fallbacks.
	// Keeping the version in the key makes legacy detail records expire naturally.
	value, info, err := cachedTMDB[tmdb.Detail](ctx, s, "detail:v2:"+kind+":"+strconv.Itoa(id), 6*time.Hour, func(ctx context.Context) (any, error) { return s.source.Details(ctx, kind, id) })
	if err != nil {
		return nil, err
	}
	value.CacheInfo = info
	return value, nil
}
func (s *TMDBService) Season(ctx context.Context, id, number int) (*tmdb.SeasonDetail, error) {
	if id < 1 || number < 0 || number > 10000 {
		return nil, tmdb.ErrInput
	}
	value, info, err := cachedTMDB[tmdb.SeasonDetail](ctx, s, "season:"+strconv.Itoa(id)+":"+strconv.Itoa(number), time.Hour, func(ctx context.Context) (any, error) { return s.source.Season(ctx, id, number) })
	if err != nil {
		return nil, err
	}
	value.CacheInfo = info
	return value, nil
}
func (s *TMDBService) Schedule(ctx context.Context, id, number int) (*tmdb.Schedule, error) {
	if id < 1 || number < 0 || number > 10000 {
		return nil, tmdb.ErrInput
	}
	value, info, err := cachedTMDB[tmdb.Schedule](ctx, s, "schedule:"+strconv.Itoa(id)+":"+strconv.Itoa(number), time.Hour, func(ctx context.Context) (any, error) {
		full, err := s.source.Season(ctx, id, number)
		if err != nil {
			return nil, err
		}
		result := &tmdb.Schedule{Number: number, Episodes: make([]tmdb.ScheduledEpisode, 0, len(full.Episodes))}
		for _, episode := range full.Episodes {
			result.Episodes = append(result.Episodes, tmdb.ScheduledEpisode{Number: episode.Number, Date: episode.Date})
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	value.CacheInfo = info
	return value, nil
}

// 一次返回整部剧已有的精简缓存；冷缓存立即返回空数组，不等待外网。
func (s *TMDBService) Schedules(ctx context.Context, id int) ([]tmdb.Schedule, error) {
	if id < 1 {
		return nil, tmdb.ErrInput
	}
	records, err := s.db.TMDBSchedules(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]tmdb.Schedule, 0, len(records))
	for _, r := range records {
		value, err := decodeTMDB[tmdb.Schedule](r.Data)
		if err != nil {
			continue
		}
		value.CacheInfo = tmdb.CacheInfo{FetchedAt: r.FetchedAt, CacheStale: s.now().Sub(time.Unix(r.FetchedAt, 0)) >= scheduleTTL(value, s.now(), time.Hour)}
		result = append(result, *value)
	}
	return result, nil
}

// 全局预热仅枚举本地已入库剧集，分批补充播出缓存，不扫描 Emby 媒体文件。
func (s *TMDBService) Run(ctx context.Context, list func(context.Context) ([]int, error)) error {
	warmCtx := context.WithValue(ctx, tmdbPrewarmKey{}, true)
	cursor := 0
	for ctx.Err() == nil {
		ids, err := list(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("读取 TMDB 预热范围失败: %v", err)
		}
		if cursor >= len(ids) {
			cursor = 0
		}
		end := min(cursor+32, len(ids))
		failures := 0
		for _, id := range ids[cursor:end] {
			catalog, _, err := cachedTMDB[[]tmdb.SeasonSummary](warmCtx, s, "catalog:"+strconv.Itoa(id), 6*time.Hour, func(ctx context.Context) (any, error) {
				detail, err := s.source.Details(ctx, "tv", id)
				if err != nil {
					return nil, err
				}
				return detail.Seasons, nil
			})
			if err != nil {
				failures++
				continue
			}
			for _, season := range *catalog {
				if ctx.Err() != nil {
					return nil
				}
				if _, err := s.Schedule(warmCtx, id, season.Number); err != nil {
					failures++
				}
			}
		}
		cursor = end
		if failures > 0 && ctx.Err() == nil {
			log.Printf("TMDB 播出缓存预热：%d 项暂未更新，保留已有缓存", failures)
		}
		delay := 10 * time.Second
		if len(ids) == 0 || cursor == len(ids) {
			delay = 5 * time.Minute
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
