package storage

import (
	"context"
	"encoding/json"
	"strm115/backend/internal/model"
	"testing"
)

func TestMemberDeletionCleanupTransactionRollback(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	u := model.PortalUser{ID: "owner", ServerID: "server", EmbyID: "emby", Username: "reader", CreatedAt: "now"}
	if err := s.SaveEmbyProfile(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMemberPlayback(ctx, model.MemberPlayback{UserID: u.ID, ServerID: u.ServerID, EmbyID: u.EmbyID}); err != nil {
		t.Fatal(err)
	}
	// An overlooked child must make the owner cleanup transaction fail, not orphan it.
	child := model.ChildCreation{ID: "child", OwnerID: u.ID, Name: "child", ServerID: u.ServerID, EmbyID: "child-emby"}
	raw, err := json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO playback_child_creations(id,owner_id,record) VALUES(?,?,?)`, child.ID, u.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	job := model.MemberDeletion{ID: u.ID, Stage: "pending"}
	if err := s.SaveMemberDeletion(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Stage = "completed"
	err = s.FinishMemberDeletion(ctx, job, model.MemberDeletionTarget{UserID: u.ID, ServerID: u.ServerID, EmbyID: u.EmbyID}, model.UserPlaybackDocument{}, model.PlaybackPoolDocument{})
	if err == nil {
		t.Fatal("owner with child removed")
	}
	if _, err := s.PortalUser(ctx, u.ID); err != nil {
		t.Fatal("profile lost on rollback", err)
	}
	records, err := s.MemberPlaybackRecords(ctx)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	jobs, err := s.MemberDeletions(ctx)
	if err != nil || jobs[0].Stage != "pending" {
		t.Fatal(jobs, err)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}
