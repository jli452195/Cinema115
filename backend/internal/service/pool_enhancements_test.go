package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type poolNotificationFake struct {
	calls int
	fail  bool
	keys  []string
}

func (f *poolNotificationFake) NotifyPoolHealth(_ context.Context, bot, key, message string) error {
	f.calls++
	if f.fail {
		return errors.New("queue unavailable")
	}
	if bot != "bot" || strings.Contains(message, "UID=") {
		return errors.New("unsafe notification")
	}
	f.keys = append(f.keys, key)
	return nil
}

func TestPoolNotificationOptInRetryAndDeduplication(t *testing.T) {
	s, _, _ := memberFixture(t)
	id := memberTestPool(t, s)
	a, err := s.accounts.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fake := &poolNotificationFake{}
	s.PoolNotifier = fake
	if err := s.Pool.report(ctx, a, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	s.flushPoolNotifications(ctx)
	if fake.calls != 0 {
		t.Fatal("notification without opt-in")
	}
	settings := s.Pool.Settings()
	settings.NotificationBotID = "bot"
	if err := s.Pool.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.report(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	fake.fail = true
	s.flushPoolNotifications(ctx)
	if s.Pool.Events()[1].Delivered {
		t.Fatal("failed enqueue marked delivered")
	}
	fake.fail = false
	s.flushPoolNotifications(ctx)
	s.flushPoolNotifications(ctx)
	if fake.calls != 2 || len(fake.keys) != 1 || !s.Pool.Events()[1].Delivered {
		t.Fatal("duplicate or lost alert", fake)
	}
}

func TestPoolReconcileOnlyRenewsExactActiveMatches(t *testing.T) {
	p, st := poolTest(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := WithPoolRequest(context.Background(), PoolRequest{observationKey: "verified-observation", lifecycleKey: "stop-key"})
	l, err := p.Acquire(ctx, "x", "u", []P115Account{poolTestAccount("a", "1")}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	restarted, err := NewDynamicPool(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = p.now
	original := restarted.doc.Leases["x"].ExpiresAt
	now = now.Add(2 * time.Minute)
	if err := restarted.reconcileObserved(ctx, map[string]int64{"other-user-session": now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if restarted.doc.Leases["x"].ExpiresAt != original {
		t.Fatal("missing session released/renewed occupancy")
	}
	if err := restarted.reconcileObserved(ctx, map[string]int64{"verified-observation": now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if restarted.doc.Leases["x"].ExpiresAt <= original {
		t.Fatal("exact evidence did not renew recovered lease")
	}
	if err := restarted.SessionEvent(ctx, "stop-key", true); err != nil {
		t.Fatal(err)
	}
	stopped := restarted.doc.Leases["x"].ExpiresAt
	now = now.Add(time.Minute)
	if err := restarted.reconcileObserved(ctx, map[string]int64{"verified-observation": now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if restarted.doc.Leases["x"].ExpiresAt != stopped {
		t.Fatal("reconciliation revived stopped lease")
	}
}

func TestPoolSingleSecondaryIsolationAndRecovery(t *testing.T) {
	p, _ := poolTest(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := context.Background()
	main, child := poolTestAccount("main", "1"), poolTestAccount("secondary", "2")
	main.Primary = true
	calls := 0
	_, err := p.Acquire(ctx, "one", "u", []P115Account{main, child}, func(_ context.Context, a P115Account) error {
		calls++
		if a.Primary {
			t.Fatal("used primary as spare")
		}
		return p115.ErrShareLoginRequired
	})
	if !errors.Is(err, ErrPoolInvalid) || calls != 1 {
		t.Fatal("repeated sole UID", err, calls)
	}
	_, err = p.Acquire(ctx, "two", "u", []P115Account{main, child}, poolProbe)
	if !errors.Is(err, ErrPoolInvalid) {
		t.Fatal("missing invalid reason", err)
	}
	if err := p.report(ctx, child, nil); err != nil {
		t.Fatal(err)
	}
	l, err := p.Acquire(ctx, "two", "u", []P115Account{main, child}, poolProbe)
	if err != nil || l.Account.ID != child.ID {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.Events()) != 2 || p.Events()[1].State != "ready" {
		t.Fatal("missing recovery event", p.Events())
	}
}

func TestPoolHealthBackoffProbeDebounceAndStaleSuccess(t *testing.T) {
	p, st := poolTest(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := context.Background()
	a := poolTestAccount("a", "1")
	seq, done, err := p.beginProbe(a, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.beginProbe(poolTestAccount("alias", "1"), false); !errors.Is(err, ErrPoolBusy) {
		t.Fatal("duplicate UID probe", err)
	}
	if err := p.report(ctx, a, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	if err := p.reportChecked(ctx, a, nil, &seq); err != nil {
		t.Fatal(err)
	}
	if p.AccountStatus(a).State != "invalid_credentials" {
		t.Fatal("late success erased failure")
	}
	done()
	if _, _, err := p.beginProbe(a, false); !errors.Is(err, ErrPoolBusy) {
		t.Fatal("manual debounce escaped", err)
	}
	now = now.Add(16 * time.Second)
	seq, done, err = p.beginProbe(a, false)
	if err != nil {
		t.Fatal(err)
	}
	st.fail = true
	if err := p.reportChecked(ctx, a, nil, &seq); err == nil {
		t.Fatal("failed save succeeded")
	}
	if len(p.Events()) != 1 {
		t.Fatal("uncommitted event published")
	}
	st.fail = false
	if err := p.reportChecked(ctx, a, nil, &seq); err != nil {
		t.Fatal(err)
	}
	done()
	if p.AccountStatus(a).State != "ready" {
		t.Fatal("recovery did not publish")
	}
	if err := p.report(ctx, a, errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	first := p.doc.Health[a.ID]
	now = time.UnixMilli(first.Until).Add(time.Second)
	if err := p.report(ctx, a, errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	second := p.doc.Health[a.ID]
	if second.Until-now.UnixMilli() <= first.Until-first.CheckedAt {
		t.Fatal("no exponential backoff")
	}
	if len(p.Events()) != 3 {
		t.Fatal("repeated failure spammed events", p.Events())
	}
}

func TestPoolAffinityLoadFirstAndPreferenceExpires(t *testing.T) {
	p, _ := poolTest(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := context.Background()
	a, b := poolTestAccount("a", "1"), poolTestAccount("b", "2")
	accounts := []P115Account{a, b}
	preferred := context.WithValue(ctx, poolCopyPreferenceKey{}, map[string]bool{"2": true})
	l, err := p.Acquire(preferred, "one", "u", accounts, poolProbe)
	if err != nil || l.Account.ID != "b" {
		t.Fatal("copy preference ignored", err)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	l2, err := p.Acquire(preferred, "two", "u", accounts, poolProbe)
	if err != nil || l2.Account.ID != "a" {
		t.Fatal("affinity overrode lower load", err)
	}
	_ = l2.Close()
	// Let the first lease expire. Recent preference has a shorter lifetime.
	now = now.Add(5 * time.Hour)
	if score := p.poolPreferences(ctx, "u", now.UnixMilli())("2"); score != 0 {
		t.Fatal("preference became permanent")
	}
	for i := 0; i < 1100; i++ {
		p.rememberPoolPreference(fmt.Sprint(i), "2")
	}
	if len(p.recent) != 1024 {
		t.Fatal("unbounded preferences", len(p.recent))
	}
	if _, ok := p.recent["1099"]; ok {
		t.Fatal("unhashed user preference")
	}
}

func TestPoolCopyAffinityRejectsAmbiguousAndUnverifiedMaterial(t *testing.T) {
	hash := strings.Repeat("A", 40)
	material := PlaybackMaterial{ID: playbackMaterialID(hash, 10), SHA1: hash, Size: 10, Name: "movie", VerifiedAt: time.Now()}
	cache := &PlaybackCache{doc: PlaybackCacheStatus{Materials: []PlaybackMaterial{material}, Entries: []PlaybackCacheEntry{{Key: "copy", MaterialID: material.ID, UID: "2", Status: "ready", File: p115.DrivePlaybackFile{ID: "f", SHA1: hash, Size: 10}}}}}
	cache.rebuildPlaybackIndexLocked()
	ctx := cache.WithPoolCopyPreference(context.Background(), "", hash)
	if uids, _ := ctx.Value(poolCopyPreferenceKey{}).(map[string]bool); !uids["2"] {
		t.Fatal("known copy not preferred")
	}
	cache.doc.Materials = append(cache.doc.Materials, PlaybackMaterial{ID: playbackMaterialID(hash, 20), SHA1: hash, Size: 20, VerifiedAt: time.Now()})
	cache.rebuildPlaybackIndexLocked()
	if cache.WithPoolCopyPreference(context.Background(), "", hash).Value(poolCopyPreferenceKey{}) != nil {
		t.Fatal("ambiguous hint accepted")
	}
	cache.doc.Materials = cache.doc.Materials[:1]
	cache.doc.Materials[0].VerifiedAt = time.Time{}
	cache.rebuildPlaybackIndexLocked()
	if cache.WithPoolCopyPreference(context.Background(), "", hash).Value(poolCopyPreferenceKey{}) != nil {
		t.Fatal("unverified hint accepted")
	}
}

func TestPoolMetricsBoundedAndRecentQuantiles(t *testing.T) {
	p, _ := poolTest(t)
	for i := 0; i < 300; i++ {
		p.ObserveRedirect(time.Duration(i)*time.Millisecond, i%2 == 0, 1)
	}
	m := p.Metrics()
	if m.Requests != 300 || m.Succeeded != 150 || m.Failed != 150 || m.Switches != 300 || m.Samples != 256 || m.P50MS != 171 || m.P95MS != 287 {
		t.Fatal(m)
	}
}

func TestPoolOldCommitCannotClearFailureAndDirectoryIsolation(t *testing.T) {
	p, _ := poolTest(t)
	ctx := context.Background()
	a := poolTestAccount("a", "1")
	l, err := p.Acquire(ctx, "one", "u", []P115Account{a}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := p.report(ctx, a, p115.ErrPlaybackDirectory); err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("late result issued after newer failure", err)
	}
	if p.AccountStatus(a).State != "invalid_directory" {
		t.Fatal("late commit erased newer failure")
	}
	if _, err := p.Acquire(ctx, "two", "u", []P115Account{a}, poolProbe); !errors.Is(err, ErrPoolInvalid) {
		t.Fatal(err)
	}
}

func TestPoolEventHistoryBoundedAndRestartDedupe(t *testing.T) {
	p, st := poolTest(t)
	ctx := context.Background()
	for i := 0; i < 120; i++ {
		if err := p.report(ctx, poolTestAccount(fmt.Sprint(i), fmt.Sprint(i)), p115.ErrShareLoginRequired); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.Events()) != 100 {
		t.Fatal("unbounded event history")
	}
	restarted, err := NewDynamicPool(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.report(ctx, poolTestAccount("119", "119"), p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	if restarted.doc.EventSequence != 120 {
		t.Fatal("restart repeated alert")
	}
	settings := restarted.Settings()
	settings.NotificationBotID = "bot"
	if err := restarted.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := restarted.report(ctx, poolTestAccount("119", "119"), nil); err != nil {
		t.Fatal(err)
	}
	last := restarted.Events()[99]
	if last.BotID != "bot" || last.State != "ready" {
		t.Fatal(last)
	}
}
