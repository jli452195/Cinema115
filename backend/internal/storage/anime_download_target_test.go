package storage

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/model"
)

func animeDownloadTargetFixture(t *testing.T) (*Store, model.AnimeDownloadTarget) {
	t.Helper()
	s, _ := testStore(t)
	ctx := context.Background()
	if err := s.SaveP115Accounts(ctx, P115AccountState{Accounts: []P115AccountRow{{ID: "main", Name: "主账号", UID: "123", Primary: true, Ownership: "admin", Revision: 2, Cookie: []byte("encrypted")}}}); err != nil {
		t.Fatal(err)
	}
	settings := model.AnimeSettings{Revision: 1, AccountID: animePtr("main"), AccountRevision: animePtr(int64(2)), UID: animePtr("123"), OrganizeCID: animePtr("900"), OrganizePath: animePtr("/organized"), DownloadCID: animePtr("100"), DownloadPath: animePtr("/动漫")}
	if _, err := s.SaveAnimeSettings(ctx, settings, animeAdminKey("target_settings")); err != nil {
		t.Fatal(err)
	}
	return s, model.AnimeDownloadTarget{SchemaVersion: 1, SettingsRevision: 2, AccountID: "main", AccountRevision: 2, UID: "123", ParentCID: "100", ParentPath: "/动漫"}
}

func TestAnimeDownloadTargetNewSettingsAndPinnedAccountGuards(t *testing.T) {
	s, target := animeDownloadTargetFixture(t)
	ctx := context.Background()
	if err := s.CheckAnimeDownloadSettings(ctx, target); err != nil {
		t.Fatal(err)
	}
	// A new default must not alter the directory of an already saved task.
	if _, err := s.db.ExecContext(ctx, `UPDATE anime_settings SET revision=revision+1,download_cid='200',download_path='/新目录' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAnimeDownloadSettings(ctx, target); !errors.Is(err, ErrAnimeSettingsChanged) {
		t.Fatal("stale new-task target accepted", err)
	}
	if err := s.CheckAnimeDownloadAccount(ctx, target); err != nil {
		t.Fatal("old task adopted current defaults", err)
	}
	// The final guard can be used inside the future atomic claim/job write.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := checkAnimeDownloadTargetTx(ctx, tx, target, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE p115_accounts SET revision=revision+1 WHERE id='main'`); err != nil {
		t.Fatal(err)
	}
	if err := checkAnimeDownloadTargetTx(ctx, tx, target, false); !errors.Is(err, ErrAnimeAccountChanged) {
		t.Fatal("transaction missed changed credential version", err)
	}
}

func TestAnimeDownloadTargetPersistentAccountMismatch(t *testing.T) {
	for _, change := range []string{
		`UPDATE p115_accounts SET revision=revision+1`,
		`UPDATE p115_accounts SET uid='999'`,
		`UPDATE p115_accounts SET is_primary=0`,
		`UPDATE p115_accounts SET ownership='member'`,
		`UPDATE p115_accounts SET cookie_enc=X''`,
		`DELETE FROM p115_accounts`,
	} {
		t.Run(change, func(t *testing.T) {
			s, target := animeDownloadTargetFixture(t)
			ctx := context.Background()
			if _, err := s.db.ExecContext(ctx, change); err != nil {
				t.Fatal(err)
			}
			for _, check := range []func(context.Context, model.AnimeDownloadTarget) error{s.CheckAnimeDownloadSettings, s.CheckAnimeDownloadAccount} {
				if err := check(ctx, target); !errors.Is(err, ErrAnimeAccountChanged) {
					t.Fatal("changed account accepted", err)
				}
			}
		})
	}
}

func TestAnimeDownloadTargetPersistentBindingMismatch(t *testing.T) {
	for _, change := range []string{
		`UPDATE anime_settings SET account_id='other'`,
		`UPDATE anime_settings SET account_revision=99`,
		`UPDATE anime_settings SET uid='999'`,
		`UPDATE anime_settings SET download_cid='0'`,
		`UPDATE anime_settings SET download_path='/other'`,
	} {
		t.Run(change, func(t *testing.T) {
			s, target := animeDownloadTargetFixture(t)
			if _, err := s.db.ExecContext(context.Background(), change); err != nil {
				t.Fatal(err)
			}
			if err := s.CheckAnimeDownloadSettings(context.Background(), target); !errors.Is(err, ErrAnimeSettingsChanged) {
				t.Fatal("binding mismatch accepted", err)
			}
		})
	}
}
