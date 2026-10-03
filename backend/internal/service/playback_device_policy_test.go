package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func enableHeartbeatPolicy(t *testing.T, s *MemberPlaybackService) {
	t.Helper()
	if err := s.UserPlayback.ConfigureDevicePolicy(context.Background(), model.PlaybackDevicePolicy{Mode: DevicePolicyHeartbeat, SilenceSeconds: 90}); err != nil {
		t.Fatal(err)
	}
}

func TestDevicePolicyBoundaryHeartbeatAndTakeover(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	enableHeartbeatPolicy(t, s)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	e := deviceSlotEvent(s, a)
	*now = now.Add(89 * time.Second)
	if _, err := deviceSlotPrepare(t, s, r, accounts, "tv", "B"); !errors.Is(err, ErrPlaybackDeviceConflict) {
		t.Fatal("early takeover", err)
	}
	if err := s.ApplyPlaybackEvent(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(89 * time.Second)
	if _, err := deviceSlotPrepare(t, s, r, accounts, "tv", "B"); !errors.Is(err, ErrPlaybackDeviceConflict) {
		t.Fatal("heartbeat ignored", err)
	}
	*now = now.Add(time.Second)
	b, err := deviceSlotPrepare(t, s, r, accounts, "tv", "B")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPlaybackEvent(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	if err := b.commitDevicePlayback(ctx); !errors.Is(err, ErrPlaybackDeviceConflict) {
		t.Fatal("late heartbeat did not cancel", err)
	}
	_ = b.Close()
	_ = b.userLease.Close()
	if !a.userLease.Current() {
		t.Fatal("failed challenger retired incumbent")
	}
	*now = now.Add(90 * time.Second)
	b = deviceSlotIssue(t, s, r, accounts, "tv", "B")
	if a.userLease.Current() || !b.userLease.Current() {
		t.Fatal("ownership not transferred")
	}
	for _, stopped := range []bool{false, true} {
		if err := s.ApplyPlaybackEvent(ctx, e, stopped); err != nil {
			t.Fatal(err)
		}
	}
	if !b.userLease.Current() || s.Pool.doc.Leases[b.poolLease.Record.Key].Stopped {
		t.Fatal("late A changed B")
	}
	if _, ok := s.Pool.doc.Leases[a.poolLease.Record.Key]; ok {
		t.Fatal("retired capacity retained")
	}
	if b.account.UID != a.account.UID {
		t.Fatal("healthy UID was not inherited")
	}
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	if _, err := deviceSlotPrepare(t, s, r, accounts, "phone", "A"); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("old URL revived", err)
	}
	if _, err := deviceSlotPrepare(t, s, r, accounts, "phone", "new-A"); !errors.Is(err, ErrPlaybackDeviceConflict) {
		t.Fatal("new A stole B", err)
	}
	t.Log("89 s denied; heartbeat resets window; 90 s permits preparation; late heartbeat cancels; successful takeover transfers healthy UID with no residual")
}

func TestDevicePolicyPrefetchRefreshAndStop(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	enableHeartbeatPolicy(t, s)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	first := a.userLease.Record.FirstIssuedAt
	for i := 0; i < 20; i++ {
		*now = now.Add(5 * time.Second)
		a = deviceSlotIssue(t, s, r, accounts, "phone", "A")
		b := deviceSlotIssue(t, s, r, accounts, "phone", fmt.Sprintf("prefetch-%d", i))
		if b.userLease.Record.FirstIssuedAt != first || a.userLease.Record.FirstIssuedAt != first {
			t.Fatal("URL renewed device protection")
		}
		if len(s.UserPlayback.List()) > 2 {
			t.Fatal("unbounded prefetch")
		}
	}
	b := deviceSlotIssue(t, s, r, accounts, "tv", "TV")
	if err := s.ApplyPlaybackEvent(ctx, deviceSlotEvent(s, b), true); err != nil {
		t.Fatal(err)
	}
	// Stopping the last child permits an immediate new device, independent of residuals.
	deviceSlotIssue(t, s, r, accounts, "tablet", "tablet")
	t.Log("20 refresh/prefetch cycles reuse one device slot and do not extend initial 90 s grace; normal stop releases admission")
}

func TestDevicePolicyConcurrentPreparations(t *testing.T) {
	s, r, _, _ := deviceSlotFixture(t)
	enableHeartbeatPolicy(t, s)
	var wg sync.WaitGroup
	winners := make(chan *UserPlaybackLease, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := handoffRequest(fmt.Sprint(i))
			q.Device = fmt.Sprint(i)
			l, err := s.UserPlayback.Acquire(context.Background(), r, q)
			if err == nil {
				winners <- l
			} else if !errors.Is(err, ErrPlaybackDeviceConflict) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	close(winners)
	count := 0
	for l := range winners {
		count++
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 || len(s.UserPlayback.List()) != 0 {
		t.Fatal("admission race", count)
	}
}

func TestDevicePolicyConfigurationRestartAndFailure(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	ctx := context.Background()
	if s.UserPlayback.DevicePolicy().Mode != DevicePolicyParallel {
		t.Fatal("default changed")
	}
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	enableHeartbeatPolicy(t, s)
	if err := a.commitDevicePlayback(ctx); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("old policy grant accepted", err)
	}
	if _, err := deviceSlotPrepare(t, s, r, accounts, "", "unknown"); !errors.Is(err, ErrPlaybackDeviceIdentity) {
		t.Fatal(err)
	}
	for _, v := range []model.PlaybackDevicePolicy{{Mode: "unknown"}, {Mode: DevicePolicyHeartbeat, SilenceSeconds: 29}, {Mode: DevicePolicyHeartbeat, SilenceSeconds: 181}} {
		if err := s.UserPlayback.ConfigureDevicePolicy(ctx, v); err == nil {
			t.Fatal("invalid setting")
		}
	}
	*now = now.Add(90 * time.Second)
	b, err := deviceSlotPrepare(t, s, r, accounts, "tv", "B")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollback.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SavePlaybackState(ctx, s.UserPlayback.doc, s.Pool.doc); err != nil {
		t.Fatal(err)
	}
	s.db = db
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `CREATE TRIGGER fail_policy BEFORE UPDATE ON playback_pool BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := b.commitDevicePlayback(ctx); err == nil {
		t.Fatal("failed transaction accepted")
	}
	u, err := db.UserPlayback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := u.Leases[a.userLease.Record.Key]; !ok {
		t.Fatal("persisted incumbent lost")
	}
	if _, ok := s.UserPlayback.doc.Leases[a.userLease.Record.Key]; !ok {
		t.Fatal("memory incumbent lost")
	}
	if _, err := conn.ExecContext(ctx, "DROP TRIGGER fail_policy"); err != nil {
		t.Fatal(err)
	}
	if err := b.commitDevicePlayback(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewUserPlayback(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = s.UserPlayback.now
	q := handoffRequest("C")
	q.Device = "tablet"
	if _, err := restarted.Acquire(ctx, r, q); !errors.Is(err, ErrPlaybackDeviceConflict) {
		t.Fatal("restart lost ownership", err)
	}
	if err := restarted.ConfigureDevicePolicy(ctx, model.PlaybackDevicePolicy{Mode: DevicePolicyParallel}); err != nil {
		t.Fatal(err)
	}
	l, err := restarted.Acquire(ctx, r, q)
	if err != nil {
		t.Fatal("parallel rollback failed", err)
	}
	_ = l.Close()
}

func TestDevicePolicyNonPoolAndPing(t *testing.T) {
	for _, source := range []string{"primary", "personal", "personal_shared"} {
		t.Run(source, func(t *testing.T) {
			s, r, _, now := deviceSlotFixture(t)
			r.Source = source
			enableHeartbeatPolicy(t, s)
			ctx := context.Background()
			q := handoffRequest("A")
			q.Device = "phone"
			u, err := s.UserPlayback.Acquire(ctx, r, q)
			if err != nil {
				t.Fatal(err)
			}
			defer u.Close()
			a := &MediaAuthorization{service: s, userLease: u}
			if err := a.commitNonPoolDevicePolicy(ctx); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(80 * time.Second)
			e := s.CapturePlaybackPing("http://emby", r.EmbyID, q.Session)
			if !e.Matched() {
				t.Fatal("ping not matched")
			}
			if err := s.ApplyPlaybackEvent(ctx, e, false); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(20 * time.Second)
			q.Device = "tv"
			q.Session = "B"
			if _, err := s.UserPlayback.Acquire(ctx, r, q); !errors.Is(err, ErrPlaybackDeviceConflict) {
				t.Fatal("ping did not protect", err)
			}
			*now = now.Add(70 * time.Second)
			b, err := s.UserPlayback.Acquire(ctx, r, q)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if err := (&MediaAuthorization{service: s, userLease: b}).commitNonPoolDevicePolicy(ctx); err != nil {
				t.Fatal(err)
			}
			if u.Current() || !b.Current() {
				t.Fatal("nonpool takeover failed")
			}
		})
	}
}

func TestDevicePolicyCapacityCancellationAndIsolation(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	enableHeartbeatPolicy(t, s)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
	*now = now.Add(90 * time.Second)
	b, err := deviceSlotPrepare(t, s, r, accounts[:1], "tv", "B")
	if err != nil {
		t.Fatal(err)
	}
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	if b.poolLease.Record.BorrowedKey != a.poolLease.Record.Key {
		t.Fatal("full UID not borrowed")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := b.commitDevicePlayback(canceled); err == nil {
		t.Fatal("canceled commit accepted")
	}
	_ = b.Close()
	_ = b.userLease.Close()
	if !a.userLease.Current() || len(s.UserPlayback.List()) != 1 {
		t.Fatal("canceled B harmed A")
	}
	q := handoffRequest("unrelated")
	q.Device = "tv"
	for _, other := range []model.MemberPlayback{{ServerID: r.ServerID, UserID: "another", EmbyID: "another"}, {ServerID: "another-server", UserID: r.UserID, EmbyID: r.EmbyID}} {
		l, err := s.UserPlayback.Acquire(ctx, other, q)
		if err != nil {
			t.Fatal("unrelated identity blocked", err)
		}
		_ = l.Close()
	}
	// A pending B must not prevent A refreshing its existing URL, nor may that
	// refresh retire B's pending reservation. A heartbeat can still cancel B.
	b, err = deviceSlotPrepare(t, s, r, accounts, "tv", "B")
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := deviceSlotPrepare(t, s, r, accounts, "phone", "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := refresh.commitDevicePlayback(ctx); err != nil {
		t.Fatal(err)
	}
	if !b.userLease.Current() {
		t.Fatal("incumbent refresh deleted challenger")
	}
	_ = refresh.Close()
	_ = refresh.userLease.Close()
	if err := b.commitDevicePlayback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := refresh.commitDevicePlayback(ctx); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("old inflight grant signed after takeover", err)
	}
}

func TestDevicePolicyPingAmbiguity(t *testing.T) {
	s, r, accounts, _ := deviceSlotFixture(t)
	enableHeartbeatPolicy(t, s)
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	q := handoffRequest("A")
	q.Device = "phone"
	q.Item = "another-movie"
	l, err := s.UserPlayback.Acquire(context.Background(), r, q)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if s.CapturePlaybackPing("http://emby", r.EmbyID, q.Session).Matched() {
		t.Fatal("ambiguous ping selected an item")
	}
	if s.CapturePlaybackPing("http://another", r.EmbyID, a.userLease.Record.AuthSession).Matched() {
		t.Fatal("foreign server accepted")
	}
}
