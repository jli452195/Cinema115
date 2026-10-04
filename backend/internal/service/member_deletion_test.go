package service

import (
	"context"
	"errors"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
	"testing"
)

type deletionRemote struct {
	*memberTestRemote
	failDelete, lostReply, failList, hideUser bool
	deleted                                   []string
}

func (f *deletionRemote) DeleteManagedUser(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	if f.failDelete {
		return errors.New("unavailable")
	}
	delete(f.users, id)
	if f.lostReply {
		return errors.New("reply lost")
	}
	return nil
}
func (f *deletionRemote) ManagedUserAbsent(_ context.Context, id string) (bool, error) {
	_, ok := f.users[id]
	return !ok, nil
}
func (f *deletionRemote) Users(ctx context.Context) ([]emby.User, error) {
	if f.failList {
		return nil, errors.New("unavailable")
	}
	users, err := f.memberTestRemote.Users(ctx)
	if f.hideUser {
		return []emby.User{}, nil
	}
	return users, err
}
func deletionInput(t *testing.T, s *MemberPlaybackService, id string) MemberDeleteInput {
	t.Helper()
	p, err := s.PreviewMemberDeletion(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return MemberDeleteInput{Key: id, Fingerprint: p.Fingerprint, ConfirmName: p.Name, IncludeChildren: true}
}
func deletionClient(s *MemberPlaybackService, f *memberTestRemote) *deletionRemote {
	r := &deletionRemote{memberTestRemote: f}
	s.NewEmby = func(string, string) memberEmbyClient { return r }
	return r
}

func TestMemberDeletionCascadeAndNoRediscovery(t *testing.T) {
	s, f, owner, child := childFixture(t)
	remote := deletionClient(s, f)
	remote.lostReply = true
	ctx := context.Background()
	original := f.users[s.record(owner).EmbyID]
	main := memberTestPrimary(t, s)
	parent := s.record(owner)
	// Capacity belonging to other users must remain untouched.
	s.UserPlayback.doc.Leases["delete-me"] = model.UserPlaybackLease{UserID: owner, Key: "delete-me", EventKey: "event", Generation: 1}
	s.Pool.doc.Leases["delete-me"] = model.PlaybackPoolLease{UserID: child.UserID, Key: "delete-me", Generation: 1}
	s.Pool.doc.Leases["keep-me"] = model.PlaybackPoolLease{UserID: "other", Key: "keep-me", Generation: 2}
	in := deletionInput(t, s, owner)
	in.IncludeChildren = false
	if _, err := s.DeleteMember(ctx, in, "admin"); err == nil {
		t.Fatal("implicit cascade accepted")
	}
	in.IncludeChildren = true
	job, err := s.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "completed" {
		t.Fatal(job, err)
	}
	if len(remote.deleted) != 2 || remote.deleted[0] != child.EmbyID || remote.deleted[1] != parent.EmbyID {
		t.Fatal("wrong order", remote.deleted)
	}
	if _, err := s.accounts.Get(parent.AccountID); err == nil {
		t.Fatal("personal credential retained")
	}
	if _, err := s.accounts.Get(main); err != nil {
		t.Fatal("primary deleted", err)
	}
	if len(s.UserPlayback.doc.Leases) != 0 || len(s.Pool.doc.Leases) != 1 || s.Pool.doc.Leases["keep-me"].UserID != "other" {
		t.Fatal("capacity cleanup incorrect")
	}
	for _, id := range []string{owner, child.UserID} {
		if s.record(id).UserID != "" {
			t.Fatal("record retained")
		}
		if _, err := s.db.PortalUser(ctx, id); err == nil {
			t.Fatal("profile retained")
		}
	}
	if _, err := s.ObserveUser(ctx, s.emby.snapshot(), original); err == nil {
		t.Fatal("deleted ID rediscovered")
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", parent.EmbyID, parent.EmbyName); err == nil {
		t.Fatal("deleted login allowed")
	}
	if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "request-child-0001", Name: "viewer-one", Password: "password123"}); err == nil {
		t.Fatal("deleted child job replayed")
	}
	if _, err := s.DeleteMember(ctx, in, "admin"); err != nil || len(remote.deleted) != 2 {
		t.Fatal("completed task replayed", err)
	}
	a, invite := provisioningInvite(t, s, "assigned")
	if _, err := a.Register(ctx, "after-deletion", "password123", invite.Code); err != nil {
		t.Fatal("unrelated new invitation blocked", err)
	}
	original.ID = strings.Repeat("c", 32)
	f.users[original.ID] = original
	newUser, err := s.ObserveUser(ctx, s.emby.snapshot(), original)
	if err != nil || newUser.ID == owner || s.record(newUser.ID).AccountID != "" {
		t.Fatal("same-name replacement inherited identity", err)
	}
}

func TestMemberDeletionPendingRestartAndDraining(t *testing.T) {
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	remote := deletionClient(s, f)
	remote.failDelete = true
	ctx := context.Background()
	in := deletionInput(t, s, owner)
	record := s.record(owner)
	release, err := s.useMember(owner)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "pending" || len(remote.deleted) != 0 {
		t.Fatal(job, err)
	}
	if _, err := s.useMember(owner); err == nil {
		t.Fatal("new work admitted")
	}
	release()
	job, err = s.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "pending" || len(remote.deleted) != 1 {
		t.Fatal(job, err)
	}
	if _, err := s.accounts.Get(record.AccountID); err != nil {
		t.Fatal("credentials removed before deletion confirmed")
	}
	restarted, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	restarted.NewEmby = s.NewEmby
	if _, err := restarted.portalUser(ctx, owner); err == nil {
		t.Fatal("restart forgot fence")
	}
	remote.failDelete = false
	job, err = restarted.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "completed" {
		t.Fatal(job, err)
	}
	if _, err := s.accounts.Get(record.AccountID); err == nil {
		t.Fatal("credentials survived cleanup")
	}
}

func TestMemberDeletionRejectsStalePreviewAdminAndUncertainIdentity(t *testing.T) {
	s, f, owner := memberFixture(t)
	remote := deletionClient(s, f)
	ctx := context.Background()
	in := deletionInput(t, s, owner)
	u := f.users[s.record(owner).EmbyID]
	u.Name = "changed-reader"
	f.users[u.ID] = u
	if _, err := s.DeleteMember(ctx, in, "admin"); err == nil {
		t.Fatal("stale name confirmation accepted")
	}
	u.Policy["IsAdministrator"] = true
	f.users[u.ID] = u
	if _, err := s.PreviewMemberDeletion(ctx, owner); err == nil {
		t.Fatal("administrator deletion allowed")
	}
	u.Policy["IsAdministrator"] = false
	f.users[u.ID] = u
	in = deletionInput(t, s, owner)
	remote.hideUser = true
	job, err := s.DeleteMember(ctx, in, "admin")
	// A changed preview can reject earlier; it must never finish from list absence alone.
	if err == nil && job.Stage == "completed" {
		t.Fatal("filtered list treated as deletion proof")
	}
	if s.record(owner).UserID == "" || len(remote.deleted) != 0 {
		t.Fatal("identity removed without proof")
	}
}

func TestMemberDeletionIncompleteRegistrationAndChild(t *testing.T) {
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	ctx := context.Background()
	remote := deletionClient(s, f)
	if err := s.SetChildLimit(ctx, owner, 2); err != nil {
		t.Fatal(err)
	}
	a, invite := provisioningInvite(t, s, "personal")
	f.failEnable = true
	if _, err := a.Register(ctx, "unfinished-user", "password123", invite.Code); err == nil {
		t.Fatal("expected failure")
	}
	if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "unfinished-child-job", Name: "unfinished-child", Password: "password123"}); err == nil {
		t.Fatal("expected failure")
	}
	pending, _, err := s.DeletionDirectory(ctx)
	if err != nil || len(pending) != 2 {
		t.Fatal(pending, err)
	}
	f.failEnable = false
	for _, p := range pending {
		job, err := s.DeleteMember(ctx, deletionInput(t, s, p.Key), "admin")
		if err != nil || job.Stage != "completed" {
			t.Fatal(job, err)
		}
	}
	if len(remote.deleted) != 2 {
		t.Fatal(remote.deleted)
	}
	if _, err := a.Register(ctx, "unfinished-user", "password123", invite.Code); err == nil {
		t.Fatal("deleted invitation resumed")
	}
	view, err := s.Children(ctx, owner)
	if err != nil || view.Used != 0 || len(view.Items) != 0 {
		t.Fatal(view, err)
	}
}

func TestMemberDeletionCleanupFailureResumesWithoutSecondDelete(t *testing.T) {
	s, f, owner := memberFixture(t)
	remote := deletionClient(s, f)
	ctx := context.Background()
	in := deletionInput(t, s, owner)
	s.CleanupDeletedUser = func(model.MemberDeletionTarget) error { return errors.New("disk unavailable") }
	job, err := s.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "pending" || !job.Targets[0].RemoteDeleted {
		t.Fatal(job, err)
	}
	s.CleanupDeletedUser = nil
	job, err = s.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "completed" || len(remote.deleted) != 1 {
		t.Fatal(job, err)
	}
}

func TestMemberDeletionPreviewRechecksChildrenAndUnknownCreate(t *testing.T) {
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	deletionClient(s, f)
	ctx := context.Background()
	if err := s.SetChildLimit(ctx, owner, 2); err != nil {
		t.Fatal(err)
	}
	in := deletionInput(t, s, owner)
	if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "new-child-after-preview", Name: "new-viewer", Password: "password123"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMember(ctx, in, "admin"); err == nil {
		t.Fatal("changed child list accepted")
	}
	unknown := &provisioningRemote{memberTestRemote: f, absent: true}
	s.NewEmby = func(string, string) memberEmbyClient { return unknown }
	a, invite := provisioningInvite(t, s, "personal")
	if _, err := a.Register(ctx, "unknown-delete", "password123", invite.Code); err == nil {
		t.Fatal("expected unknown creation")
	}
	deletionClient(s, f)
	pending, _, err := s.DeletionDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatal(pending)
	}
	if _, err := s.PreviewMemberDeletion(ctx, pending[0].Key); err == nil {
		t.Fatal("unknown external creation forgotten")
	}
	if _, err := s.db.Registration(ctx, invite.CodeHash); err != nil {
		t.Fatal("unknown reservation removed", err)
	}
}

func TestMemberDeletionConcurrentSameTask(t *testing.T) {
	s, f, owner := memberFixture(t)
	remote := deletionClient(s, f)
	ctx := context.Background()
	in := deletionInput(t, s, owner)
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			job, err := s.DeleteMember(ctx, in, "admin")
			if err == nil && job.Stage != "completed" {
				err = errors.New("unexpected pending")
			}
			results <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if len(remote.deleted) != 1 {
		t.Fatal("duplicate delete", remote.deleted)
	}
}

func TestMemberDeletionDrainsActualMediaGrant(t *testing.T) {
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	remote := deletionClient(s, f)
	ctx := context.Background()
	grant, err := s.BeginMediaAuthorization(ctx, "http://emby", s.record(owner).EmbyID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := deletionInput(t, s, owner)
	job, err := s.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "pending" || len(remote.deleted) != 0 {
		t.Fatal(job, err)
	}
	if err := grant.CheckCurrent(ctx); err == nil {
		t.Fatal("old grant still usable")
	}
	if err := grant.Close(); err != nil {
		t.Fatal(err)
	}
	job, err = s.DeleteMember(ctx, in, "admin")
	if err != nil || job.Stage != "completed" {
		t.Fatal(job, err)
	}
}
