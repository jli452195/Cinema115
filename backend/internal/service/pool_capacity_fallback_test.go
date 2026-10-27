package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func capacityPoolFixture(t *testing.T) (*DynamicPool, *memoryPoolStore, []P115Account, *time.Time) {
	t.Helper()
	p, store := poolTest(t)
	ctx := context.Background()
	now := time.Now()
	p.now = func() time.Time { return now }
	settings := p.Settings()
	settings.MaxSessions = 1
	if err := p.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2"), poolTestAccount("c", "3")}
	for _, a := range accounts {
		if err := p.report(ctx, a, nil); err != nil {
			t.Fatal(err)
		}
	}
	l, err := p.Acquire(ctx, "original", "user", accounts[:1], poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return p, store, accounts, &now
}

func TestPoolCapacityFallbackCommitsOnlyAfterSuccess(t *testing.T) {
	p, store, accounts, now := capacityPoolFixture(t)
	ctx := context.Background()
	original := p.doc.Assignments["user"]
	health := p.doc.Health[accounts[0].ID]
	// Refreshing a live lease at capacity must not create another allocation.
	refresh, err := p.Acquire(ctx, "original", "user", accounts, poolProbe)
	if err != nil || refresh.Account.ID != accounts[0].ID {
		t.Fatal("full live lease moved", err)
	}
	defer refresh.Close()
	probes := 0
	prepare := func(key string) *PoolLease {
		t.Helper()
		l, err := p.Acquire(ctx, key, "user", accounts, func(context.Context, P115Account) error { probes++; return nil })
		if err != nil || l.Account.UID == original.UID {
			t.Fatal("capacity fallback failed", err)
		}
		return l
	}
	l := prepare("replacement")
	if p.doc.Assignments["user"] != original || probes != 1 {
		t.Fatal("preparation published assignment or probed every candidate")
	}
	// A pending full replacement cannot fan out to the third UID.
	if next, err := p.Acquire(ctx, "parallel", "user", accounts, poolProbe); !errors.Is(err, ErrPoolBusy) {
		if next != nil {
			_ = next.Close()
		}
		t.Fatal("parallel replacement selected another UID", err)
	}
	store.fail = true
	if err := l.Commit(ctx); err == nil || p.doc.Assignments["user"] != original {
		t.Fatal("failed commit published replacement", err)
	}
	store.fail = false
	if err := l.Close(); err != nil || p.AccountStatus(l.Account).Sessions != 0 {
		t.Fatal("failed preparation leaked capacity", err)
	}
	l = prepare("replacement")
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if p.doc.Health[accounts[0].ID] != health || len(p.Events()) != 0 {
		t.Fatal("capacity pressure marked a healthy account faulty")
	}
	if refresh.Current() || !errors.Is(refresh.Commit(ctx), ErrAccountChanged) {
		t.Fatal("old preparation overwrote successful replacement")
	}
	// Releasing old occupancy must not trigger an automatic switch back.
	d := p.clone()
	v := d.Leases["original"]
	v.ExpiresAt = now.Add(-time.Second).UnixMilli()
	d.Leases[v.Key] = v
	if err := p.save(ctx, d); err != nil {
		t.Fatal(err)
	}
	p, err = NewDynamicPool(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return *now }
	if got, err := p.Acquire(ctx, "replacement", "user", accounts, poolProbe); err != nil || got.Account.ID != l.Account.ID {
		t.Fatal("restart moved an existing session back", err)
	} else {
		_ = got.Close()
	}
}

func TestPoolCapacityFallbackRespectsNonCapacityBlocks(t *testing.T) {
	for _, mode := range []string{"all-full", "same-uid", "cooldown", "uid-throttle", "draining", "first-probe-failure", "unknown-read"} {
		t.Run(mode, func(t *testing.T) {
			p, _, accounts, _ := capacityPoolFixture(t)
			ctx := context.Background()
			original := p.doc.Assignments["user"]
			want := ErrPoolBusy
			switch mode {
			case "all-full":
				for i, a := range accounts[1:] {
					l, err := p.Acquire(ctx, fmt.Sprint("occupant", i), fmt.Sprint("other", i), []P115Account{a}, poolProbe)
					if err != nil {
						t.Fatal(err)
					}
					if err := l.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					_ = l.Close()
				}
			case "same-uid":
				alias := poolTestAccount("alias", accounts[0].UID)
				accounts = append(accounts[:1], alias)
				if err := p.report(ctx, alias, nil); err != nil {
					t.Fatal(err)
				}
			case "cooldown":
				if err := p.report(ctx, accounts[0], context.DeadlineExceeded); err != nil {
					t.Fatal(err)
				}
				want = ErrPoolCooling
			case "uid-throttle":
				alias := poolTestAccount("alias", accounts[0].UID)
				if err := p.report(ctx, alias, p115.ErrShareThrottled); err != nil {
					t.Fatal(err)
				}
				want = ErrPoolCooling
			case "draining":
				if err := p.SetMode(ctx, accounts[0].ID, "draining"); err != nil {
					t.Fatal(err)
				}
			case "first-probe-failure":
				want = ErrPoolCooling
			case "unknown-read":
				delete(p.doc.Health, accounts[0].ID)
				ctx, want = WithPlaybackFileReuse(ctx), nil
			}
			calls := 0
			l, err := p.Acquire(ctx, "new-device", "user", accounts, func(context.Context, P115Account) error {
				calls++
				return context.DeadlineExceeded
			})
			if l != nil {
				if l.Account.UID == original.UID {
					t.Fatal("unknown target at capacity did not use spare UID")
				}
				_ = l.Close()
			}
			if !errors.Is(err, want) || p.doc.Assignments["user"] != original || len(p.busy) != 0 {
				t.Fatal("capacity path bypassed admission or changed assignment", err)
			}
			if mode != "first-probe-failure" && calls != 0 || calls > 1 {
				t.Fatal("unnecessary or repeated probe", calls)
			}
		})
	}
}

func TestPoolCapacityFallbackConcurrentReservations(t *testing.T) {
	p, _, accounts, _ := capacityPoolFixture(t)
	ctx := context.Background()
	for i := 0; i < 24; i++ {
		// These idle users previously succeeded on the same account.
		p.doc.Assignments[fmt.Sprint("user", i)] = p.doc.Assignments["user"]
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *PoolLease, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			l, err := p.Acquire(ctx, fmt.Sprint("play", i), fmt.Sprint("user", i), accounts, poolProbe)
			if err != nil {
				if !errors.Is(err, ErrPoolBusy) {
					t.Error(err)
				}
				return
			}
			results <- l
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	if len(results) != 2 {
		t.Fatal("free UID capacity was not fully reserved", len(results))
	}
	for _, a := range accounts {
		if p.AccountStatus(a).Sessions != 1 {
			t.Fatal("UID capacity exceeded or unused", a.ID)
		}
	}
	for l := range results {
		if err := l.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.busy) != 0 || len(p.probes) != 0 {
		t.Fatal("concurrent requests leaked admission locks")
	}
}

func TestDeviceSlotCapacityFallbackAndReuse(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	first := deviceSlotIssue(t, s, r, accounts, "phone", "phone")
	// Different devices need distinct slots. Original playback stays intact.
	second := deviceSlotIssue(t, s, r, accounts, "tv", "tv")
	if first.account.UID == second.account.UID || len(s.UserPlayback.List()) != 2 {
		t.Fatal("capacity fallback merged or retired original playback")
	}
	original := s.Pool.doc.Assignments[r.UserID]
	// The old device can reuse its existing slot even when both UIDs are full.
	first = deviceSlotIssue(t, s, r, accounts, "phone", "phone")
	if got := s.Pool.doc.Assignments[r.UserID]; got != original {
		t.Fatal("old session refresh changed the newer default", got, original)
	}
	*now = now.Add(time.Second)
	refresh := deviceSlotIssue(t, s, r, accounts, "phone", "next-movie")
	if refresh.account.UID != first.account.UID {
		t.Fatal("same-device reuse moved to a free UID")
	}
	if got := s.Pool.doc.Assignments[r.UserID]; got.UID != refresh.account.UID || got.Generation <= original.Generation {
		t.Fatal("new playback did not commit the reused device target", got, original)
	}
	multiUIDCounts(t, s.Pool, accounts, 2, 0, 0)
}
