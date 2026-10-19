package storage

import (
	"context"
	"reflect"
	"testing"
)

func TestAnimeSubmitMigrationV46PreservesJobsAndSelections(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	var databasePath string
	rows, err := f.store.db.Query(`PRAGMA database_list`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			databasePath = file
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if databasePath == "" {
		t.Fatal("main database path missing")
	}
	for _, statement := range []string{`DROP TABLE anime_drive_operations`, `PRAGMA user_version=46`} {
		if _, err := f.store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(saved, job) {
		t.Fatal("migration changed existing job", saved, job, err)
	}
	op, err := reopened.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
	if err != nil || op.State != "prepared" {
		t.Fatal("old selections lost on upgrade", op, err)
	}
	var version int
	if err := reopened.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	check, err := reopened.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	if check.Next() {
		t.Fatal("migration broke foreign keys")
	}
	if err := check.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeSubmitMigrationFailureRollsBack(t *testing.T) {
	f, _ := animeSubmitStoreFixture(t)
	for _, statement := range []string{`DROP TABLE anime_drive_operations`, `PRAGMA user_version=46`, `CREATE VIEW anime_drive_operations AS SELECT 'bad' AS id`} {
		if _, err := f.store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	before := animeMigrationRows(t, f.store.db, "sqlite_master")
	if err := f.store.initialize(context.Background()); err == nil {
		t.Fatal("invalid operation schema accepted")
	}
	if got := animeMigrationRows(t, f.store.db, "sqlite_master"); !reflect.DeepEqual(got, before) {
		t.Fatal("failed migration left partial objects")
	}
	var version int
	if err := f.store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 46 {
		t.Fatal(version, err)
	}
}
