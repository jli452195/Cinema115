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

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func deviceSlotFixture(t *testing.T) (*MemberPlaybackService, model.MemberPlayback, []P115Account, *time.Time) {
	t.Helper()
	s, _, id := memberFixture(t)
	r := s.record(id)
	r.Source = "assigned"
	now := time.Now()
	s.UserPlayback.now = func() time.Time { return now }
	s.Pool.now = s.UserPlayback.now
	settings := s.Pool.Settings()
	settings.MaxSessions = 1
	if err := s.Pool.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	return s, r, []P115Account{poolTestAccount("a", "7"), poolTestAccount("b", "8"), poolTestAccount("c", "9"), poolTestAccount("d", "10")}, &now
}

func deviceSlotPrepare(t *testing.T, s *MemberPlaybackService, r model.MemberPlayback, accounts []P115Account, device, id string) (*MediaAuthorization, error) {
	t.Helper()
	q := handoffRequest(id)
	q.Device, q.Item = device, "movie-"+id
	u, err := s.UserPlayback.Acquire(context.Background(), r, q)
	if err != nil {
		return nil, err
	}
	q.userGeneration, q.lifecycleKey, q.observationKey = u.Record.Generation, u.Record.EventKey, u.Record.ObservationKey
	q.transferCapacity = u.policyMode == DevicePolicyHeartbeat
	p, err := s.Pool.Acquire(WithPoolRequest(context.Background(), q), "pool-"+id, r.UserID, accounts, poolProbe)
	if err != nil {
		_ = u.Close()
		return nil, err
	}
	a := &MediaAuthorization{service: s, record: r, userLease: u, poolLease: p, account: p.Account}
	t.Cleanup(func() { _ = p.Close(); _ = u.Close() })
	return a, nil
}

func deviceSlotIssue(t *testing.T, s *MemberPlaybackService, r model.MemberPlayback, accounts []P115Account, device, id string) *MediaAuthorization {
	t.Helper()
	a, err := deviceSlotPrepare(t, s, r, accounts, device, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.commitDevicePlayback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.userLease.Close(); err != nil {
		t.Fatal(err)
	}
	return a
}

func deviceSlotEvent(s *MemberPlaybackService, a *MediaAuthorization) PlaybackEvent {
	e := PlaybackEvent{key: a.userLease.Record.EventKey, userGeneration: a.userLease.Record.Generation, poolGenerations: make(map[string]uint64)}
	s.Pool.mu.Lock()
	defer s.Pool.mu.Unlock()
	for key, l := range s.Pool.doc.Leases {
		if l.SessionKey == e.key && l.UserGeneration == e.userGeneration {
			e.poolGenerations[key] = l.Generation
		}
	}
	return e
}

func TestDeviceSlotHundredSwitchesAndStops(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(fmt.Sprint(stopped), func(t *testing.T) {
			s, r, accounts, now := deviceSlotFixture(t)
			first := deviceSlotIssue(t, s, r, accounts, "phone-player", "first")
			uid := first.account.UID
			last := first
			for i := 0; i < 100; i++ {
				if stopped {
					if err := s.ApplyPlaybackEvent(context.Background(), deviceSlotEvent(s, last), true); err != nil {
						t.Fatal(err)
					}
				}
				*now = now.Add(time.Second)
				last = deviceSlotIssue(t, s, r, accounts, "phone-player", fmt.Sprint(i))
				if last.account.UID != uid {
					t.Fatal("healthy device slot changed UID")
				}
				multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
				if len(s.UserPlayback.List()) > 2 || len(s.Pool.doc.Leases) > 2 {
					t.Fatal("URL-only history grew unbounded")
				}
			}
			if !stopped && !first.userLease.Current() {
				t.Fatal("URL-only candidates killed anchor playback")
			}
			if err := s.ApplyPlaybackEvent(context.Background(), deviceSlotEvent(s, last), true); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(6 * time.Minute)
			multiUIDCounts(t, s.Pool, accounts, 0, 0, 0)
			t.Log("100 switches, with/without stops: healthy UID stable; capacity=1; URL-only records<=2; eventual idle release")
		})
	}
}

func TestDeviceSlotPrefetchFailureRestartAndOldEvents(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	oldEvent := deviceSlotEvent(s, a)
	if err := s.ApplyPlaybackEvent(context.Background(), oldEvent, false); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	b, err := deviceSlotPrepare(t, s, r, accounts, "phone", "B")
	if err != nil {
		t.Fatal(err)
	}
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	// A can refresh while B's network work is pending; no device-wide I/O lock.
	refresh, err := deviceSlotPrepare(t, s, r, accounts, "phone", "A")
	if err != nil {
		t.Fatal("current blocked by pending prefetch", err)
	}
	if err := refresh.commitDevicePlayback(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = refresh.Close()
	_ = refresh.userLease.Close()
	_ = b.Close()
	_ = b.userLease.Close()
	if !a.userLease.Current() || len(s.UserPlayback.List()) != 1 {
		t.Fatal("failed B changed A")
	}
	b = deviceSlotIssue(t, s, r, accounts, "phone", "B")
	until := b.userLease.Record.ExpiresAt
	deviceSlotIssue(t, s, r, accounts, "phone", "A")
	if !b.userLease.Current() {
		t.Fatal("current A range refresh evicted prepared B")
	}
	*now = now.Add(10 * time.Second)
	b = deviceSlotIssue(t, s, r, accounts, "phone", "B")
	if b.userLease.Record.ExpiresAt != until {
		t.Fatal("GET renewed unconfirmed candidate indefinitely")
	}
	u, err := NewUserPlayback(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewDynamicPool(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	u.now, p.now = s.UserPlayback.now, s.Pool.now
	s.UserPlayback, s.Pool = u, p
	multiUIDCounts(t, p, accounts, 1, 0, 0)
	if err := s.ApplyPlaybackEvent(context.Background(), oldEvent, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPlaybackEvent(context.Background(), oldEvent, false); err != nil {
		t.Fatal(err)
	}
	if len(u.List()) != 1 || p.doc.Leases[b.poolLease.Record.Key].Stopped {
		t.Fatal("old A event killed B")
	}
	if err := s.ApplyPlaybackEvent(context.Background(), deviceSlotEvent(s, b), false); err != nil {
		t.Fatal(err)
	}
	if u.List()[0].ExpiresAt <= until {
		t.Fatal("confirmed B did not get playback TTL")
	}
	multiUIDCounts(t, p, accounts, 1, 0, 0)
	t.Log("full UID: A + prefetch B share one slot; failed B preserves A; A refresh unblocked; restart and late A events preserve B")
}

func TestDeviceSlotConcurrentAdmissionAndFailedCommit(t *testing.T) {
	s, r, accounts, _ := deviceSlotFixture(t)
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	ctx := context.Background()
	if err := s.ApplyPlaybackEvent(ctx, deviceSlotEvent(s, a), false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			b, err := deviceSlotPrepare(t, s, r, accounts, "phone", fmt.Sprintf("parallel-%d", i))
			if err == nil {
				err = b.commitDevicePlayback(ctx)
				_ = b.Close()
				_ = b.userLease.Close()
			}
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, ErrPoolBusy) {
			t.Fatal(err)
		}
	}
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	if !a.userLease.Current() || len(s.UserPlayback.List()) > 2 {
		t.Fatal("parallel prefetch damaged current/record bounds")
	}
	b, err := deviceSlotPrepare(t, s, r, accounts, "phone", "db-fail")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "slot.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SavePlaybackState(ctx, s.UserPlayback.doc, s.Pool.doc); err != nil {
		t.Fatal(err)
	}
	s.db = db
	connection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `CREATE TRIGGER fail_pool BEFORE UPDATE ON playback_pool BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	before := len(s.UserPlayback.List())
	if err := b.commitDevicePlayback(ctx); err == nil {
		t.Fatal("failed transaction accepted")
	}
	ud, err := db.UserPlayback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !a.userLease.Current() || len(s.UserPlayback.List()) != before || len(ud.Leases) != before || s.Pool.doc.Leases[b.poolLease.Record.Key].Issued {
		t.Fatal("failed commit changed memory or persisted children")
	}
}

func TestDeviceSlotConfirmedMultiSessionAndForcedRelease(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	if err := s.ApplyPlaybackEvent(ctx, deviceSlotEvent(s, a), false); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	b := deviceSlotIssue(t, s, r, accounts, "phone", "B")
	if err := s.ApplyPlaybackEvent(ctx, deviceSlotEvent(s, b), false); err != nil {
		t.Fatal(err)
	}
	if !a.userLease.Current() || !b.userLease.Current() {
		t.Fatal("active formal sessions merged")
	}
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	if err := s.ReleaseUserPlayback(ctx, a.userLease.Record.Key, a.userLease.Record.Generation+1); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale admin release accepted", err)
	}
	if err := s.ReleaseUserPlayback(ctx, a.userLease.Record.Key, a.userLease.Record.Generation); err != nil {
		t.Fatal(err)
	}
	if !b.userLease.Current() {
		t.Fatal("forced release affected another child")
	}
	if _, err := deviceSlotPrepare(t, s, r, accounts, "phone", "A"); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("forced child reissued", err)
	}
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	t.Log("confirmed concurrent sessions retain independent lifecycle; same authenticated device has one logical UID slot, not one CDN connection")
}

func TestDeviceSlotPollingConfirmationAfterRestart(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	*now = now.Add(40 * time.Second)
	b := deviceSlotIssue(t, s, r, accounts, "phone", "B")
	u, err := NewUserPlayback(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewDynamicPool(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	u.now, p.now = s.UserPlayback.now, s.Pool.now
	s.UserPlayback, s.Pool = u, p
	listed := emby.PlaybackSession{UserID: r.EmbyID, PlaySessionID: "B", LastPlaybackCheckIn: *now, LastActivityDate: *now}
	listed.NowPlayingItem.ID = "movie-B"
	remote := &phaseOneRemote{memberEmbyClient: s.NewEmby("", ""), sessions: []emby.PlaybackSession{listed}}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	s.reconcilePoolSessions(ctx)
	if len(u.List()) != 1 || u.List()[0].Generation != b.userLease.Record.Generation || u.doc.Stopped[a.userLease.Record.EventKey] == 0 {
		t.Fatal("internal poll identity lost after public redaction/restart")
	}
	multiUIDCounts(t, p, accounts, 1, 0, 0)
	*now = now.Add(4 * time.Minute)
	listed.LastPlaybackCheckIn = *now
	remote.sessions = []emby.PlaybackSession{listed}
	s.reconcilePoolSessions(ctx)
	if len(u.List()) != 1 || p.AccountStatus(b.account).Sessions != 1 {
		t.Fatal("confirmed candidate did not survive original 90-second deadline")
	}
}

func TestDeviceSlotOldInflightCannotIssueAfterUIDSwitch(t *testing.T) {
	s, r, accounts, _ := deviceSlotFixture(t)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	refresh, err := deviceSlotPrepare(t, s, r, accounts, "phone", "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.report(ctx, a.account, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	b := deviceSlotIssue(t, s, r, accounts, "phone", "B")
	if b.account.UID == a.account.UID {
		t.Fatal("failed UID was reused")
	}
	if err := refresh.commitDevicePlayback(ctx); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("late old UID result issued", err)
	}
	if refresh.poolLease.Current() {
		t.Fatal("old UID residual remained issuable")
	}
	multiUIDCounts(t, s.Pool, accounts, 2, 0, 1)
}

func TestDeviceSlotOtherDevicesAndUnknownIdentity(t *testing.T) {
	s, r, accounts, _ := deviceSlotFixture(t)
	settings := s.Pool.Settings()
	settings.MaxSessions = 2
	if err := s.Pool.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	deviceSlotIssue(t, s, r, accounts, "phone", "phone")
	deviceSlotIssue(t, s, r, accounts, "tv", "tv")
	multiUIDCounts(t, s.Pool, accounts, 2, 0, 0)
	// Untrusted/missing identity retains existing per-session accounting.
	p, _ := poolTest(t)
	for _, id := range []string{"one", "two"} {
		l, err := p.Acquire(context.Background(), id, "same-user", accounts, poolProbe)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = l.Close()
	}
	multiUIDCounts(t, p, accounts, 2, 0, 0)
	if p.AccountStatus(accounts[0]).Sessions != 2 {
		t.Fatal("same user sessions merged or moved to another UID")
	}
}

func TestDeviceSlotCrossUIDResidualBoundAndNoRollback(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	a := deviceSlotIssue(t, s, r, accounts, "phone", "A")
	for i := 0; i < 2; i++ {
		if err := s.Pool.report(context.Background(), a.account, p115.ErrShareLoginRequired); err != nil {
			t.Fatal(err)
		}
		a = deviceSlotIssue(t, s, r, accounts, "phone", fmt.Sprintf("switch-%d", i))
		multiUIDCounts(t, s.Pool, accounts, i+2, 0, i+1)
	}
	if err := s.Pool.report(context.Background(), a.account, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceSlotPrepare(t, s, r, accounts, "phone", "fourth"); !errors.Is(err, ErrPoolBusy) {
		t.Fatal("unbounded UID residual chain", err)
	}
	*now = now.Add(121 * time.Second)
	a = deviceSlotIssue(t, s, r, accounts, "phone", "recovered")
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	if a.account.UID == "" {
		t.Fatal("missing target")
	}
	t.Log("cross UID: current + at most 2 residual UIDs; fourth UID blocked until residual expiry; no primary fallback")
}
