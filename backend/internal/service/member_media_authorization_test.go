package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRequestAuthorizationRechecksLocalChangesWithoutProbingDrive(t *testing.T) {
	s, remote, owner, child := childFixture(t)
	ctx := context.Background()
	calls := 0
	s.checker = healthCheckerFunc(func(context.Context, string) (bool, error) { calls++; return true, nil })
	u := remote.users[child.EmbyID]
	grant, err := s.BeginMediaAuthorization(ctx, child.EmbyURL, child.EmbyID, false, &u)
	if err != nil {
		t.Fatal(err)
	}
	if err := grant.CheckCurrent(ctx); err != nil {
		t.Fatal(err)
	}
	if err := grant.Revalidate(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("request repeated 115 check", calls)
	}
	updated := child
	updated.ChildRevoked = true
	if err := s.save(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if err := grant.Revalidate(ctx); err == nil {
		t.Fatal("revoked child issued link")
	}
	if err := s.save(ctx, child); err != nil {
		t.Fatal(err)
	}
	r, err := s.accounts.UnbindMember(ctx, s.record(owner))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.save(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := grant.CheckCurrent(ctx); err == nil {
		t.Fatal("unbound owner issued link")
	}
	if calls != 1 {
		t.Fatal("local rejection queried 115")
	}
}

type mediaWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *mediaWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestConcurrentMediaDriveProbesShareOnlyInflightWork(t *testing.T) {
	for _, cancelWaiter := range []bool{false, true} {
		t.Run(map[bool]string{false: "share", true: "cancel-waiter"}[cancelWaiter], func(t *testing.T) {
			s, _, _ := memberFixture(t)
			main := memberTestPrimary(t, s)
			a, _ := s.accounts.Get(main)
			entered, release := make(chan struct{}), make(chan struct{})
			calls := 0
			s.checker = healthCheckerFunc(func(ctx context.Context, _ string) (bool, error) {
				calls++
				if calls == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return false, ctx.Err()
					}
				}
				return true, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			leader := make(chan error, 1)
			go func() { leader <- s.checkMediaDrive(ctx, a) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("leader not started")
			}
			waiterCtx, cancelFollower := context.WithCancel(ctx)
			defer cancelFollower()
			observed := &mediaWaitContext{Context: waiterCtx, waiting: make(chan struct{})}
			follower := make(chan error, 1)
			go func() { follower <- s.checkMediaDrive(observed, a) }()
			select {
			case <-observed.waiting:
			case <-ctx.Done():
				t.Fatal("waiter not attached")
			}
			if cancelWaiter {
				cancelFollower()
				if err := <-follower; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			close(release)
			if err := <-leader; err != nil {
				t.Fatal(err)
			}
			if !cancelWaiter {
				if err := <-follower; err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 {
				t.Fatal("concurrent work duplicated", calls)
			}
			if err := s.checkMediaDrive(ctx, a); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal("completed success was cached")
			}
		})
	}
}

func TestRequestAuthorizationRejectsChangedCredentialsAndStaleSelection(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	main := memberTestPrimary(t, s)
	if err := s.SetSource(ctx, id, "primary", ""); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	u := remote.users[r.EmbyID]
	a, _ := s.accounts.Get(main)
	grant, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, false, &u)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.MatchesSelection(a, nil) {
		t.Fatal("valid snapshot rejected")
	}
	cookie := "UID=888_A; SEID=new"
	if err := s.accounts.Update(main, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if err := grant.CheckCurrent(ctx); err == nil {
		t.Fatal("credential change missed")
	}
	grant, err = s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, false, &u)
	if err != nil {
		t.Fatal(err)
	}
	if grant.MatchesSelection(a, nil) {
		t.Fatal("old request snapshot accepted")
	}
}

func TestSlowRequestRechecksEmbyAndNewRequestDoesNotReuseAuthorization(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	r := s.record(id)
	grant, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	remote.users[r.EmbyID].Policy["EnableMediaPlayback"] = false
	grant.checkedAt = time.Now().Add(-6 * time.Second)
	if err := grant.Revalidate(ctx); err == nil {
		t.Fatal("slow request ignored Emby denial")
	}
	if _, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, false, nil); err == nil {
		t.Fatal("new request reused grant")
	}
}
