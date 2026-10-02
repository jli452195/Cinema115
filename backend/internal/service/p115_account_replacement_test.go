package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/emby"
)

func TestManagedReplacementValidatesBeforeCommit(t *testing.T) {
	ctx := context.Background()
	s, remote, owner := memberFixture(t)
	id, err := s.accounts.Add("shared")
	if err != nil {
		t.Fatal(err)
	}
	oldCookie := "UID=501_A; SEID=old"
	if err := s.accounts.Update(id, 1, "shared", &oldCookie); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.UpdatePlaybackDirectory(id, 2, "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, owner, "assigned", ""); err != nil {
		t.Fatal(err)
	}
	old, _ := s.accounts.Get(id)
	newCookie := "UID=502_A; SEID=new"
	for _, dir := range []string{"", "999"} {
		if err := s.accounts.LoginManaged(ctx, id, old.Revision, old.Name, newCookie, dir, remote, remote); err == nil {
			t.Fatal("invalid directory accepted")
		}
		if got, _ := s.accounts.Get(id); got != old {
			t.Fatal("failed replacement mutated active slot")
		}
	}
	if err := s.accounts.LoginManaged(ctx, id, old.Revision, old.Name, newCookie, "123", remote, remote); err != nil {
		t.Fatal(err)
	}
	current, _ := s.accounts.Get(id)
	if current.UID != "502" || current.Cookie != newCookie || current.PlaybackParentCID != "123" || s.record(owner).Source != "assigned" {
		t.Fatal("slot or binding changed incorrectly")
	}
	if old.Cookie != oldCookie || old.UID != "501" {
		t.Fatal("running task snapshot changed")
	}
	if err := s.accounts.LoginManaged(ctx, id, old.Revision, old.Name, oldCookie, "123", remote, remote); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale login overwrote replacement")
	}
	if _, err := s.Authorize(ctx, "http://emby", s.record(owner).EmbyID); err != nil {
		t.Fatal(err)
	}
	refreshed := "UID=502_B; SEID=refresh"
	if err := s.accounts.LoginManaged(ctx, id, current.Revision, current.Name, refreshed, "", remote, remote); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.accounts.Get(id); got.PlaybackParentCID != "123" {
		t.Fatal("same UID refresh lost directory")
	}
}

func TestPrimaryReplacementInvalidatesOldFeatureIdentity(t *testing.T) {
	ctx := context.Background()
	s, remote, _ := memberFixture(t)
	id := memberTestPrimary(t, s)
	old, _ := s.accounts.Primary(id)
	if err := s.accounts.LoginManaged(ctx, id, old.Revision, old.Name, "UID=999_A; SEID=new", "", remote, remote); err != nil {
		t.Fatal(err)
	}
	if s.accounts.PrimaryID() == id {
		t.Fatal("old feature directories still address new main")
	}
	if _, err := s.accounts.Primary(id); err == nil {
		t.Fatal("stale feature ownership accepted")
	}
	if _, err := s.accounts.Get(id); !errors.Is(err, ErrAccountMissing) {
		t.Fatal("old main ID still resolves")
	}
	if old.UID != "888" {
		t.Fatal("running snapshot changed")
	}
	reopened, err := newP115Accounts(s.accounts.store, "")
	if err != nil {
		t.Fatal(err)
	}
	if a, err := reopened.Primary(""); err != nil || a.UID != "999" {
		t.Fatal("new primary not persisted")
	}
}

func TestIndependentSameUIDAndAtomicPersonalUnbind(t *testing.T) {
	ctx := context.Background()
	s, remote, owner := memberFixture(t)
	memberScan(t, s, owner)
	old := s.record(owner)
	u := emby.ManagedUser{ID: strings.Repeat("c", 32), Name: "other", Policy: map[string]any{"IsDisabled": false}}
	remote.users[u.ID] = u
	profile, err := s.ObserveUser(ctx, s.emby.snapshot(), u)
	if err != nil {
		t.Fatal(err)
	}
	other := s.record(profile.ID)
	other.Source = "personal"
	other, err = s.accounts.BindMember(ctx, other, "other", "UID=123_B; SEID=other", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.accounts.Get(other.PendingAccountID)
	if a.ID == old.AccountID || a.Cookie == remote.cookie {
		t.Fatal("same UID reused another owner's credentials")
	}
	if err := s.Suspend(ctx, owner); err != nil {
		t.Fatal(err)
	}
	r := s.record(owner)
	if r.AccountID != "" || r.PendingAccountID != "" || r.DirectoryID != "" || r.Source != "personal" || !r.Suspended {
		t.Fatal("unbind retained identity or fell back")
	}
	if _, err := s.accounts.Get(old.AccountID); !errors.Is(err, ErrAccountMissing) {
		t.Fatal("unbound personal config retained")
	}
	if got, _ := s.accounts.Get(a.ID); got != a {
		t.Fatal("unbind changed another owner's same-UID credentials")
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("unbound user played")
	}
	remote.cookie = "UID=777_A; SEID=rebound"
	memberScan(t, s, owner)
	if s.View(owner).UID != "777" {
		t.Fatal("unbind prevented arbitrary new login")
	}
}

func TestUnbindStorageFailurePreservesCredentialsAndAssociation(t *testing.T) {
	s, _, owner := memberFixture(t)
	memberScan(t, s, owner)
	r := s.record(owner)
	a, _ := s.accounts.Get(r.AccountID)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.accounts.UnbindMember(context.Background(), r); err == nil {
		t.Fatal("failed transaction accepted")
	}
	if got, _ := s.accounts.Get(a.ID); got != a {
		t.Fatal("failed unbind changed credentials")
	}
	if s.record(owner).AccountID != r.AccountID {
		t.Fatal("failed unbind changed association")
	}
}

func TestManagedReplacementStorageFailureKeepsOldIdentity(t *testing.T) {
	s, remote, _ := memberFixture(t)
	id := memberTestPrimary(t, s)
	old, _ := s.accounts.Get(id)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.LoginManaged(context.Background(), id, old.Revision, old.Name, "UID=999_A; SEID=new", "", remote, remote); err == nil {
		t.Fatal("failed replacement accepted")
	}
	if got, _ := s.accounts.Get(id); got != old || s.accounts.PrimaryID() != id {
		t.Fatal("failed transaction changed primary")
	}
}
