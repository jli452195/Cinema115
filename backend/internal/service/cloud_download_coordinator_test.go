package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitCloudResources(t *testing.T, c *CloudDownloadCoordinator, uid string, count int) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		c.mu.Lock()
		n := 0
		if work := c.uids[uid]; work != nil {
			n = len(work.resources)
		}
		c.mu.Unlock()
		if n == count {
			return
		}
		select {
		case <-timer.C:
			t.Fatalf("coordination reservation count=%d, want %d", n, count)
		case <-tick.C:
		}
	}
}

func TestCloudCoordinatorNormalizesIdentityAndRejectsInvalidKeys(t *testing.T) {
	c := &CloudDownloadCoordinator{}
	hash := strings.Repeat("a", 40)
	release, err := c.Acquire(context.Background(), "00123", "magnet", strings.ToUpper(hash))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := c.Acquire(context.Background(), "123", "bt_v1", hash); !errors.Is(err, ErrCloudTaskBusy) {
		t.Fatal("BT aliases or UID forms bypassed coordination", err)
	}
	for _, tc := range []struct{ uid, protocol, hash string }{
		{"", "magnet", hash}, {"test", "magnet", hash}, {"0", "magnet", hash},
		{"123", "bt_v2", hash}, {"123", "bt_v1", strings.Repeat("g", 40)},
		{"123", "magnet", strings.Repeat("a", 32)}, {"123", "ed2k", hash},
	} {
		if _, err := c.Acquire(context.Background(), tc.uid, tc.protocol, tc.hash); !errors.Is(err, ErrCloudCoordinationInput) {
			t.Fatal("invalid coordination key accepted", tc, err)
		}
	}
	var absent *CloudDownloadCoordinator
	if _, err := absent.Acquire(context.Background(), "123", "bt_v1", hash); !errors.Is(err, ErrCloudCoordinationInput) {
		t.Fatal("nil coordinator silently created a private gate", err)
	}
	other, err := c.Acquire(context.Background(), "456", "bt_v1", hash)
	if err != nil {
		t.Fatal("different UID was blocked", err)
	}
	other()
	release()
	waitCloudResources(t, c, "123", 0)
}

func TestCloudCoordinatorSerializesUIDAndCancelsWaitingResource(t *testing.T) {
	c := &CloudDownloadCoordinator{}
	hash := strings.Repeat("a", 40)
	release, err := c.Acquire(context.Background(), "123", "bt_v1", hash)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	var waitErr error
	go func() {
		defer close(finished)
		unlock, err := c.Acquire(ctx, "123", "ed2k", strings.Repeat("b", 32))
		if err == nil {
			unlock()
			err = errors.New("same UID operations overlapped")
		}
		waitErr = err
	}()
	defer func() { cancel(); <-finished }()
	waitCloudResources(t, c, "123", 2)
	if _, err := c.Acquire(context.Background(), "123", "ed2k", strings.Repeat("b", 32)); !errors.Is(err, ErrCloudTaskBusy) {
		t.Fatal("duplicate waiting resource was not rejected", err)
	}
	cancel()
	<-finished
	if !errors.Is(waitErr, context.Canceled) {
		t.Fatal("waiting operation was not cancelled", waitErr)
	}
	waitCloudResources(t, c, "123", 1)
	release()
	next, err := c.Acquire(context.Background(), "123", "ed2k", strings.Repeat("b", 32))
	if err != nil {
		t.Fatal("cancelled reservation leaked", err)
	}
	defer next()
	release() // A late/double release must not unlock this new operation.
	if _, err := c.Acquire(context.Background(), "123", "ed2k", strings.Repeat("b", 32)); !errors.Is(err, ErrCloudTaskBusy) {
		t.Fatal("stale release removed a newer reservation", err)
	}
}

func TestCloudCoordinatorConcurrentResourcesReleaseAndNoOverlap(t *testing.T) {
	c := &CloudDownloadCoordinator{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := c.Acquire(ctx, "123", "bt_v1", strings.Repeat("f", 40))
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	var wg sync.WaitGroup
	var active, overlaps atomic.Int32
	errorsCh := make(chan error, 48)
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := c.Acquire(ctx, "123", "bt_v1", fmt.Sprintf("%040x", i+1))
			if err != nil {
				errorsCh <- err
				return
			}
			defer release()
			if active.Add(1) != 1 {
				overlaps.Add(1)
			}
			active.Add(-1)
		}(i)
	}
	defer wg.Wait()
	defer cancel()
	waitCloudResources(t, c, "123", 49)
	first()
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if overlaps.Load() != 0 {
		t.Fatal("UID gate allowed overlapping operations")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.uids) != 0 {
		t.Fatal("idle UID/resource identities retained indefinitely")
	}
}

func TestCloudCoordinatorAlreadyCancelledDoesNotReserve(t *testing.T) {
	c := &CloudDownloadCoordinator{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Acquire(ctx, "123", "bt_v1", strings.Repeat("a", 40)); !errors.Is(err, context.Canceled) || len(c.uids) != 0 {
		t.Fatal("cancelled request reserved a resource", err)
	}
}
