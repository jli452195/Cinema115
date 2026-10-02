package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPersonalLoginClientAndExplicitDirectory(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	input := memberQRInput(s, id, false)
	input.ClientType = "arbitrary"
	if _, err := s.CreateQR(ctx, id, input); err == nil {
		t.Fatal("unknown client accepted")
	}
	input.ClientType = "wechat-miniapp"
	old, err := s.CreateQR(ctx, id, input)
	if err != nil || remote.qrApp != "wechatmini" {
		t.Fatal(old, err)
	}
	if len(s.accounts.List()) != 0 {
		t.Fatal("QR created persistent account")
	}
	input.ClientType = "115-life-ios"
	qr, err := s.CreateQR(ctx, id, input)
	if err != nil || remote.qrApp != "ios" {
		t.Fatal(qr, err)
	}
	if _, err := s.PollQR(ctx, id, old.ID); err == nil {
		t.Fatal("old QR still active")
	}
	if _, err := s.PollQR(ctx, id, qr.ID); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	if r.AccountID != "" || r.PendingAccountID == "" || remote.dirs != 0 {
		t.Fatal("login activated or created directory", r)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if remote.dirs != 0 || s.record(id).AccountID != "" {
		t.Fatal("sync bypassed directory selection")
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("pending drive played")
	}
	selectInput := memberDirectoryInput(s, id, "123", false)
	if err := s.SetPersonalDirectory(ctx, id, selectInput); err != nil {
		t.Fatal(err)
	}
	if s.record(id).Stage != "ready" || s.View(id).DirectoryID != "123" {
		t.Fatal(s.View(id))
	}
	if err := s.SetPersonalDirectory(ctx, id, selectInput); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale directory accepted", err)
	}
	if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "456", false)); err != nil {
		t.Fatal(err)
	}
	reopened, err := newP115Accounts(s.accounts.store, "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := reopened.Get(s.record(id).AccountID)
	if err != nil || a.PlaybackParentCID != "456" || s.record(id).DirectoryID != "456" || remote.dirs != 0 {
		t.Fatal("directory update not persisted", err)
	}
}

func TestPersonalDirectoryOwnershipAndValidation(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	before := s.record(id)
	input := memberDirectoryInput(s, id, "0", false)
	page, err := s.PersonalDirectories(ctx, id, input, 0)
	if err != nil || len(page.Directories) != 1 || page.Directories[0].ID != "123" || page.NextOffset != 2 || remote.directoryCookie != remote.cookie {
		t.Fatal(page, err)
	}
	admin, err := s.accounts.Add("admin")
	if err != nil {
		t.Fatal(err)
	}
	input.AccountID = admin
	if _, err := s.PersonalDirectories(ctx, id, input, 0); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("other account browsed", err)
	}
	input = memberDirectoryInput(s, id, "0", false)
	if err := s.SetPersonalDirectory(ctx, id, input); err == nil {
		t.Fatal("root used directly")
	}
	input.DirectoryID = "999"
	if err := s.SetPersonalDirectory(ctx, id, input); err == nil {
		t.Fatal("invalid directory accepted")
	}
	if !reflect.DeepEqual(before, s.record(id)) {
		t.Fatal("failed selection changed binding")
	}
	input.DirectoryID = "456"
	remote.valid = false
	if err := s.SetPersonalDirectory(ctx, id, input); err == nil {
		t.Fatal("invalid cookie accepted")
	}
	remote.valid = true
	r := s.record(id)
	remote.users[r.EmbyID].Policy["IsDisabled"] = true
	if _, err := s.PersonalDirectories(ctx, id, input, 0); err == nil {
		t.Fatal("blocked user browsed")
	}
	if err := s.SetPersonalDirectory(ctx, id, input); err == nil {
		t.Fatal("blocked user wrote")
	}
}

func TestPersonalDirectoryCommitFailurePreservesMemory(t *testing.T) {
	s, _, id := memberFixture(t)
	memberScan(t, s, id)
	input := memberDirectoryInput(s, id, "456", false)
	r := s.record(id)
	a, _ := s.accounts.Get(r.AccountID)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.accounts.SetMemberDirectory(context.Background(), r, a.ID, a.Revision, "456"); err == nil {
		t.Fatal("failed commit accepted")
	}
	got, _ := s.accounts.Get(a.ID)
	if got != a || !reflect.DeepEqual(r, s.record(id)) {
		t.Fatal("memory published before commit", input)
	}
}

func TestPersonalExplicitCreateAndCancel(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	qr, err := s.CreateQR(ctx, id, memberQRInput(s, id, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, id, qr.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "0", true)); err != nil {
		t.Fatal(err)
	}
	if remote.dirs != 1 || s.View(id).DirectoryID != "123" || s.View(id).DirectoryAccount.CreationUncertain {
		t.Fatal(s.View(id))
	}
	old := s.record(id)
	remote.cookie = "UID=999_A; SEID=replacement"
	qr, err = s.CreateQR(ctx, id, memberQRInput(s, id, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, id, qr.ID); err != nil {
		t.Fatal(err)
	}
	input := memberDirectoryInput(s, id, "0", false)
	if _, err := s.PersonalDirectories(ctx, id, input, 0); err != nil || remote.directoryCookie != remote.cookie {
		t.Fatal("pending credentials not used", err)
	}
	if err := s.FinishDriveChange(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if s.record(id).AccountID != old.AccountID || s.record(id).DirectoryID != old.DirectoryID {
		t.Fatal("cancel changed active binding")
	}
	if _, err := s.PersonalDirectories(ctx, id, input, 0); err == nil {
		t.Fatal("canceled account browsed")
	}
}
