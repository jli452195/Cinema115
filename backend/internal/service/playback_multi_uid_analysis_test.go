package service

// Isolated characterization tests: no live accounts or external writes.
// Residual assertions describe current behavior, not the proposed device-slot model.
import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func multiUIDCounts(t *testing.T, p *DynamicPool, accounts []P115Account, sessions, preparing, residual int) {
	t.Helper()
	var gotSessions, gotPreparing, gotResidual int
	seen := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		if seen[a.UID] {
			continue
		}
		seen[a.UID] = true
		s := p.AccountStatus(a)
		gotSessions += s.Sessions
		gotPreparing += s.Preparing
		gotResidual += s.Residual
	}
	if gotSessions != sessions || gotPreparing != preparing || gotResidual != residual {
		t.Fatalf("slots/preparing/residual=%d/%d/%d, want %d/%d/%d", gotSessions, gotPreparing, gotResidual, sessions, preparing, residual)
	}
}

type multiUIDMaterialFake struct {
	*materialFake
	onRead func()
	reads  int
}

func (f *multiUIDMaterialFake) PlaybackPickCodeSource(ctx context.Context, scope, pc, cookie string) (*p115.DownloadInfo, error) {
	f.reads++
	f.onRead()
	if f.reads < 3 {
		return nil, &p115.HTTPError{Status: 503}
	}
	return f.materialFake.PlaybackPickCodeSource(ctx, scope, pc, cookie)
}

func TestMultiUIDAnalysisDonorRotationOnlyBillsTarget(t *testing.T) {
	cache, fast, selected := setupFastCache(t)
	base := &materialFake{fastCacheFake: fast}
	cache.client = base
	ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, DirectLinkKey("multi-uid-origin"))
	load := func() (*p115.DownloadInfo, error) {
		return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
	}
	donors := make([]P115Account, 0, 3)
	for i := 0; i < 3; i++ {
		a := poolTestAccount(fmt.Sprintf("donor-%d", i), fmt.Sprint(22+i))
		donors = append(donors, a)
		copySelection := *selected
		copySelection.binding.AccountID, copySelection.uid, copySelection.cookie = a.ID, a.UID, a.Cookie
		if _, err := cache.resolveInstant(ctx, &copySelection, "source", "player", &PlaybackResolution{}, load); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := poolTest(t)
	cache.Members = &MemberPlaybackService{Pool: p}
	target := poolTestAccount("target", "33")
	l, err := p.Acquire(ctx, "same-play-session", "same-emby-user", []P115Account{target}, poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	fake := &multiUIDMaterialFake{materialFake: base, onRead: func() {
		multiUIDCounts(t, p, donors, 0, 0, 0)
		multiUIDCounts(t, p, []P115Account{target}, 1, 1, 0)
	}}
	base.challenge = true
	cache.client = fake
	targetSelection := *selected
	targetSelection.binding.AccountID, targetSelection.uid, targetSelection.cookie = target.ID, target.UID, target.Cookie
	targetSelection.donors = donors
	if _, err := cache.resolveInstant(ctx, &targetSelection, "", "player", &PlaybackResolution{}, func() (*p115.DownloadInfo, error) {
		t.Fatal("known donors should avoid primary source reads")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if fake.reads != 3 || fast.uploads != 4 {
		t.Fatalf("donor attempts=%d uploads=%d", fake.reads, fast.uploads)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	multiUIDCounts(t, p, donors, 0, 0, 0)
	multiUIDCounts(t, p, []P115Account{target}, 1, 0, 0)
	targetCopies := 0
	for _, e := range cache.Status().Entries {
		if e.UID == target.UID {
			targetCopies++
			if e.AccountID != target.ID {
				t.Fatal("donor changed target ownership")
			}
		}
	}
	if targetCopies != 1 {
		t.Fatal("expected one persisted target copy", targetCopies)
	}
	t.Log("3 donor UIDs tried (2 failures, 1 success): donors=0 slots; playback target=1 slot")
}

func TestMultiUIDAnalysisUnissuedFailuresReleaseSlots(t *testing.T) {
	p, _ := poolTest(t)
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2"), poolTestAccount("c", "3")}
	calls := 0
	l, err := p.Acquire(context.Background(), "session", "user", accounts, func(context.Context, P115Account) error {
		calls++
		multiUIDCounts(t, p, accounts, 1, 1, 0)
		if calls < 3 {
			return p115.ErrShareLoginRequired
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if err := l.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("expected three target probes", calls)
	}
	multiUIDCounts(t, p, accounts, 1, 0, 0)
	t.Log("2 failed targets before issuance + 1 successful target: total=1 slot, residual=0")
}

func TestMultiUIDAnalysisIssuedFailoverAccumulatesResidual(t *testing.T) {
	p, _ := poolTest(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2"), poolTestAccount("c", "3")}
	failed := make(map[string]bool, 2)
	for i := 0; i < 3; i++ {
		l, err := p.Acquire(context.Background(), "same-session", "same-user", accounts, func(_ context.Context, a P115Account) error {
			if failed[a.UID] {
				return p115.ErrShareLoginRequired
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		multiUIDCounts(t, p, accounts, i+1, 0, i)
		failed[l.Account.UID] = true
	}
	now = now.Add(3 * time.Minute)
	multiUIDCounts(t, p, accounts, 1, 0, 0)
	for key, lease := range p.doc.Leases {
		if key != lease.Key && (!lease.Stopped || lease.ExpiresAt > now.UnixMilli()) {
			t.Fatal("unexpected residual expiry")
		}
	}
	t.Log("same issued session switches across 3 UIDs: initially active=1 residual=2; after 3 minutes residual=0, active=1")
	now = now.Add(time.Duration(p.Settings().LeaseMinutes) * time.Minute)
	multiUIDCounts(t, p, accounts, 0, 0, 0)
}

func TestMultiUIDAnalysisSameUserSeparateSessions(t *testing.T) {
	p, _ := poolTest(t)
	settings := p.Settings()
	settings.MaxSessions = 1
	if err := p.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2"), poolTestAccount("c", "3")}
	for i := 0; i < 3; i++ {
		l, err := p.Acquire(context.Background(), fmt.Sprintf("session-%d", i), "same-user", accounts, poolProbe)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	multiUIDCounts(t, p, accounts, 3, 0, 0)
	t.Log("one user, 3 different session keys, capacity 1 per UID: 3 UIDs x 1 slot")
}

func TestMultiUIDAnalysisRapidSameDeviceKeepsOneSlot(t *testing.T) {
	s, r, _, _, now := handoffFixture(t)
	// The fixture has already issued one link for this trusted device on UID 7.
	*now = now.Add(-35 * time.Second)
	accounts := []P115Account{poolTestAccount("pool", "7"), poolTestAccount("b", "8"), poolTestAccount("c", "9")}
	for i := 0; i < 2; i++ {
		q := handoffRequest(fmt.Sprintf("rapid-%d", i))
		q.Item = fmt.Sprintf("movie-%d", i)
		u, err := s.UserPlayback.Acquire(context.Background(), r, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = u.Close() })
		if u.handoff != nil {
			t.Fatal("expected current rapid-switch handoff limitation")
		}
		q.lifecycleKey, q.userGeneration = u.Record.EventKey, u.Record.Generation
		ctx := WithPoolRequest(context.Background(), q)
		l, err := s.Pool.Acquire(ctx, fmt.Sprintf("rapid-pool-%d", i), r.UserID, accounts, poolProbe)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		if err := l.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := u.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		if err := u.Close(); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(5 * time.Second)
	}
	multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	t.Log("same authenticated device/user/player switches every 5 seconds without stop events: one device slot")
}

func TestMultiUIDAnalysisDonorAndUncertainErrorsCannotSwitchTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"donor-read", &p115.HTTPError{Status: 503}},
		{"uncertain-write", &playbackTargetError{stage: "upload", err: p115.ErrInstantUncertain}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolTest(t)
			accounts := []P115Account{poolTestAccount("a", "1"), poolTestAccount("b", "2")}
			l, err := p.Acquire(context.Background(), "session", "user", accounts, poolProbe)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = l.Close() })
			a := &MediaAuthorization{poolLease: l, account: l.Account}
			retry, err := a.PoolTargetFailed(context.Background(), tc.err)
			if err != nil || retry || !l.Current() {
				t.Fatal("source/uncertain failure switched playback target", retry, err)
			}
			multiUIDCounts(t, p, accounts, 1, 1, 0)
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			multiUIDCounts(t, p, accounts, 0, 0, 0)
		})
	}
}
