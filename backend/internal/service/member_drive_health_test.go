package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
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

type personalMembershipChecker struct {
	healthCheckerFunc
	query func(string) (model.P115Summary, bool, error)
}

func (c personalMembershipChecker) QueryMembership(_ context.Context, cookie string) (model.P115Summary, bool, error) {
	return c.query(cookie)
}

func TestPersonalDriveStatusUsesBoundAccountAndIssuedTargets(t *testing.T) {
	ctx := context.Background()
	s, _, owner := memberFixture(t)
	memberScan(t, s, owner)
	if err := s.SetSource(ctx, owner, "assigned", ""); err != nil {
		t.Fatal(err)
	}
	r := s.record(owner)
	a, err := s.accounts.Get(r.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	renewed := "UID=" + a.UID + "_A; SEID=renewed"
	query := func(cookie string) (model.P115Summary, bool, error) {
		tier := "vip"
		if cookie == renewed {
			tier = "normal"
		} else if cookie != a.Cookie {
			t.Fatal("查询使用了非个人网盘凭据")
		}
		return model.P115Summary{VIP: tier, CheckedAt: "2026-11-24T00:00:00Z"}, true, nil
	}
	check := healthCheckerFunc(func(context.Context, string) (bool, error) { return true, nil })
	s.checker = personalMembershipChecker{check, query}
	now := time.Now()
	target := model.UserPlaybackTarget{AccountID: a.ID, Source: "personal_shared"}
	s.UserPlayback, err = NewUserPlayback(ctx, &memoryUserPlaybackStore{doc: model.UserPlaybackDocument{
		Leases: map[string]model.UserPlaybackLease{
			"child":     {UserID: "child", Issued: true, ExpiresAt: now.Add(time.Minute).UnixMilli(), Target: target},
			"expired":   {Issued: true, ExpiresAt: now.Add(-time.Minute).UnixMilli(), Target: target},
			"preparing": {ExpiresAt: now.Add(time.Minute).UnixMilli(), Target: target},
			"other":     {UserID: owner, Issued: true, ExpiresAt: now.Add(time.Minute).UnixMilli(), Target: model.UserPlaybackTarget{AccountID: "pool"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.refreshMissingDriveHealth(ctx)
	rows := s.PersonalDriveStatuses()
	if view := s.View(owner); view.Source != "assigned" || view.VIP != "vip" || view.VIPCheckedAt == "" {
		t.Fatalf("个人视图未显示绑定账号会员状态：%+v", view)
	}
	if len(rows) != 1 || rows[0].VIP != "vip" || rows[0].IssuedLinks != 1 || rows[0].DriveStatus.State != "ready" {
		t.Fatalf("管理员快照与签发目标不一致：%+v", rows)
	}
	updated, err := s.accounts.BindMember(ctx, r, r.EmbyName, renewed, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.save(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if view := s.View(owner); view.VIP != "" || view.VIPCheckedAt != "" {
		t.Fatal("续登后继续显示旧会员状态")
	}
	if err := s.CheckPersonalDrive(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if view := s.View(owner); view.VIP != "normal" {
		t.Fatalf("未显示更新后的非会员状态：%+v", view)
	}
	s.checker = personalMembershipChecker{check, func(string) (model.P115Summary, bool, error) {
		return model.P115Summary{}, false, errors.New("资料接口暂不可用")
	}}
	if err := s.CheckPersonalDrive(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if row := s.PersonalDriveStatuses()[0]; row.VIP != "" || row.VIPCheckedAt == "" || row.DriveStatus.State != "ready" {
		t.Fatalf("会员查询失败影响了健康状态：%+v", row)
	}
}
