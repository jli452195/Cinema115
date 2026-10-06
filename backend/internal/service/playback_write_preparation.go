package service

import (
	"context"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
)

type playbackAccountPreparer interface {
	PreparePlaybackAccount(context.Context, string, string) (*p115.PlaybackUploadTicket, error)
}

type playbackWritePreparation struct {
	ticket                            *p115.PlaybackUploadTicket
	directoryDuration, ticketDuration time.Duration
}

// Called only after a confirmed target miss. Both branches are read-only and
// use the same fixed credentials. The authorization grant stays on the caller
// goroutine; no upload intent may be persisted until both branches finish.
func preparePlaybackWrite(ctx context.Context, client playbackAccountPreparer, accountID, cookie string, validateDirectory func(context.Context) error) (playbackWritePreparation, error) {
	var result playbackWritePreparation
	if err := ctx.Err(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		started := time.Now()
		ticket, err := client.PreparePlaybackAccount(ctx, accountID, cookie)
		result.ticketDuration = time.Since(started)
		result.ticket = ticket
		if err != nil {
			cancel(&playbackTargetError{stage: "朋友账号秒传预检失败", err: err})
		}
	}()
	started := time.Now()
	if err := validateDirectory(ctx); err != nil {
		cancel(err)
	}
	result.directoryDuration = time.Since(started)
	// Cancellation is not completion: join even on a failed directory check.
	wg.Wait()
	return result, context.Cause(ctx)
}
