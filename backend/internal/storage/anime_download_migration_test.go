package storage

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func resetAnimeDownloadsToV45(t *testing.T, s *Store) {
	t.Helper()
	for _, statement := range []string{
		`DROP TABLE anime_drive_operations`,
		`ALTER TABLE anime_releases DROP COLUMN resource_id`,
		`DROP TABLE anime_job_selections`, `DROP TABLE anime_download_jobs`,
		`DROP TABLE anime_resource_claims`, `DROP TABLE anime_resources`,
		`PRAGMA user_version=45`,
	} {
		if _, err := s.db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnimeDownloadMigrationV45PreservesExistingData(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	sub := saveAnimeConfig(t, s, animeConfig(c.Chapter.ID))
	b := createPreview(t, s, c, previewPage(2, time.Now().UnixMilli()), time.Now().UnixMilli())
	resetAnimeDownloadsToV45(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reopened, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := reopened.GetAnimeSubscription(ctx, sub.ID, false)
		if err != nil || !reflect.DeepEqual(got, sub) {
			t.Fatal("subscription changed during migration", err)
		}
		preview, err := reopened.GetAnimePreview(ctx, "admin-a", b.ID)
		if err != nil || !reflect.DeepEqual(preview, b) {
			t.Fatal("saved preview lost", err)
		}
		assertDownloadCounts(t, reopened, 0, 0, 0, 0)
		var version int
		if err := reopened.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
			t.Fatal(version, err)
		}
		rows, err := reopened.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Fatal("foreign keys lost")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnimeDownloadMigrationFailureRollsBack(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	resetAnimeDownloadsToV45(t, s)
	if _, err := s.db.ExecContext(ctx, `CREATE VIEW anime_resources AS SELECT 'invalid' AS id`); err != nil {
		t.Fatal(err)
	}
	before := animeMigrationRows(t, s.db, "sqlite_master")
	if err := s.initialize(ctx); err == nil {
		t.Fatal("invalid existing schema accepted")
	}
	if got := animeMigrationRows(t, s.db, "sqlite_master"); !reflect.DeepEqual(before, got) {
		t.Fatal("failed migration left partial tables")
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != 45 {
		t.Fatal("failed migration changed version", version, err)
	}
}
