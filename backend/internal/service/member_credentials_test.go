package service

import (
	"context"
	"strings"
	"strm115/backend/internal/client/emby"
	"testing"
)

func TestMemberCustomRegistrationAndCredentialIdentity(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	if err := s.RegisterAccount(ctx, id, "Alice", "password123", 0); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	if r.EmbyName != "Alice" || !r.Provisioned || f.created != 1 || r.Stage != "pending" {
		t.Fatal(r)
	}
	originalID, originalSource := r.EmbyID, r.Source
	otherID := strings.Repeat("d", 32)
	f.users[otherID] = emby.ManagedUser{ID: otherID, Name: "Bob", Policy: map[string]any{}}
	if err := s.RenameAccount(ctx, id, "bob", r.AccountRevision); err == nil {
		t.Fatal("case-insensitive duplicate accepted")
	}
	for _, name := range []string{"ab", "c115_reserved", " Alice", "../Bob", "a/b", "a\nb"} {
		if err := s.RenameAccount(ctx, id, name, r.AccountRevision); err == nil {
			t.Fatal("invalid name accepted", name)
		}
	}
	f.renameResponseLost = true
	if err := s.RenameAccount(ctx, id, "Alice2", r.AccountRevision); err != nil {
		t.Fatal("lost response was not resolved by ID", err)
	}
	if err := s.ChangePassword(ctx, id, "new-password", r.AccountRevision); err == nil {
		t.Fatal("stale password form accepted")
	}
	r = s.record(id)
	if err := s.ChangePassword(ctx, id, "new-password", r.AccountRevision); err != nil {
		t.Fatal(err)
	}
	if f.passwordTarget != originalID || s.record(id).EmbyID != originalID || s.record(id).Source != originalSource || f.users[otherID].Name != "Bob" {
		t.Fatal("credential update changed identity")
	}
	if s.record(id).PolicyEnabled || !s.record(id).LoginEnabled {
		t.Fatal("credential update changed playback authorization")
	}
	// A deleted account is never replaced with a newly-created namesake.
	delete(f.users, originalID)
	f.users[otherID] = emby.ManagedUser{ID: otherID, Name: "Alice2", Policy: map[string]any{}}
	f.passwordTarget = ""
	if err := s.ChangePassword(ctx, id, "new-password", s.record(id).AccountRevision); err == nil || f.passwordTarget != "" {
		t.Fatal("password sent to replacement account")
	}
	if err := s.RenameAccount(ctx, id, "Alice3", s.record(id).AccountRevision); err == nil {
		t.Fatal("rename adopted replacement account")
	}
}

func TestMemberCustomRegistrationRecoversOnlyInternalReservation(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	f.failCreate = true
	if err := s.RegisterAccount(ctx, id, "Reader", "password123", 0); err == nil {
		t.Fatal("lost creation response accepted")
	}
	r := s.record(id)
	if r.EmbyName == "Reader" || r.EmbyName == "c115_"+id || !strings.HasPrefix(r.EmbyName, "c115_") || r.EmbyID != "" {
		t.Fatal("missing random reservation", r)
	}
	f.failCreate = false
	if err := s.RegisterAccount(ctx, id, "Reader", "password123", r.AccountRevision); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 || s.record(id).EmbyName != "Reader" {
		t.Fatal("recreated user")
	}
	if err := s.RegisterAccount(ctx, id, "SomeoneElse", "password123", s.record(id).AccountRevision); err == nil {
		t.Fatal("registration reused as password reset")
	}
}

func TestMemberExternalRenameRefreshRejectsStaleMutation(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	if err := s.RegisterAccount(ctx, id, "Reader", "password123", 0); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	u := f.users[r.EmbyID]
	u.Name = "ChangedInEmby"
	f.users[r.EmbyID] = u
	f.passwordTarget = ""
	if err := s.ChangePassword(ctx, id, "new-password", r.AccountRevision); err == nil || f.passwordTarget != "" {
		t.Fatal("external rename did not reject stale form")
	}
	if s.record(id).EmbyName != "ChangedInEmby" || s.record(id).EmbyID != r.EmbyID {
		t.Fatal("external rename changed binding")
	}
}
