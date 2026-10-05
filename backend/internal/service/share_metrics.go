package service

import (
	"context"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type reshareMetricsKey struct{}
type reshareScanPhaseKey struct{}

// Measurements are local to one serial workflow. No credentials or file names
// enter the persisted record, and no background measurement goroutine is used.
type reshareMetrics struct {
	stats *model.ReshareStats
	stage string
	since time.Time
}

func withReshareMetrics(ctx context.Context) context.Context {
	return context.WithValue(ctx, reshareMetricsKey{}, &reshareMetrics{
		stats: &model.ReshareStats{StageMS: make(map[string]int64), Directories: make(map[string]*model.ReshareDirectoryStats)},
		stage: "source", since: time.Now(),
	})
}

func reshareMetricsFrom(ctx context.Context) *reshareMetrics {
	m, _ := ctx.Value(reshareMetricsKey{}).(*reshareMetrics)
	return m
}

func (m *reshareMetrics) advance(stage string) {
	if m == nil {
		return
	}
	now := time.Now()
	m.stats.StageMS[m.stage] += now.Sub(m.since).Milliseconds()
	m.stage, m.since = stage, now
}

func directoryMetrics(ctx context.Context, phase, cid string) *model.ReshareDirectoryStats {
	m := reshareMetricsFrom(ctx)
	if m == nil {
		return nil
	}
	key := phase + "/" + cid
	d := m.stats.Directories[key]
	if d == nil {
		d = &model.ReshareDirectoryStats{Phase: phase, CID: cid}
		m.stats.Directories[key] = d
	}
	return d
}

func measuredPages(ctx context.Context, phase string, list transferPageLister) transferPageLister {
	return func(callCtx context.Context, cid string, offset int) (*p115.SharePage, error) {
		started := time.Now()
		page, err := list(callCtx, cid, offset)
		if d := directoryMetrics(ctx, phase, cid); d != nil {
			d.Requests++
			if offset == 0 {
				d.FirstPages++
			}
			d.RequestMS += time.Since(started).Milliseconds()
		}
		return page, err
	}
}

func waitTransfer(ctx context.Context, delay time.Duration) error {
	started := time.Now()
	defer func() {
		if m := reshareMetricsFrom(ctx); m != nil {
			m.stats.WaitMS += time.Since(started).Milliseconds()
		}
	}()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
