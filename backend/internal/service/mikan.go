package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/mikan"
)

type MikanReader interface {
	Search(context.Context, string, string) (mikan.Result, error)
}

var mikanLogSensitive = regexp.MustCompile(`(?i)(?:https?://\S+|(?:cookie|password|token|secret|api[_-]?key)\s*[=:].*)`)

type MikanInput struct {
	Query    string `json:"query"`
	Fallback string `json:"fallback"`
	Group    string `json:"group"`
	Refresh  bool   `json:"refresh"`
}
type MikanAttempt struct {
	Query  string `json:"query"`
	Count  int    `json:"count"`
	Cached bool   `json:"cached"`
}
type MikanResult struct {
	mikan.Result
	Query     string         `json:"query"`
	Group     string         `json:"group"`
	Attempts  []MikanAttempt `json:"attempts"`
	Cached    bool           `json:"cached"`
	FetchedAt time.Time      `json:"fetchedAt"`
	ExpiresAt time.Time      `json:"expiresAt"`
}
type mikanCache struct {
	result           mikan.Result
	fetched, expires time.Time
}
type mikanFlight struct {
	done  chan struct{}
	entry mikanCache
	err   error
}
type MikanService struct {
	Client      MikanReader
	Logs        *GenerationLogs
	mu          sync.Mutex
	cache       map[string]mikanCache
	flights     map[string]*mikanFlight
	gate        chan struct{}
	lastRequest time.Time
}

func NewMikanService(client MikanReader, logs *GenerationLogs) *MikanService {
	return &MikanService{Client: client, Logs: logs, cache: make(map[string]mikanCache, 16), flights: make(map[string]*mikanFlight), gate: make(chan struct{}, 1)}
}
func cloneMikan(result mikan.Result) mikan.Result {
	result.Items = append([]mikan.Item{}, result.Items...)
	result.Groups = append([]mikan.Group{}, result.Groups...)
	result.Bangumis = append([]mikan.Bangumi{}, result.Bangumis...)
	result.More = append([]mikan.Expansion{}, result.More...)
	if result.Bangumi != nil {
		copy := *result.Bangumi
		result.Bangumi = &copy
	}
	return result
}

func (s *MikanService) Search(ctx context.Context, input MikanInput) (output MikanResult, err error) {
	started := time.Now()
	task := ""
	if s.Logs != nil {
		s.Logs.mu.Lock()
		s.Logs.taskSequence++
		task = fmt.Sprintf("MIKAN-%06d", s.Logs.taskSequence)
		s.Logs.mu.Unlock()
	}
	log := func(level, message string) { s.Logs.Append("蜜柑", task, "详情页搜索", level, message) }
	log("INFO", "开始蜜柑搜索")
	defer func() {
		if err != nil {
			level := "ERROR"
			if errors.Is(err, context.Canceled) || errors.Is(err, mikan.ErrInput) {
				level = "WARNING"
			}
			log(level, fmt.Sprintf("蜜柑搜索未完成：%s，耗时 %.2f 秒", err.Error(), time.Since(started).Seconds()))
		} else {
			log("INFO", fmt.Sprintf("蜜柑搜索完成，%d 条资源，耗时 %.2f 秒", len(output.Items), time.Since(started).Seconds()))
		}
	}()
	query, err := mikan.NormalizeQuery(input.Query)
	if err != nil || !mikan.ValidGroup(input.Group) {
		return output, mikan.ErrInput
	}
	fallback := strings.TrimSpace(input.Fallback)
	if fallback != "" {
		if _, err := mikan.NormalizeQuery(fallback); err != nil {
			return output, err
		}
	}
	queries := []string{query}
	if fallback != "" && fallback != query && input.Group == "" {
		queries = append(queries, fallback)
	}
	output.Attempts = make([]MikanAttempt, 0, len(queries))
	cards := make([]mikan.Bangumi, 0, 8)
	seenCards := make(map[string]bool, 8)
	for i, q := range queries {
		if i > 0 {
			log("DEBUG", "标题搜索无结果，使用原名兜底")
		}
		// 搜索词可能由用户输入，先脱敏后再写日志。
		safe := mikanLogSensitive.ReplaceAllString(q, "[敏感内容已隐藏]")
		log("INFO", "搜索关键词："+safe)
		if input.Refresh {
			log("DEBUG", "强制刷新，跳过已有缓存")
		}
		entry, cached, readErr := s.search(ctx, q, input.Group, input.Refresh)
		if readErr != nil {
			return output, readErr
		}
		log("DEBUG", fmt.Sprintf("本轮 %d 条资源；缓存有效至 %s", len(entry.result.Items), entry.expires.Format(time.RFC3339)))
		if cached {
			log("DEBUG", "从缓存或同一正在执行的请求返回，未重复访问蜜柑")
		}
		output.Attempts = append(output.Attempts, MikanAttempt{q, len(entry.result.Items), cached})
		output.Result = cloneMikan(entry.result)
		// 原名兜底不能覆盖中文搜索已找到的番剧入口。
		for _, card := range entry.result.Bangumis {
			if !seenCards[card.ID] {
				cards = append(cards, card)
				seenCards[card.ID] = true
			}
		}
		output.Bangumis = append([]mikan.Bangumi{}, cards...)
		output.Query, output.Group, output.Cached = q, input.Group, cached
		output.FetchedAt, output.ExpiresAt = entry.fetched, entry.expires
		if len(entry.result.Items) > 0 {
			break
		}
	}
	return output, nil
}

// 首个调用者同步执行请求，其余调用者等待结果；不创建脱离请求生命周期的后台协程。
func (s *MikanService) search(ctx context.Context, query, group string, refresh bool) (mikanCache, bool, error) {
	key := query + "\x00" + group
	return s.cachedRead(ctx, key, refresh, func(ctx context.Context) (mikan.Result, error) { return s.Client.Search(ctx, query, group) })
}

func (s *MikanService) cachedRead(ctx context.Context, key string, refresh bool, read func(context.Context) (mikan.Result, error)) (mikanCache, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return mikanCache{}, false, err
		}
		s.mu.Lock()
		if entry, ok := s.cache[key]; ok && !refresh && time.Now().Before(entry.expires) {
			s.mu.Unlock()
			return entry, true, nil
		}
		if flight, ok := s.flights[key]; ok {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return mikanCache{}, false, ctx.Err()
			case <-flight.done:
				// 首个请求取消不影响其他仍在等待的页面，存活请求可以重新发起。
				if errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
					continue
				}
				return flight.entry, true, flight.err
			}
		}
		if len(s.flights) >= 32 {
			s.mu.Unlock()
			return mikanCache{}, false, errors.New("蜜柑搜索繁忙，请稍后重试")
		}
		flight := &mikanFlight{done: make(chan struct{})}
		s.flights[key] = flight
		s.mu.Unlock()
		result, err := s.fetch(ctx, read)
		entry := mikanCache{result: result, fetched: time.Now()}
		ttl := 10 * time.Minute
		if len(result.Items) == 0 && len(result.Bangumis) == 0 {
			ttl = time.Minute
		}
		entry.expires = entry.fetched.Add(ttl)
		s.mu.Lock()
		if err == nil {
			for k, v := range s.cache {
				if !time.Now().Before(v.expires) {
					delete(s.cache, k)
				}
			}
			if len(s.cache) >= 16 {
				oldestKey := ""
				var oldest time.Time
				for k, v := range s.cache {
					if oldest.IsZero() || v.fetched.Before(oldest) {
						oldestKey, oldest = k, v.fetched
					}
				}
				delete(s.cache, oldestKey)
			}
			s.cache[key] = entry
		}
		flight.entry, flight.err = entry, err
		delete(s.flights, key)
		close(flight.done)
		s.mu.Unlock()
		return entry, false, err
	}
}

func (s *MikanService) fetch(ctx context.Context, read func(context.Context) (mikan.Result, error)) (mikan.Result, error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return mikan.Result{}, ctx.Err()
	}
	defer func() { <-s.gate }()
	// 同时只访问一次蜜柑，相邻请求至少间隔一秒，降低站点压力。
	if delay := time.Until(s.lastRequest.Add(time.Second)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return mikan.Result{}, ctx.Err()
		}
	}
	s.lastRequest = time.Now()
	return read(ctx)
}
