package service

import (
	"context"
	"errors"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"testing"
)

type cleanupRemote struct {
	*memberTestRemote
	offline, failDelete, lostDelete bool
	serverID                        string
	deletes                         int
}

func (f *cleanupRemote) Info(context.Context) (emby.Server, error) {
	if f.offline {
		return emby.Server{}, errors.New("offline")
	}
	return emby.Server{ID: f.serverID}, nil
}
func (f *cleanupRemote) DeleteManagedUser(_ context.Context, id string) error {
	f.deletes++
	if f.failDelete {
		return errors.New("failed")
	}
	delete(f.users, id)
	if f.lostDelete {
		return errors.New("lost response")
	}
	return nil
}
func cleanupFixture(t *testing.T) (*MemberPlaybackService, *cleanupRemote, string) {
	t.Helper()
	s, f, id := memberFixture(t)
	r := &cleanupRemote{memberTestRemote: f, serverID: "server"}
	s.NewEmby = func(string, string) memberEmbyClient { return r }
	return s, r, id
}
func cleanupSubmit(t *testing.T, s *MemberPlaybackService, id, mode string) error {
	t.Helper()
	p, err := s.CleanupPreview(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s.CleanupWebsiteUser(context.Background(), id, p.Fingerprint, p.Username, mode)
}
func TestWebsiteCleanupEmptyAndFreshRegistration(t *testing.T) {
	s, f, id := cleanupFixture(t)
	ctx := context.Background()
	if err := cleanupSubmit(t, s, id, "none"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.WebsiteUser(ctx, id); !errors.Is(err, storage.ErrWebsiteUserNotFound) {
		t.Fatal("website account retained", err)
	}
	if f.deletes != 0 {
		t.Fatal("empty account touched Emby")
	}
	if err := cleanupSubmit(t, s, id, "none"); err != nil {
		t.Fatal("retry not idempotent", err)
	}
	if err := s.db.CreateWebsiteUser(ctx, model.WebsiteUser{ID: strings.Repeat("f", 32), Username: "reader", PasswordHash: "new", CreatedAt: "now"}); err != nil {
		t.Fatal("name not reusable", err)
	}
	if _, err := s.db.WebsiteUser(ctx, id); !errors.Is(err, storage.ErrWebsiteUserNotFound) {
		t.Fatal("old identity restored")
	}
}
func TestWebsiteCleanupPersonalDriveAndRestart(t *testing.T) {
	s, f, id := cleanupFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	f.lostDelete = true
	if err := cleanupSubmit(t, s, id, "delete"); err != nil {
		t.Fatal(err)
	}
	a, err := s.accounts.Get(r.AccountID)
	if err != nil || a.Cookie != "" || a.UID != "123" || a.PlaybackParentCID != "123" || a.OwnerUserID != id {
		t.Fatal("personal identity lost or credentials retained")
	}
	if _, ok := f.users[r.EmbyID]; ok {
		t.Fatal("Emby retained")
	}
	history, err := s.CleanupHistory(ctx)
	if err != nil || len(history) != 1 || history[0].State != "deleted" {
		t.Fatal("history missing", err)
	}
	reopened, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	if managed, err := reopened.Authorize(ctx, "http://emby", r.EmbyID); !managed || err == nil {
		t.Fatal("deleted identity fell back after restart")
	}
	if err := reopened.AuthorizeLogin(ctx, "http://emby", r.EmbyID, r.EmbyName); err == nil {
		t.Fatal("deleted identity can log in")
	}
	if err := reopened.SyncUser(ctx, id); err != nil {
		t.Fatal("archived identity reconciled", err)
	}
}
func TestWebsiteCleanupKeepAndFailedRetry(t *testing.T) {
	s, f, id := cleanupFixture(t)
	ctx := context.Background()
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	f.offline = true
	if err := cleanupSubmit(t, s, id, "keep"); err == nil {
		t.Fatal("offline cleanup succeeded")
	}
	u, err := s.db.WebsiteUser(ctx, id)
	if err != nil || !u.Disabled || u.CleanupState != "deleting" {
		t.Fatal("not locked", err)
	}
	no := false
	if err := s.db.UpdateWebsiteUser(ctx, id, "", &no); err == nil {
		t.Fatal("cleanup user reenabled")
	}
	if err := s.AdminUpdate(ctx, id, &no, "", false); err == nil {
		t.Fatal("cleanup playback reenabled")
	}
	if err := cleanupSubmit(t, s, id, "delete"); err == nil {
		t.Fatal("retry changed mode")
	}
	f.offline = false
	if err := cleanupSubmit(t, s, id, "keep"); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 0 || f.users[r.EmbyID].Policy["IsDisabled"] != true {
		t.Fatal("kept account not disabled")
	}
	if managed, err := s.Authorize(ctx, "http://emby", r.EmbyID); !managed || err == nil {
		t.Fatal("kept user fell back")
	}
}
func TestWebsiteCleanupRejectsStaleAndForeignServer(t *testing.T) {
	s, f, id := cleanupFixture(t)
	ctx := context.Background()
	p, err := s.CleanupPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupWebsiteUser(ctx, id, p.Fingerprint, p.Username, "none"); err == nil {
		t.Fatal("stale preview accepted")
	}
	f.serverID = "other-server"
	if err := cleanupSubmit(t, s, id, "delete"); err == nil {
		t.Fatal("foreign server accepted")
	}
	if f.deletes != 0 {
		t.Fatal("wrong server touched")
	}
	f.serverID = "server"
	f.failDelete = true
	if err := cleanupSubmit(t, s, id, "delete"); err == nil {
		t.Fatal("failed deletion finalized")
	}
	f.failDelete = false
	if err := cleanupSubmit(t, s, id, "delete"); err != nil {
		t.Fatal(err)
	}
}

func TestWebsiteCleanupPreservesAssignedAccount(t *testing.T) {
	s, f, id := cleanupFixture(t)
	ctx := context.Background()
	accountID, err := s.accounts.Add("friend")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=888_A; SEID=friend"
	if err := s.accounts.Update(accountID, 1, "friend", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.UpdatePlaybackDirectory(accountID, 2, "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, id, "assigned", accountID); err != nil {
		t.Fatal(err)
	}
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	if err := cleanupSubmit(t, s, id, "delete"); err != nil {
		t.Fatal(err)
	}
	a, err := s.accounts.Get(accountID)
	if err != nil || a.Cookie != cookie || a.PlaybackParentCID != "123" || f.dirs != 0 {
		t.Fatal("shared administrator account changed")
	}
}
