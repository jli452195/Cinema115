package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestMemberQRDoesNotPersistPlaceholder(t *testing.T) {
	for _, outcome := range []string{"waiting", "canceled", "expired", "invalid", "conflict", "source-changed", "local-expiry"} {
		t.Run(outcome, func(t *testing.T) {
			s, f, id := memberFixture(t)
			ctx := context.Background()
			if outcome == "conflict" {
				other, err := s.accounts.Add("other")
				if err != nil {
					t.Fatal(err)
				}
				if err := s.accounts.Update(other, 1, "other", &f.cookie); err != nil {
					t.Fatal(err)
				}
			}
			before := len(s.accounts.List())
			qr, err := s.CreateQR(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.accounts.List()) != before || s.record(id).AccountID != "" {
				t.Fatal("QR created an account")
			}
			switch outcome {
			case "invalid":
				f.valid = false
			case "source-changed":
				if err := s.SetSource(ctx, id, "primary", ""); err != nil {
					t.Fatal(err)
				}
			case "local-expiry":
				qr.ExpiresAt = time.Now().Add(-time.Second)
			case "waiting", "canceled", "expired":
				f.qrStatus = outcome
			}
			_, err = s.PollQR(ctx, id, qr.ID)
			if (outcome == "invalid" || outcome == "conflict" || outcome == "source-changed" || outcome == "local-expiry") && err == nil {
				t.Fatal("invalid QR accepted")
			}
			if len(s.accounts.List()) != before || s.record(id).AccountID != "" {
				t.Fatal("failed scan persisted an account")
			}
			records, err := s.db.MemberPlaybackRecords(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range records {
				if r.AccountID != "" {
					t.Fatal("placeholder association persisted")
				}
			}
		})
	}
}

func TestMemberDriveOwnershipAndInactiveDisconnect(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	a, err := s.accounts.Get(r.AccountID)
	if err != nil || a.OwnerUserID != id || a.Ownership != "member" || a.UID != "123" || len(s.accounts.ManagedList()) != 0 {
		t.Fatal("wrong ownership")
	}
	// Reopening the encrypted store must preserve ownership and identity.
	reopened, err := OpenP115AccountsSQLite(ctx, s.db, t.TempDir(), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := reopened.Get(a.ID)
	if err != nil || reloaded.OwnerUserID != id || reloaded.UID != a.UID || len(reopened.ManagedList()) != 0 {
		t.Fatal("ownership lost after restart")
	}
	target, err := s.accounts.Add("assigned")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=888_A; SEID=assigned"
	if err := s.accounts.Update(target, 1, "assigned", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.UpdatePlaybackDirectory(target, 2, "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, id, "assigned", target); err != nil {
		t.Fatal(err)
	}
	if s.record(id).AccountID != a.ID || s.record(id).DirectoryID != "123" {
		t.Fatal("personal association discarded")
	}
	view := s.AdminMembers()[0]
	if view.PersonalDrive.UID != "123" || !view.PersonalDrive.Connected {
		t.Fatal("inactive drive missing")
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "SEID") || strings.Contains(string(raw), "secret-cookie") {
		t.Fatal("credential exposed")
	}
	if err := s.Suspend(ctx, id); err != nil {
		t.Fatal(err)
	}
	a, _ = s.accounts.Get(a.ID)
	assigned, _ := s.accounts.Get(target)
	if a.Cookie != "" || a.UID != "123" || a.PlaybackParentCID != "123" || assigned.Cookie != cookie {
		t.Fatal("disconnect changed identity, directory or assigned account")
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err != nil {
		t.Fatal("inactive disconnect revoked playback", err)
	}
	if f.users[r.EmbyID].Policy["EnableMediaPlayback"] != true || s.record(id).Suspended {
		t.Fatal("inactive disconnect paused playback")
	}
	if err := s.SetSource(ctx, id, "personal", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("cleared personal credential fell back")
	}
	memberScan(t, s, id)
	if s.record(id).Stage != "ready" || f.dirs != 1 {
		t.Fatal("personal drive failed to resume or created a duplicate directory")
	}
}

func TestMemberDriveTransactionRollback(t *testing.T) {
	s, f, _ := memberFixture(t)
	ctx := context.Background()
	r := model.MemberPlayback{UserID: strings.Repeat("f", 32), Source: "personal"}
	// Missing website user forces a foreign-key failure after account writes.
	if _, err := s.accounts.BindMember(ctx, r, "missing", f.cookie, 0); err == nil {
		t.Fatal("missing owner accepted")
	}
	if len(s.accounts.List()) != 0 {
		t.Fatal("failed transaction changed memory")
	}
	reopened, err := OpenP115AccountsSQLite(ctx, s.db, t.TempDir(), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.List()) != 0 {
		t.Fatal("failed transaction persisted orphan account")
	}
	records, err := s.db.MemberPlaybackRecords(ctx)
	if err != nil || len(records) != 0 {
		t.Fatal("failed transaction persisted association", err)
	}
}

func TestPrepareMemberDirectoryRejectsWrongOwnership(t *testing.T) {
	for _, target := range []string{"primary", "secondary", "another-member"} {
		t.Run(target, func(t *testing.T) {
			s, f, id := memberFixture(t)
			ctx := context.Background()
			var accountID string
			var err error
			if target == "another-member" {
				memberScan(t, s, id)
				accountID = s.record(id).AccountID
				id = strings.Repeat("e", 32)
			} else {
				if target == "primary" {
					accountID, err = s.accounts.AddPrimary("primary")
				} else {
					accountID, err = s.accounts.Add("secondary")
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := s.accounts.Update(accountID, 1, target, &f.cookie); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.accounts.Get(accountID)
			if err != nil {
				t.Fatal(err)
			}
			dirs := f.dirs
			r := model.MemberPlayback{UserID: id, AccountID: accountID, Source: "personal"}
			if _, err := s.prepareDirectory(ctx, r); err == nil {
				t.Fatal("foreign drive accepted")
			}
			after, err := s.accounts.Get(accountID)
			if err != nil || after.Revision != before.Revision || after.PlaybackParentCID != before.PlaybackParentCID || f.dirs != dirs {
				t.Fatal("foreign drive changed")
			}
		})
	}
}

func TestMemberDirectoryIncludesUnregisteredOwnerWithoutSecrets(t *testing.T) {
	s, _, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	disabled := true
	if err := s.db.UpdateWebsiteUser(ctx, id, "", &disabled); err != nil {
		t.Fatal(err)
	}
	views, err := s.AdminMemberDirectory(ctx)
	if err != nil || len(views) != 1 {
		t.Fatal("directory unavailable", err)
	}
	v := views[0]
	if v.UserID != id || v.WebsiteName != "reader" || !v.WebsiteDisabled || v.EmbyID != "" || v.PersonalDrive.UID != "123" {
		t.Fatal("owner or pending record lost")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"passwordHash", "SEID", "secret-cookie", "tokenVersion"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("directory leaked secret", secret)
		}
	}
}

func TestMemberDriveCleanupPreservesIdentityAndReferences(t *testing.T) {
	for _, kind := range []string{"empty", "uid", "directory", "copy", "binding"} {
		t.Run(kind, func(t *testing.T) {
			s, f, id := memberFixture(t)
			ctx := context.Background()
			r, err := s.ensure(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			r.AccountID = "member_" + id
			a := P115Account{ID: r.AccountID, Name: "old personal", Revision: 1}
			if kind == "uid" {
				a.UID = "123"
			}
			if kind == "directory" {
				r.DirectoryID = "123"
			}
			value := s.accounts.copy()
			value.Accounts = append(value.Accounts, a)
			if err := s.accounts.commitMember(ctx, value, r); err != nil {
				t.Fatal(err)
			}
			if kind == "copy" {
				if err := s.db.SavePlayback(ctx, storage.PlaybackBatch{Copies: []storage.PlaybackCopyRow{{ID: "copy", AccountID: a.ID, Record: "{}"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "binding" {
				raw := `{"accountId":"` + a.ID + `"}`
				if err := s.db.SavePlayback(ctx, storage.PlaybackBatch{Settings: &raw}); err != nil {
					t.Fatal(err)
				}
			}
			restarted, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, f, f, f)
			if err != nil {
				t.Fatal(err)
			}
			r = restarted.record(id)
			if kind == "empty" {
				if r.AccountID != "" || len(s.accounts.List()) != 0 {
					t.Fatal("empty placeholder survived")
				}
			} else {
				kept, err := s.accounts.Get(a.ID)
				if err != nil || r.AccountID != a.ID || kept.OwnerUserID != id || kept.UID != a.UID {
					t.Fatal("identity or referenced account removed")
				}
			}
			persisted, err := s.db.MemberPlaybackRecords(ctx)
			if err != nil || len(persisted) != 1 || persisted[0].AccountID != r.AccountID {
				t.Fatal("cleanup association not persisted", err)
			}
		})
	}
}
