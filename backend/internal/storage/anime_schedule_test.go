package storage

import (
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestAnimeStatusScheduleSkipsMissedSlotsAndSlowsLongDownloads(t *testing.T) {
	start := int64(1700000000000)
	op := model.AnimeSubmitOperation{AuthorizedAt: &start, UpdatedAt: start}
	for _, tc := range []struct {
		elapsed, wait time.Duration
	}{
		{0, 5 * time.Second},
		{5 * time.Second, 15 * time.Second},
		{20 * time.Second, 100 * time.Second},
		{2 * time.Minute, 3 * time.Minute},
		{5 * time.Minute, 5 * time.Minute},
		{30 * time.Minute, 5 * time.Minute},
		{time.Hour, 10 * time.Minute},
		{3 * time.Hour, 30 * time.Minute},
	} {
		now := start + tc.elapsed.Milliseconds()
		if got := animeNextStatusAt(op, now); got != now+tc.wait.Milliseconds() {
			t.Errorf("elapsed %s: next delay %s, want %s", tc.elapsed, time.Duration(got-now)*time.Millisecond, tc.wait)
		}
	}
	// A restart after hours must not replay the fast checks from the beginning.
	if due := animeNextStatusAt(op, start+(4*time.Hour).Milliseconds()); due != start+(270*time.Minute).Milliseconds() {
		t.Fatal("restart reset the early check window", due)
	}
}
