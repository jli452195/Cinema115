package service

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/emby"
)

type healthCheckerFunc func(context.Context, string) (bool, error)

func (f healthCheckerFunc) CheckCookie(ctx context.Context, cookie string) (bool, error) {
	return f(ctx, cookie)
}

func TestDriveHealthSharedRefreshAndConfigChanges(t *testing.T) {
	ctx := context.Background()
	s, remote, owner := memberFixture(t)
	main := memberTestPrimary(t, s)
	if err := s.SetSource(ctx, owner, "primary", ""); err != nil {
		t.Fatal(err)
	}
	u := emby.ManagedUser{ID: strings.Repeat("d", 32), Name: "second", Policy: map[string]any{"EnableMediaPlayback": true}}
	remote.users[u.ID] = u
	if _, err := s.ObserveUser(ctx, s.emby.snapshot(), u); err != nil {
		t.Fatal(err)
	}
	second := embyProfileID("server", u.ID)
	calls := 0
	s.checker = healthCheckerFunc(func(context.Context, string) (bool, error) { calls++; return true, nil })
	writes := remote.policyWrites
	s.RequestDriveRefresh()
	s.refreshMissingDriveHealth(ctx)
	first := s.DriveHealth(s.record(owner))
	if calls != 1 || first.State != "ready" || first.CheckedAt == "" || first != s.DriveHealth(s.record(second)) {
		t.Fatalf("shared health not reused: %d %+v", calls, first)
	}
	a, _ := s.accounts.Get(main)
	if err := s.accounts.Update(main, a.Revision, "renamed", nil); err != nil {
		t.Fatal(err)
	}
	s.refreshMissingDriveHealth(ctx)
	if calls != 1 || s.DriveHealth(s.record(owner)) != first {
		t.Fatal("rename invalidated health")
	}
	a, _ = s.accounts.Get(main)
	cookie := "UID=888_A; SEID=updated"
	if err := s.accounts.Update(main, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if s.DriveHealth(s.record(owner)).State != "pending" {
		t.Fatal("old credentials still reported healthy")
	}
	s.refreshMissingDriveHealth(ctx)
	if calls != 2 {
		t.Fatal("new credentials were not checked")
	}
	if err := s.CheckUserPlayback(ctx, second); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || s.DriveHealth(s.record(owner)) != s.DriveHealth(s.record(second)) {
		t.Fatal("single check not shared")
	}
	a, _ = s.accounts.Get(main)
	cookie = ""
	if err := s.accounts.Update(main, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if s.DriveHealth(s.record(owner)).State != "unavailable" {
		t.Fatal("cleared credentials stayed healthy")
	}
	s.refreshMissingDriveHealth(ctx)
	if calls != 3 || remote.policyWrites != writes {
		t.Fatal("refresh probed missing credentials or wrote Emby policy")
	}
}

func TestDriveHealthDiscardsLateResult(t *testing.T) {
	s, _, owner := memberFixture(t)
	main := memberTestPrimary(t, s)
	ctx := context.Background()
	if err := s.SetSource(ctx, owner, "primary", ""); err != nil {
		t.Fatal(err)
	}
	s.checker = healthCheckerFunc(func(context.Context, string) (bool, error) {
		a, _ := s.accounts.Get(main)
		cookie := "UID=888_A; SEID=replaced-during-check"
		if err := s.accounts.Update(main, a.Revision, a.Name, &cookie); err != nil {
			t.Fatal(err)
		}
		return true, nil
	})
	s.RequestDriveRefresh()
	s.refreshMissingDriveHealth(ctx)
	if s.DriveHealth(s.record(owner)).State != "pending" {
		t.Fatal("late result accepted for changed credentials")
	}
}

func TestDriveHealthWorkerObservesAccountUpdates(t *testing.T) {
	s, _, owner := memberFixture(t)
	main := memberTestPrimary(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.SetSource(ctx, owner, "primary", ""); err != nil {
		cancel()
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.checker = healthCheckerFunc(func(context.Context, string) (bool, error) { calls.Add(1); return true, nil })
	done := make(chan struct{})
	go func() { defer close(done); s.runDriveHealth(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("worker did not exit")
		}
	})
	waitReady := func(count int32) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			// Observe passively: DriveHealth itself wakes the worker and would mask a lost notification.
			s.driveHealthMu.Lock()
			state := s.driveHealth[main].view.State
			s.driveHealthMu.Unlock()
			if calls.Load() >= count && state == "ready" {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("automatic health refresh missing")
	}
	waitReady(1)
	a, _ := s.accounts.Get(main)
	cookie := "UID=888_A; SEID=new-login"
	if err := s.accounts.Update(main, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	waitReady(2)
}

func TestDriveHealthPersonalDirectoryAndUnbindIsolation(t *testing.T) {
	s, _, owner, child := childFixture(t)
	ctx := context.Background()
	memberTestPrimary(t, s)
	if err := s.SetSource(ctx, owner, "primary", ""); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.checker = healthCheckerFunc(func(context.Context, string) (bool, error) { calls++; return true, nil })
	s.RequestDriveRefresh()
	s.refreshMissingDriveHealth(ctx)
	primary := s.DriveHealth(s.record(owner))
	if calls != 2 || primary.State != "ready" || s.DriveHealth(child).State != "ready" {
		t.Fatal("child did not retain independent personal source")
	}
	personal, err := s.healthAccount(child)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.accounts.SetMemberDirectory(ctx, s.record(owner), personal.ID, personal.Revision, "456")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.save(ctx, r); err != nil {
		t.Fatal(err)
	}
	if s.DriveHealth(child).State != "pending" || s.DriveHealth(r) != primary {
		t.Fatal("directory edit invalidated wrong account")
	}
	s.refreshMissingDriveHealth(ctx)
	if calls != 3 || s.DriveHealth(child).State != "ready" {
		t.Fatal("directory was not rechecked")
	}
	r, err = s.accounts.UnbindMember(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.save(ctx, r); err != nil {
		t.Fatal(err)
	}
	if s.DriveHealth(child).State != "unavailable" || s.DriveHealth(r) != primary {
		t.Fatal("unbind fell back or invalidated primary")
	}
}

func TestDriveHealthRefreshDuringProbeDiscardsResult(t *testing.T) {
	s, _, owner := memberFixture(t)
	ctx := context.Background()
	memberTestPrimary(t, s)
	if err := s.SetSource(ctx, owner, "primary", ""); err != nil {
		t.Fatal(err)
	}
	s.checker = healthCheckerFunc(func(context.Context, string) (bool, error) { s.RequestDriveRefresh(); return true, nil })
	s.refreshMissingDriveHealth(ctx)
	if s.DriveHealth(s.record(owner)).State != "pending" {
		t.Fatal("in-flight result overwrote explicit refresh")
	}
}
