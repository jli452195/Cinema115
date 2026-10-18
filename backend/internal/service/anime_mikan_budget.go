package service

import (
	"context"
	"sync"
	"time"

	"strm115/backend/internal/client/mikan"
)

// AnimeMikanBudget belongs to one generate/continue operation, shared by its
// metadata reads. Seed it with the current batch's raw counts, not deduplicated
// release counts. P1-06 will obtain those counts from trusted stored previews.
type AnimeMikanBudget struct {
	gate      chan struct{}
	mu        sync.Mutex
	deadline  time.Time
	requests  int
	readBytes int64
	loaded    map[mikan.Source]int
	source    mikan.Source
}

func (b *AnimeMikanBudget) bindSource(source mikan.Source) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.source.Valid() && b.source != source {
		return mikan.ErrInput
	}
	b.source = source
	return nil
}

type AnimeMikanBudgetStats struct {
	Requests      int   `json:"requests"`
	ReadBytes     int64 `json:"readBytes"`
	TotalRawCount int   `json:"totalRawCount"`
	Limit         int   `json:"limit"`
	Remaining     int   `json:"remaining"`
}

func NewAnimeMikanBudget(loaded map[mikan.Source]int) (*AnimeMikanBudget, error) {
	if len(loaded) > 5 {
		return nil, mikan.ErrInput
	}
	b := &AnimeMikanBudget{gate: make(chan struct{}, 1), deadline: time.Now().Add(45 * time.Second), loaded: make(map[mikan.Source]int, len(loaded))}
	total := 0
	for source, count := range loaded {
		if !source.Valid() || count < 0 || count > mikan.AnimeMaxRows {
			return nil, mikan.ErrInput
		}
		total += count
		if total > mikan.AnimeMaxRows {
			return nil, &mikan.LimitError{Kind: "rows"}
		}
		b.loaded[source] = count
	}
	return b, nil
}

func (b *AnimeMikanBudget) Stats() AnimeMikanBudgetStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	stats := AnimeMikanBudgetStats{Requests: b.requests, ReadBytes: b.readBytes, Limit: mikan.AnimeMaxRows}
	for _, count := range b.loaded {
		stats.TotalRawCount += count
	}
	stats.Remaining = stats.Limit - stats.TotalRawCount
	return stats
}

func (b *AnimeMikanBudget) allowance(source mikan.Source) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := mikan.AnimeMaxRows
	for key, count := range b.loaded {
		if key != source {
			remaining -= count
		}
	}
	return remaining
}

func (b *AnimeMikanBudget) startRequest() (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.requests >= 3 {
		return 0, &mikan.LimitError{Kind: "requests"}
	}
	if b.readBytes >= 8<<20 {
		return 0, &mikan.LimitError{Kind: "bytes"}
	}
	b.requests++
	return min(int64(mikan.AnimeMaxBodyBytes), (8<<20)-b.readBytes), nil
}

func (b *AnimeMikanBudget) addBytes(n int64) {
	b.mu.Lock()
	b.readBytes += n
	b.mu.Unlock()
}

func (b *AnimeMikanBudget) replaceCount(source mikan.Source, count int) {
	b.mu.Lock()
	b.loaded[source] = count
	b.mu.Unlock()
}

func (b *AnimeMikanBudget) acquire(ctx context.Context) error {
	select {
	case b.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
