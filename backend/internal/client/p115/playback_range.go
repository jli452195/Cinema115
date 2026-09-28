package p115

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PlaybackRangeSHA1 reads only the challenge range using the source's pinned
// credentials to acquire a URL. The CDN request itself receives no credentials.
func (c *ShareClient) PlaybackRangeSHA1(ctx context.Context, info *DownloadInfo, start, end int64) (string, error) {
	if info == nil || start < 0 || end < start || end >= info.Size || end-start+1 > MaxInstantRangeBytes {
		return "", fmt.Errorf("秒传校验范围无效")
	}
	link := info.URL
	if info.loadPlaybackURL != nil {
		started := time.Now()
		var err error
		link, err = info.loadPlaybackURL(ctx)
		emitPlaybackTiming(ctx, PlaybackTiming{Stage: "获取校验源直链（预取时为剩余等待）", Duration: time.Since(started), Failed: err != nil, Canceled: errors.Is(err, context.Canceled)})
		if err != nil {
			return "", fmt.Errorf("获取二次校验源直链: %w", err)
		}
	}
	return c.instantRangeHash(ctx, link, start, end, info.Size)
}

// Only fetch the URL speculatively, never file bytes. The upload request owns
// cancellation and joins the worker even if no challenge is returned.
func StartPlaybackSourcePrefetch(ctx context.Context, source *DownloadInfo) (*DownloadInfo, func()) {
	if source == nil || source.loadPlaybackURL == nil {
		return source, func() {}
	}
	copy := *source
	load := source.loadPlaybackURL
	workCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var wg sync.WaitGroup
	var link string
	var err error
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		started := time.Now()
		link, err = load(workCtx)
		emitPlaybackTiming(workCtx, PlaybackTiming{Stage: "校验源直链预取（与首次提交重叠）", Duration: time.Since(started), Failed: err != nil, Canceled: errors.Is(err, context.Canceled)})
	}()
	copy.loadPlaybackURL = func(readCtx context.Context) (string, error) {
		select {
		case <-readCtx.Done():
			return "", readCtx.Err()
		case <-done:
			return link, err
		}
	}
	return &copy, func() { cancel(); wg.Wait() }
}
