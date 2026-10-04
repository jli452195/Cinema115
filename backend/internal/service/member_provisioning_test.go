package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"strm115/backend/internal/client/emby"
	"sync"
	"testing"
	"time"
)

type provisioningRemote struct {
	*memberTestRemote
	stale       int
	absent      bool
	creates     int
	passwords   int
	afterEnable func()
}

func (f *provisioningRemote) CreateManagedUser(ctx context.Context, name string) (emby.ManagedUser, error) {
	f.creates++
	if f.absent {
		return emby.ManagedUser{}, errors.New("connection lost before response")
	}
	return f.memberTestRemote.CreateManagedUser(ctx, name)
}
func (f *provisioningRemote) SetManagedPolicy(ctx context.Context, id string, p map[string]any) error {
	err := f.memberTestRemote.SetManagedPolicy(ctx, id, p)
	if p["EnableMediaPlayback"] == true && f.afterEnable != nil {
		f.afterEnable()
	}
	f.stale = 1
	return err
}
func (f *provisioningRemote) ManagedUser(ctx context.Context, id string) (emby.ManagedUser, error) {
	u, err := f.memberTestRemote.ManagedUser(ctx, id)
	if f.stale > 0 {
		f.stale--
		u.Policy = map[string]any{}
	}
	return u, err
}
func (f *provisioningRemote) SetManagedPassword(ctx context.Context, id, password string) error {
	f.passwords++
	return f.memberTestRemote.SetManagedPassword(ctx, id, password)
}

func provisioningInvite(t *testing.T, s *MemberPlaybackService, source string) (*PortalAuth, GeneratedPortalInvite) {
	t.Helper()
	a := NewPortalAuth(s.db, "admin", "password123", "secret")
	a.Members = s
	invites, err := a.GenerateInvites(context.Background(), "admin", "", 1, 7, source)
	if err != nil {
		t.Fatal(err)
	}
	return a, invites[0]
}

func TestProvisioningConfirmsLostResponsesAndStaleReads(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(map[bool]string{false: "invitation", true: "child"}[child], func(t *testing.T) {
			s, f, owner := memberFixture(t)
			ctx := context.Background()
			if child {
				memberScan(t, s, owner)
				if err := s.SetChildLimit(ctx, owner, 1); err != nil {
					t.Fatal(err)
				}
			}
			remote := &provisioningRemote{memberTestRemote: f}
			s.NewEmby = func(string, string) memberEmbyClient { return remote }
			f.failCreate, f.policyResponseLost, f.renameResponseLost = true, true, true
			if child {
				if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "canary-child-lost", Name: "canary-child", Password: "password123"}); err != nil {
					t.Fatal(err)
				}
				view, err := s.Children(ctx, owner)
				if err != nil || len(view.Items) != 1 || !view.Items[0].Completed {
					t.Fatal(view, err)
				}
			} else {
				a, invite := provisioningInvite(t, s, "personal")
				u, err := a.Register(ctx, "canary-user", "password123", invite.Code)
				if err != nil || !s.record(u.ID).Provisioned {
					t.Fatal(u, err)
				}
			}
			if remote.creates != 1 || remote.passwords != 1 {
				t.Fatal("writes replayed", remote.creates, remote.passwords)
			}
		})
	}
}

func TestProvisioningRegistrationActivationResumesAfterRestart(t *testing.T) {
	s, f, _ := memberFixture(t)
	ctx := context.Background()
	remote := &provisioningRemote{memberTestRemote: f}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	a, invite := provisioningInvite(t, s, "personal")
	f.failEnable = true
	if _, err := a.Register(ctx, "pending-reader", "password123", invite.Code); err == nil {
		t.Fatal("activation failure reported success")
	}
	hash, _ := inviteHash(invite.Code)
	job, err := s.db.Registration(ctx, hash)
	if err != nil || job.ActivationPolicy == nil {
		t.Fatal(job, err)
	}
	id := embyProfileID(job.ServerID, job.EmbyID)
	if s.record(id).Provisioned {
		t.Fatal("pending user completed")
	}
	if _, err := s.AdminMemberDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	if s.record(id).Source != "personal" {
		t.Fatal("pending invitation became primary")
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", job.EmbyID, job.Name); err == nil {
		t.Fatal("pending login accepted")
	}
	restarted, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	restarted.NewEmby = s.NewEmby
	a.Members = restarted
	f.failEnable, f.failLibraries = false, true // Resumption uses the saved initial policy.
	u, err := a.Register(ctx, "pending-reader", "password123", invite.Code)
	if err != nil || u.ID != id || !restarted.record(id).Provisioned {
		t.Fatal(u, err)
	}
	if remote.creates != 1 || remote.passwords != 1 {
		t.Fatal("resumption rewrote account", remote.creates, remote.passwords)
	}
	if f.users[job.EmbyID].Policy["IsDisabled"] != false {
		t.Fatal("login still disabled")
	}
	if err := a.CheckInvite(ctx, invite.Code); err == nil {
		t.Fatal("completed invite reusable")
	}
	if _, err := a.Register(ctx, "pending-reader", "password123", invite.Code); err == nil {
		t.Fatal("completed registration rewritten")
	}
}

func TestProvisioningUnknownCreateNeverReplayed(t *testing.T) {
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	ctx := context.Background()
	if err := s.SetChildLimit(ctx, owner, 1); err != nil {
		t.Fatal(err)
	}
	remote := &provisioningRemote{memberTestRemote: f, absent: true}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	a, invite := provisioningInvite(t, s, "assigned")
	for i := 0; i < 2; i++ {
		if _, err := a.Register(ctx, "unknown-user", "password123", invite.Code); err == nil {
			t.Fatal("unknown registration succeeded")
		}
		if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "unknown-child-request", Name: "unknown-child", Password: "password123"}); err == nil {
			t.Fatal("unknown child succeeded")
		}
	}
	if remote.creates != 2 {
		t.Fatal("unknown creates replayed", remote.creates)
	}
	view, err := s.Children(ctx, owner)
	if err != nil || view.Used != 1 || view.Items[0].Completed {
		t.Fatal(view, err)
	}
}

func TestProvisioningChildActivationAndCompletionIdempotency(t *testing.T) {
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	ctx := context.Background()
	if err := s.SetChildLimit(ctx, owner, 1); err != nil {
		t.Fatal(err)
	}
	remote := &provisioningRemote{memberTestRemote: f}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	input := ChildCreateInput{RequestID: "activation-child-job", Name: "pending-child", Password: "private-password"}
	f.failEnable = true
	if err := s.CreateChild(ctx, owner, input); err == nil {
		t.Fatal("pending child succeeded")
	}
	view, err := s.Children(ctx, owner)
	if err != nil || view.Items[0].Completed || view.Used != 1 {
		t.Fatal(view, err)
	}
	child := s.record(view.Items[0].UserID)
	if !s.ChildSessionDenied("http://emby", child.EmbyID) {
		t.Fatal("pending child session accepted")
	}
	jobs, err := s.db.ChildCreations(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(jobs)
	if err != nil || strings.Contains(string(raw), input.Password) {
		t.Fatal("persisted password", err)
	}
	restarted, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	restarted.NewEmby = s.NewEmby
	f.failEnable, f.failLibraries = false, true
	if err := restarted.CreateChild(ctx, owner, input); err != nil {
		t.Fatal(err)
	}
	if remote.creates != 1 || remote.passwords != 1 {
		t.Fatal("duplicate creation/password write")
	}
	// Completed retries must preserve administrator edits and child lifecycle.
	f.users[child.EmbyID].Policy["EnableMediaPlayback"] = false
	writes := f.policyWrites
	if err := restarted.CreateChild(ctx, owner, input); err != nil {
		t.Fatal(err)
	}
	if writes != f.policyWrites || f.users[child.EmbyID].Policy["EnableMediaPlayback"] != false {
		t.Fatal("completed task overwrote admin policy")
	}
}

func TestProvisioningGateWaitAndCancellation(t *testing.T) {
	s, _, _ := memberFixture(t)
	ctx := context.Background()
	if err := s.lock(ctx); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- s.lockCreation(ctx) }()
	select {
	case err := <-result:
		t.Fatal("did not wait", err)
	case <-time.After(20 * time.Millisecond):
	}
	s.unlock()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.lockCreation(cancelled); err == nil {
		t.Fatal("cancelled request acquired gate")
	}
	s.unlock()
}

func TestProvisioningConcurrentSubmissionsCreateOnce(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(map[bool]string{false: "invitation", true: "child"}[child], func(t *testing.T) {
			s, f, owner := memberFixture(t)
			ctx := context.Background()
			memberScan(t, s, owner)
			if err := s.SetChildLimit(ctx, owner, 1); err != nil {
				t.Fatal(err)
			}
			a, invite := provisioningInvite(t, s, "assigned")
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if child {
						results <- s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "concurrent-child-job", Name: "parallel-child", Password: "password123"})
					} else {
						_, err := a.Register(ctx, "parallel-user", "password123", invite.Code)
						results <- err
					}
				}()
			}
			wg.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				}
			}
			want := 1
			if child {
				want = 8
			}
			if f.created != 1 || successes != want {
				t.Fatal("duplicate creation or incorrect result", f.created, successes)
			}
		})
	}
}

func TestProvisioningRevocationDuringActivationDoesNotCommit(t *testing.T) {
	s, f, _ := memberFixture(t)
	ctx := context.Background()
	a, invite := provisioningInvite(t, s, "personal")
	remote := &provisioningRemote{memberTestRemote: f}
	remote.afterEnable = func() {
		if err := s.db.RevokePortalInvite(ctx, invite.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	if _, err := a.Register(ctx, "revoked-pending", "password123", invite.Code); err == nil {
		t.Fatal("revoked invitation completed")
	}
	for _, r := range s.Records() {
		if r.EmbyName != "revoked-pending" {
			continue
		}
		if r.Provisioned || f.users[r.EmbyID].Policy["IsDisabled"] != true {
			t.Fatal("uncommitted identity enabled")
		}
		if _, err := s.portalUser(ctx, r.UserID); err == nil {
			t.Fatal("pending portal access accepted")
		}
		return
	}
	t.Fatal("missing durable pending identity")
}
