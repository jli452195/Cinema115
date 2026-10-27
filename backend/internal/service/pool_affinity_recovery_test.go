package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestPoolUserTargetCapacityAndConcurrentAllocation(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2")}
	// Warm health, then hold two overlapping preparations for the same user.
	for _, a := range accounts {
		if err := p.report(ctx, a, nil); err != nil {
			t.Fatal(err)
		}
	}
	one, err := p.Acquire(ctx, "phone", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := p.Acquire(ctx, "tv", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	if one.Account.UID != two.Account.UID {
		t.Fatal("overlapping first requests selected different UIDs")
	}
	if err := two.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := one.Commit(ctx); err != nil {
		t.Fatal("concurrent first commit rejected same target", err)
	}
	if p.AccountStatus(one.Account).Sessions != 2 {
		t.Fatal("user target merged independent session capacity")
	}
	settings := p.Settings()
	settings.MaxSessions = 2
	if err := p.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	before := p.doc.Assignments["user"]
	overflow, err := p.Acquire(ctx, "third-player", "user", accounts, poolProbe)
	if err != nil || overflow.Account.UID == one.Account.UID {
		t.Fatal("full user target did not use free capacity", err)
	}
	defer overflow.Close()
	if p.doc.Assignments["user"] != before {
		t.Fatal("preparation changed assignment before commit")
	}
	if err := overflow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if p.doc.Assignments["user"].UID != overflow.Account.UID || p.AccountStatus(one.Account).Sessions != 2 {
		t.Fatal("capacity switch did not commit or erased original plays")
	}
	other, err := p.Acquire(ctx, "other", "other-user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if other.Account.UID == one.Account.UID {
		t.Fatal("new user ignored free capacity on other UID")
	}
}

func TestPoolTimeoutDedupRecoveryAndTransactionalFailover(t *testing.T) {
	p, store := poolTest(t)
	ctx := context.Background()
	settings := p.Settings()
	settings.MaxSessions = 4 // One issued session and three overlapping requests.
	if err := p.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.now = func() time.Time { return now }
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2")}
	initial, err := p.Acquire(ctx, "playing", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = initial.Close()
	assignment := p.doc.Assignments["user"]
	stale, err := p.Acquire(ctx, "abandoned-old-preparation", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	// Multiple requests observe one health generation, as with a coalesced read.
	one, err := p.Acquire(ctx, "range", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Acquire(ctx, "prefetch", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	failure := &playbackTargetError{stage: "directory", err: context.DeadlineExceeded}
	for _, l := range []*PoolLease{one, two} {
		a := &MediaAuthorization{poolLease: l, account: l.Account}
		retry, err := a.PoolTargetFailed(ctx, failure)
		var recovery *PoolRecoveryError
		if retry || !errors.As(err, &recovery) || recovery.RetryAfter > 12 {
			t.Fatal("first timeout switched UID or lost retry hint", retry, err)
		}
		_ = a.Close()
	}
	health := p.doc.Health[initial.Account.ID]
	if health.Failures != 1 || health.NextProbe != health.Until || p.doc.Assignments["user"] != assignment {
		t.Fatal("shared failure multiplied backoff or changed assignment", health)
	}
	if _, err := p.Acquire(ctx, "during-cooldown", "user", accounts, poolProbe); !errors.Is(err, ErrPoolCooling) {
		t.Fatal("cooldown moved user to another UID", err)
	}
	now = time.UnixMilli(health.Until).Add(time.Millisecond)
	recovering, err := p.Acquire(ctx, "playing", "user", accounts, nil)
	if err != nil || recovering.Account.UID != initial.Account.UID {
		t.Fatal("recovery did not retry original UID", err)
	}
	if _, err := p.Acquire(ctx, "other-recovery", "another-user", accounts[:1], poolProbe); !errors.Is(err, ErrPoolBusy) {
		t.Fatal("second recovery admitted", err)
	}
	if _, _, err := p.beginProbe(initial.Account, false); !errors.Is(err, ErrPoolBusy) {
		t.Fatal("background/manual probe overlapped foreground recovery", err)
	}
	a := &MediaAuthorization{poolLease: recovering, account: recovering.Account}
	if retry, err := a.PoolTargetFailed(ctx, failure); !retry || err != nil {
		t.Fatal("second independent failure did not permit failover", retry, err)
	}
	if p.doc.Health[initial.Account.ID].Failures != 2 || p.doc.Assignments["user"] != assignment {
		t.Fatal("failed attempt changed user target before successful issuance")
	}
	next, err := p.Acquire(ctx, "replacement", "user", accounts, poolProbe)
	if err != nil || next.Account.UID == initial.Account.UID {
		t.Fatal("confirmed failure did not select another UID", err)
	}
	defer next.Close()
	store.fail = true
	if err := next.Commit(ctx); err == nil || p.doc.Assignments["user"] != assignment {
		t.Fatal("failed persistence published target change", err)
	}
	store.fail = false
	if err := next.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := stale.Commit(ctx); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale original-UID request committed after failover", err)
	}
	if err := p.report(ctx, initial.Account, nil); err != nil {
		t.Fatal(err)
	}
	_ = next.Close()
	last, err := p.Acquire(ctx, "new-player", "user", accounts, poolProbe)
	if err != nil || last.Account.UID != next.Account.UID {
		t.Fatal("original UID recovery moved user back", err)
	}
	_ = last.Close()
}

func TestPoolProbeTimeoutCanRecoverWithoutAccountSwitch(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	now := time.Now()
	p.now = func() time.Time { return now }
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2")}
	l, err := p.Acquire(ctx, "initial", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	calls := 0
	_, err = p.Acquire(ctx, "retry", "user", accounts, func(context.Context, P115Account) error {
		calls++
		return context.DeadlineExceeded
	})
	if !errors.Is(err, ErrPoolCooling) || calls != 1 {
		t.Fatal("probe timeout caused eager failover", err, calls)
	}
	h := p.doc.Health[l.Account.ID]
	now = time.UnixMilli(h.Until).Add(time.Millisecond)
	next, err := p.Acquire(ctx, "retry", "user", accounts, poolProbe)
	if err != nil || next.Account.UID != l.Account.UID {
		t.Fatal("original UID could not recover", err)
	}
	defer next.Close()
	if err := next.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if p.doc.Health[l.Account.ID].State != "ready" || p.doc.Health[l.Account.ID].Failures != 0 {
		t.Fatal("successful recovery did not reset faults")
	}
}

func TestPoolConcurrentFailureReportsAndQueueCancellation(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	a := poolTestAccount("a", "1")
	if err := p.report(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	sequence := p.doc.Health[a.ID].Sequence
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.reportChecked(ctx, a, context.DeadlineExceeded, &sequence); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p.doc.Health[a.ID].Failures != 1 {
		t.Fatal("simultaneous errors multiplied failure count")
	}
	now := time.UnixMilli(p.doc.Health[a.ID].Until).Add(time.Second)
	p.now = func() time.Time { return now }
	before := p.doc.Health[a.ID]
	_, err := p.Acquire(ctx, "queue-timeout", "user", []P115Account{a}, func(context.Context, P115Account) error {
		return errors.Join(p115.ErrRateQueueFull, context.DeadlineExceeded)
	})
	if !errors.Is(err, p115.ErrRateQueueFull) || p.doc.Health[a.ID] != before || len(p.busy) != 0 || len(p.probes) != 0 {
		t.Fatal("local queue timeout poisoned health or leaked recovery gate", err)
	}
}

func TestPoolFileReadProbesOnlyForRecovery(t *testing.T) {
	p, _ := poolTest(t)
	ctx := WithPlaybackFileReuse(context.Background())
	now := time.Now()
	p.now = func() time.Time { return now }
	a := poolTestAccount("a", "1")
	calls := 0
	probe := func(context.Context, P115Account) error { calls++; return nil }
	l, err := p.Acquire(ctx, "initial", "user", []P115Account{a}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || !l.skipHealthRefresh {
		t.Fatal("first read added a directory request")
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if err := p.report(ctx, a, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	now = time.UnixMilli(p.doc.Health[a.ID].Until).Add(time.Millisecond)
	l, err = p.Acquire(ctx, "initial", "user", []P115Account{a}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || l.skipHealthRefresh {
		t.Fatal("read-only recovery skipped its real health check")
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if p.doc.Health[a.ID].State != "ready" {
		t.Fatal("successful recovery left account cooling")
	}
	l, err = p.Acquire(ctx, "initial", "user", []P115Account{a}, probe)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if calls != 1 || !l.skipHealthRefresh {
		t.Fatal("healthy read repeated recovery check")
	}
}
