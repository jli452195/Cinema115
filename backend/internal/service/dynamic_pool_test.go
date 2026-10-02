package service

import (
	"context"
	"errors"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"sync"
	"testing"
	"time"
)

type memoryPoolStore struct {
	doc  model.PlaybackPoolDocument
	fail bool
}

func (s *memoryPoolStore) PlaybackPool(context.Context) (model.PlaybackPoolDocument, error) {
	return s.doc, nil
}
func (s *memoryPoolStore) SavePlaybackPool(_ context.Context, d model.PlaybackPoolDocument) error {
	if s.fail {
		return errors.New("disk full")
	}
	s.doc = d
	return nil
}
func poolTestAccount(id, uid string) P115Account {
	return P115Account{ID: id, UID: uid, Revision: 1, Cookie: "UID=" + uid + "_A", PlaybackParentCID: "123"}
}
func poolTest(t *testing.T) (*DynamicPool, *memoryPoolStore) {
	t.Helper()
	st := &memoryPoolStore{}
	p, e := NewDynamicPool(context.Background(), st)
	if e != nil {
		t.Fatal(e)
	}
	return p, st
}
func poolProbe(context.Context, P115Account) error { return nil }

func TestDynamicPoolFailoverAndRestart(t *testing.T) {
	p, st := poolTest(t)
	ctx := context.Background()
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2")}
	calls := 0
	l, err := p.Acquire(ctx, "session", "user", accounts, func(_ context.Context, a P115Account) error {
		calls++
		if a.ID == "a" {
			return p115.ErrShareLoginRequired
		}
		return nil
	})
	if err != nil || l.Account.ID != "b" || calls != 2 {
		t.Fatal(l, err, calls)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewDynamicPool(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.AccountStatus(accounts[1]).Sessions != 1 {
		t.Fatal("restart lost issued occupancy")
	}
	l2, err := restarted.Acquire(ctx, "session", "user", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if l2.Record.Generation != l.Record.Generation || l2.Account.ID != "b" {
		t.Fatal("session drift")
	}
}

func TestDynamicPoolAtomicUIDCapacity(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	settings := p.Settings()
	settings.MaxSessions = 1
	if err := p.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "1")}
	var wg sync.WaitGroup
	wins := make(chan *PoolLease, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, e := p.Acquire(ctx, string(rune('a'+i)), "user", accounts, poolProbe)
			if e == nil {
				wins <- l
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	n := 0
	for l := range wins {
		n++
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("capacity exceeded: %d", n)
	}
}

func TestDynamicPoolCancelledAndFailedPersistence(t *testing.T) {
	p, st := poolTest(t)
	ctx := context.Background()
	accounts := []P115Account{poolTestAccount("a", "1")}
	st.fail = true
	if _, err := p.Acquire(ctx, "x", "u", accounts, poolProbe); err == nil {
		t.Fatal("failed store accepted")
	}
	if p.AccountStatus(accounts[0]).Sessions != 0 {
		t.Fatal("uncommitted lease published")
	}
	st.fail = false
	cancelled, cancel := context.WithCancel(ctx)
	_, err := p.Acquire(cancelled, "x", "u", accounts, func(context.Context, P115Account) error { cancel(); return context.Canceled })
	if !errors.Is(err, context.Canceled) || p.AccountStatus(accounts[0]).Sessions != 0 {
		t.Fatal("cancel leaked reservation", err)
	}
	if p.AccountStatus(accounts[0]).State != "unknown" {
		t.Fatal("cancel poisoned health")
	}
}

func TestDynamicPoolResidualAndStaleLease(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2")}
	l, err := p.Acquire(ctx, "x", "u", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if err := p.SetMode(ctx, l.Account.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	next, err := p.Acquire(ctx, "x", "u", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Account.UID == l.Account.UID || l.Current() {
		t.Fatal("old lease remained current")
	}
	if p.AccountStatus(l.Account).Sessions != 1 {
		t.Fatal("issued residual dropped")
	}
	if err := l.Commit(ctx); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale result committed", err)
	}
}

func TestDynamicPoolSameSessionWaitCancellation(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	accounts := []P115Account{poolTestAccount("a", "1")}
	l, err := p.Acquire(ctx, "x", "u", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, err := p.Acquire(c, "x", "u", accounts, poolProbe); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func memberTestPool(t *testing.T, s *MemberPlaybackService) string {
	t.Helper()
	id, err := s.accounts.Add("pool")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=700_A; SEID=test"
	if err := s.accounts.Update(id, 1, "pool", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.UpdatePlaybackDirectory(id, 2, "123"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDynamicPoolSharedAttemptsAndUIDExclusion(t *testing.T) {
	p, _ := poolTest(t)
	ctx := WithPoolAttempts(context.Background(), 2)
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("duplicate", "1"), poolTestAccount("b", "2"), poolTestAccount("c", "3")}
	l, err := p.Acquire(ctx, "session", "u", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	first := l.Account.UID
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = p.Acquire(ctx, "session", "u", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if l.Account.UID == first {
		t.Fatal("retried the same UID through another configuration")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if PoolCanRetry(ctx) {
		t.Fatal("nested retry exceeded shared budget")
	}
	if _, err = p.Acquire(ctx, "session", "u", accounts, poolProbe); !errors.Is(err, ErrPoolUnavailable) {
		t.Fatal(err)
	}
}

func TestDynamicPoolSessionLifecycle(t *testing.T) {
	p, st := poolTest(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := WithPoolRequest(context.Background(), PoolRequest{lifecycleKey: "authenticated-session"})
	account := poolTestAccount("a", "1")
	l, err := p.Acquire(ctx, "x", "u", []P115Account{account}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	original := p.doc.Leases["x"].ExpiresAt
	now = now.Add(2 * time.Minute)
	if err = p.SessionEvent(ctx, "forged-session", true); err != nil {
		t.Fatal(err)
	}
	if p.doc.Leases["x"].ExpiresAt != original {
		t.Fatal("other session released capacity")
	}
	if err = p.SessionEvent(ctx, "authenticated-session", false); err != nil {
		t.Fatal(err)
	}
	if p.doc.Leases["x"].ExpiresAt <= original {
		t.Fatal("heartbeat did not renew")
	}
	st.fail = true
	if err = p.SessionEvent(ctx, "authenticated-session", true); err == nil {
		t.Fatal("failed stop persisted")
	}
	if p.doc.Leases["x"].Stopped {
		t.Fatal("uncommitted stop published")
	}
	st.fail = false
	if err = p.SessionEvent(ctx, "authenticated-session", true); err != nil {
		t.Fatal(err)
	}
	stopped := p.doc.Leases["x"].ExpiresAt
	if stopped != now.Add(2*time.Minute).UnixMilli() {
		t.Fatal("missing residual grace")
	}
	now = now.Add(time.Minute)
	if err = p.SessionEvent(ctx, "authenticated-session", false); err != nil {
		t.Fatal(err)
	}
	if p.doc.Leases["x"].ExpiresAt != stopped {
		t.Fatal("late progress revived stop")
	}
	now = now.Add(2 * time.Minute)
	if p.AccountStatus(account).Sessions != 0 {
		t.Fatal("stopped lease leaked")
	}
}

func TestDynamicPoolRejectCrossUserReuse(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	accounts := []P115Account{poolTestAccount("a", "1")}
	l, err := p.Acquire(ctx, "x", "owner", accounts, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Acquire(ctx, "x", "other", accounts, poolProbe); !errors.Is(err, ErrLogin) {
		t.Fatal(err)
	}
}

func TestDynamicPoolThrottleSurvivesRestart(t *testing.T) {
	p, st := poolTest(t)
	ctx := context.Background()
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2")}
	if err := p.report(ctx, accounts[0], &p115.HTTPError{Status: 429, RetryAfter: time.Minute}); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewDynamicPool(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = restarted.Acquire(ctx, "x", "u", accounts, func(context.Context, P115Account) error { calls++; return nil })
	if !errors.Is(err, ErrPoolUnavailable) || calls != 0 {
		t.Fatal("restart bypassed global throttle", err, calls)
	}
	if restarted.AccountStatus(accounts[1]).State != "throttled" {
		t.Fatal("global cooling not visible")
	}
}

func TestDynamicPoolDrainAndLowerCapacity(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	a := poolTestAccount("a", "1")
	l, err := p.Acquire(ctx, "x", "u", []P115Account{a}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if err = p.SetMode(ctx, a.ID, "draining"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Acquire(ctx, "new", "u", []P115Account{a}, poolProbe); !errors.Is(err, ErrPoolBusy) {
		t.Fatal(err)
	}
	next, err := p.Acquire(ctx, "x", "u", []P115Account{a}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Record.Generation != l.Record.Generation {
		t.Fatal("drain replaced active session")
	}
	c := p.Settings()
	c.MaxSessions = 1
	if err = p.Configure(ctx, c); err != nil {
		t.Fatal(err)
	}
	if !next.Current() {
		t.Fatal("capacity reduction revoked issued session")
	}
}
