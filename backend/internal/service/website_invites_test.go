package service

import (
	"context"
	"errors"
	"path/filepath"
	"strm115/backend/internal/storage"
	"testing"
)

func TestWebsiteInvitesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "invites.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	auth := NewWebsiteAuth(db, "admin", "password", "secret")
	invites, err := auth.GenerateInvites(ctx, "admin", "friend", 1, 7)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth = NewWebsiteAuth(db, "admin", "password", "secret")
	if err := auth.CheckInvite(ctx, invites[0].Code); err != nil {
		t.Fatal("invite lost on restart", err)
	}
	user, err := auth.Register(ctx, "friend", "test-password", invites[0].Code)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.CheckInvite(ctx, invites[0].Code); !errors.Is(err, storage.ErrWebsiteInviteInvalid) {
		t.Fatal("used invite accepted", err)
	}
	_, logged, err := auth.Login(ctx, "friend", "test-password")
	if err != nil || logged.ID != user.ID || logged.Role != "user" {
		t.Fatal("registered account cannot login", err)
	}
}
