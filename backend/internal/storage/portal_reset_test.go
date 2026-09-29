package storage

import (
	"context"
	"strm115/backend/internal/model"
	"testing"
)

func TestPortalResetPreservesAdministratorAccountsAndSettings(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	u := model.PortalUser{ID: "owner", ServerID: "server", EmbyID: "emby", Username: "reader", CreatedAt: "now"}
	if err := s.SaveEmbyProfile(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMemberPlayback(ctx, model.MemberPlayback{UserID: u.ID, EmbyID: u.EmbyID, ServerID: u.ServerID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveP115Accounts(ctx, P115AccountState{Accounts: []P115AccountRow{{ID: "main", Name: "main", Primary: true, Ownership: "admin", Revision: 1, Cookie: []byte{1}}, {ID: "personal", Name: "personal", Ownership: "member", OwnerUserID: u.ID, Revision: 1, Cookie: []byte{1}}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMemberPlaybackSettings(ctx, model.MemberPlaybackSettings{Revision: 4, PublicURL: "https://play.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE website_users(id TEXT); CREATE TABLE website_invites(id TEXT); CREATE TABLE website_user_cleanup(id TEXT);`); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPortal(ctx); err != nil {
		t.Fatal(err)
	}
	accounts, err := s.LoadP115Accounts(ctx)
	if err != nil || len(accounts.Accounts) != 1 || accounts.Accounts[0].ID != "main" {
		t.Fatal(accounts, err)
	}
	settings, err := s.MemberPlaybackSettings(ctx)
	if err != nil || settings.Revision != 4 {
		t.Fatal(settings, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name IN ('website_users','website_invites','website_user_cleanup')`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM emby_profiles`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}
