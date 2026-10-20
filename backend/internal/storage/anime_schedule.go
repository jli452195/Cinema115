package storage

import (
	"time"

	"strm115/backend/internal/model"
)

// Use elapsed time, not poll counts: restarts and slow requests skip missed
// slots instead of replaying a burst of early checks. Only accepted/running
// downloads use this schedule; upstream failures retain their own backoff.
func animeNextStatusAt(op model.AnimeSubmitOperation, now int64) int64 {
	started := op.UpdatedAt
	if op.AuthorizedAt != nil {
		started = *op.AuthorizedAt
	}
	for _, offset := range [...]time.Duration{5 * time.Second, 20 * time.Second, 2 * time.Minute, 5 * time.Minute} {
		if due := started + offset.Milliseconds(); due > now {
			return max(due, now+5000)
		}
	}
	delay := 5 * time.Minute
	if now-started >= (3 * time.Hour).Milliseconds() {
		delay = 30 * time.Minute
	} else if now-started >= time.Hour.Milliseconds() {
		delay = 10 * time.Minute
	}
	return now + delay.Milliseconds()
}
