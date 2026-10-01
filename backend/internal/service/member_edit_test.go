package service

import (
	"context"
	"reflect"
	"testing"
)

func TestMemberEditValidatesWholeFormAndRejectsStale(t *testing.T) {
	s, _, id := memberFixture(t)
	ctx := context.Background()
	before := s.record(id)
	input := MemberEditInput{Expected: memberEditValues(before), MemberEditValues: MemberEditValues{Source: "assigned", AccountID: "missing", ChildLimit: 3, SkipLibrarySync: true}}
	if err := s.EditMember(ctx, id, input); err == nil {
		t.Fatal("invalid source accepted")
	}
	if !reflect.DeepEqual(before, s.record(id)) {
		t.Fatal("invalid form partially saved")
	}
	input.Source = "primary"
	input.AccountID = ""
	if err := s.EditMember(ctx, id, input); err != nil {
		t.Fatal(err)
	}
	got := s.record(id)
	if got.Source != "primary" || got.ChildLimit != 3 || !got.SkipLibrarySync || got.PolicyEnabled || got.AppliedRevision != 0 {
		t.Fatal(got)
	}
	input.ChildLimit = 4
	if err := s.EditMember(ctx, id, input); err == nil {
		t.Fatal("stale form accepted")
	}
	rows, err := s.db.MemberPlaybackRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.UserID == id {
			found = true
			if !reflect.DeepEqual(r, got) {
				t.Fatal("persisted settings differ")
			}
		}
	}
	if !found {
		t.Fatal("settings not persisted")
	}
}

func TestMemberEditChildCannotChangeSourceOrQuota(t *testing.T) {
	s, _, _, child := childFixture(t)
	ctx := context.Background()
	in := MemberEditInput{Expected: memberEditValues(child), MemberEditValues: memberEditValues(child)}
	in.ChildLimit = 1
	if err := s.EditMember(ctx, child.UserID, in); err == nil {
		t.Fatal("nested sharing allowed")
	}
	in.ChildLimit = 0
	in.Source = "primary"
	if err := s.EditMember(ctx, child.UserID, in); err == nil {
		t.Fatal("child source changed")
	}
	in.Source = child.Source
	in.SkipLibrarySync = true
	if err := s.EditMember(ctx, child.UserID, in); err != nil {
		t.Fatal(err)
	}
}

func TestMemberEditStorageFailurePreservesState(t *testing.T) {
	s, _, id := memberFixture(t)
	ctx := context.Background()
	before := s.record(id)
	in := MemberEditInput{Expected: memberEditValues(before), MemberEditValues: memberEditValues(before)}
	in.ChildLimit = 3
	in.SkipLibrarySync = true
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.EditMember(ctx, id, in); err == nil {
		t.Fatal("write failure hidden")
	}
	if !reflect.DeepEqual(before, s.record(id)) {
		t.Fatal("memory updated before commit")
	}
}
