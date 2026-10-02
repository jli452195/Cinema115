package p115

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPoolBackgroundBudgetYieldsAndCounters(t *testing.T) {
	b := &RequestBudget{waiting: 1, lastBackground: time.Now()}
	ctx := WithBackgroundBudget(WithRequestBudget(context.Background(), b))
	if err := WaitRequestBudget(ctx, "UID=1_A"); !errors.Is(err, ErrRateQueueFull) {
		t.Fatal("background queued behind foreground", err)
	}
	if b.Stats().BackgroundDeferred != 1 {
		t.Fatal("missing deferred count")
	}
	b.waiting = 0
	ctx = WithBudgetSource(ctx, "1")
	if err := WaitRequestBudget(ctx, "UID=1_A"); err != nil {
		t.Fatal(err)
	}
	ObserveRequestBudget(ctx, 429, time.Minute)
	s := b.Stats()
	if s.Requests != 1 || s.SourceRequests != 1 || s.Throttled != 1 || s.CoolingUntil == 0 || s.Waiting != 0 {
		t.Fatal(s)
	}
}

func TestPoolBudgetVirtualClockSpacingAndRetryAfter(t *testing.T) {
	now := time.Unix(1000, 0)
	b := &RequestBudget{clock: func() time.Time { return now }, wait: func(ctx context.Context, at time.Time) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		now = at
		return nil
	}}
	b.Configure(250*time.Millisecond, 50*time.Millisecond)
	ctx := WithRequestBudget(context.Background(), b)
	start := now
	for _, cookie := range []string{"UID=1_A", "UID=1_B", "UID=2_A"} {
		if err := WaitRequestBudget(ctx, cookie); err != nil {
			t.Fatal(err)
		}
	}
	if now.Sub(start) != 300*time.Millisecond || b.Stats().WaitMS != 300 {
		t.Fatal("incorrect UID/global spacing", now.Sub(start), b.Stats())
	}
	ObserveRequestBudget(ctx, 429, time.Minute)
	if err := WaitRequestBudget(ctx, "UID=3_A"); !errors.Is(err, ErrShareThrottled) {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := WaitRequestBudget(ctx, "UID=3_A"); err != nil {
		t.Fatal("cooling did not end", err)
	}
	b.next["3"] = now.Add(4 * time.Second)
	if err := WaitRequestBudget(ctx, "UID=3_B"); !errors.Is(err, ErrRateQueueFull) {
		t.Fatal("unbounded scheduling", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := WaitRequestBudget(canceled, "UID=4_A"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if b.Stats().Waiting != 0 {
		t.Fatal("cancel leaked queue")
	}
}

func TestPoolRequestBudgetCancellationAndGlobalThrottle(t *testing.T) {
	b := &RequestBudget{}
	ctx := WithRequestBudget(context.Background(), b)
	if err := WaitRequestBudget(ctx, "UID=1_A"); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if err := WaitRequestBudget(deadline, "UID=1_B"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("same UID escaped rate limit", err)
	}
	if b.waiting != 0 {
		t.Fatal("cancelled waiter leaked")
	}
	ObserveRequestBudget(ctx, 429, time.Minute)
	if err := WaitRequestBudget(ctx, "UID=2_A"); !errors.Is(err, ErrShareThrottled) {
		t.Fatal("429 escaped via another UID", err)
	}
	if time.Until(b.cooling) < 59*time.Second {
		t.Fatal("Retry-After shortened")
	}
}

func TestPoolRequestBudgetBoundedQueue(t *testing.T) {
	b := &RequestBudget{waiting: 64}
	ctx := WithRequestBudget(context.Background(), b)
	if err := WaitRequestBudget(ctx, "UID=1_A"); !errors.Is(err, ErrRateQueueFull) {
		t.Fatal(err)
	}
	b.waiting = 0
	b.next = map[string]time.Time{"1": time.Now().Add(4 * time.Second)}
	if err := WaitRequestBudget(ctx, "UID=1_B"); !errors.Is(err, ErrRateQueueFull) {
		t.Fatal("unbounded delay", err)
	}
}
