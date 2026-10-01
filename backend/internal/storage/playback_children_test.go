package storage

import (
	"context"
	"errors"
	"strm115/backend/internal/model"
	"testing"
)

func TestChildReservationAndAtomicIdentityCommit(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	u := model.PortalUser{ID: "owner", ServerID: "server", EmbyID: "owner-emby", Username: "owner", CreatedAt: "now"}
	if err := s.SaveEmbyProfile(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMemberPlayback(ctx, model.MemberPlayback{UserID: u.ID, ServerID: u.ServerID, EmbyID: u.EmbyID, ChildLimit: 1}); err != nil {
		t.Fatal(err)
	}
	job := model.ChildCreation{ID: "request-1", OwnerID: u.ID, ServerID: u.ServerID, EmbyURL: "http://emby", Name: "viewer", ReservedName: "c115_random"}
	if _, err := s.ReserveChild(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveChild(ctx, job); err != nil {
		t.Fatal("repeat consumed quota", err)
	}
	second := job
	second.ID = "request-2"
	if _, err := s.ReserveChild(ctx, second); !errors.Is(err, ErrChildQuota) {
		t.Fatal("quota not enforced", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_child BEFORE INSERT ON member_playback WHEN NEW.user_id='child' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	child := model.PortalUser{ID: "child", ServerID: "server", EmbyID: "child-emby", Username: "viewer", CreatedAt: "now"}
	r := model.MemberPlayback{UserID: child.ID, ServerID: child.ServerID, EmbyID: child.EmbyID, PlaybackOnly: true, OwnerUserID: u.ID, Source: "personal_shared"}
	job.EmbyID = child.EmbyID
	job.Completed = true
	if err := s.CommitChild(ctx, job, child, r); err == nil {
		t.Fatal("partial commit accepted")
	}
	if _, err := s.PortalUser(ctx, child.ID); !errors.Is(err, ErrEmbyUserNotFound) {
		t.Fatal("profile escaped rollback", err)
	}
	jobs, err := s.ChildCreations(ctx, u.ID)
	if err != nil || len(jobs) != 1 || jobs[0].Completed {
		t.Fatal("job escaped rollback", jobs, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER reject_child`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitChild(ctx, job, child, r); err != nil {
		t.Fatal(err)
	}
	r.ChildDisabled = true
	if err := s.SaveMemberPlayback(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveChild(ctx, second); !errors.Is(err, ErrChildQuota) {
		t.Fatal("disabled child released quota")
	}
	r.ChildRevoked = true
	if err := s.SaveMemberPlayback(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveChild(ctx, second); err != nil {
		t.Fatal("revoked child did not release quota", err)
	}
	if err := s.ResetPortal(ctx); err != nil {
		t.Fatal("reset with child rows failed", err)
	}
}
