package storage

import (
	"context"
	"path/filepath"
	"strm115/backend/internal/model"
	"testing"
)

func TestWebsiteCleanupTransactionAndSharedData(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateWebsiteUser(ctx, model.WebsiteUser{ID: "reader", Username: "reader", PasswordHash: "hash", CreatedAt: "now"}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO follow_requests(kind,tmdb_id,title,poster_url,release_date,created_at,updated_at) VALUES('tv',1,'Show','','','now','now')`,
		`INSERT INTO user_follows(user_id,kind,tmdb_id,active,subscribed_at) VALUES('reader','tv',1,1,'now'),('other','tv',1,1,'now')`,
		`INSERT INTO website_notifications(user_id,kind,tmdb_id,title,message,created_at) VALUES('reader','tv',1,'Show','msg',1),('other','tv',1,'Show','msg',1)`,
		`INSERT INTO website_invites(id,code_hash,created_by,note,created_at,expires_at,used_at,used_by) VALUES('used','hash','admin','',1,9999999999,2,'reader')`,
	} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	r := model.MemberPlayback{UserID: "reader", CleanupState: "deleting", CleanupMode: "none", CleanupName: "reader"}
	if err := s.BeginWebsiteCleanup(ctx, r); err != nil {
		t.Fatal(err)
	}
	// A late transaction error must not delete the login or its related data.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_cleanup BEFORE INSERT ON website_user_cleanup BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	r.CleanupState = "deleted"
	if err := s.FinishWebsiteCleanup(ctx, r); err == nil {
		t.Fatal("failed transaction accepted")
	}
	if _, err := s.WebsiteUser(ctx, "reader"); err != nil {
		t.Fatal("rollback lost website user")
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup`); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishWebsiteCleanup(ctx, r); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM website_users`, 0},
		{`SELECT count(*) FROM member_playback`, 0},
		{`SELECT count(*) FROM user_follows WHERE user_id='reader'`, 0},
		{`SELECT count(*) FROM website_notifications WHERE user_id='reader'`, 0},
		{`SELECT count(*) FROM user_follows WHERE user_id='other'`, 1},
		{`SELECT count(*) FROM website_notifications WHERE user_id='other'`, 1},
		{`SELECT count(*) FROM follow_requests`, 1},
		{`SELECT count(*) FROM website_user_cleanup`, 1},
		{`SELECT count(*) FROM website_invites WHERE id='used' AND used_at=2`, 1},
	} {
		var n int
		if err := s.db.QueryRowContext(ctx, tc.query).Scan(&n); err != nil || n != tc.want {
			t.Fatalf("%s: count=%d err=%v", tc.query, n, err)
		}
	}
	if err := s.CheckWebsiteInvite(ctx, "hash"); err == nil {
		t.Fatal("used invitation resurrected")
	}
}
