package storage

import (
	"context"
	"reflect"
	"testing"
)

func TestAnimeStatusMigrationPreservesTaskFacts(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	before := animeMigrationRows(t, f.store.db, "anime_download_jobs")
	operations := animeMigrationRows(t, f.store.db, "anime_drive_operations")
	if _, err := f.store.db.Exec(`DROP INDEX anime_jobs_status_uid`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`PRAGMA user_version=48`); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := animeMigrationRows(t, reopened.db, "anime_download_jobs"); !reflect.DeepEqual(before, got) {
		t.Fatal("migration changed tasks")
	}
	if got := animeMigrationRows(t, reopened.db, "anime_drive_operations"); !reflect.DeepEqual(operations, got) {
		t.Fatal("migration changed submit authorization")
	}
	var version, indices int
	if err := reopened.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	if err := reopened.db.QueryRow(`SELECT count(*) FROM pragma_index_list('anime_download_jobs') WHERE name='anime_jobs_status_uid'`).Scan(&indices); err != nil || indices != 1 {
		t.Fatal(indices, err)
	}
	got, err := reopened.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || !reflect.DeepEqual(got, job) {
		t.Fatal(got, err)
	}
}

func TestAnimeStatusMigrationFailureRollsBack(t *testing.T) {
	f, _, _ := animeStatusStoreFixture(t)
	for _, query := range []string{`DROP INDEX anime_jobs_status_uid`, `CREATE TABLE anime_jobs_status_uid(id TEXT)`, `PRAGMA user_version=48`} {
		if _, err := f.store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	before := animeMigrationRows(t, f.store.db, "sqlite_master")
	jobs := animeMigrationRows(t, f.store.db, "anime_download_jobs")
	if err := f.store.initialize(context.Background()); err == nil {
		t.Fatal("invalid index object accepted")
	}
	if after := animeMigrationRows(t, f.store.db, "sqlite_master"); !reflect.DeepEqual(before, after) {
		t.Fatal("partial schema escaped rollback")
	}
	if after := animeMigrationRows(t, f.store.db, "anime_download_jobs"); !reflect.DeepEqual(jobs, after) {
		t.Fatal("migration failure mutated jobs")
	}
	var version int
	if err := f.store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 48 {
		t.Fatal(version, err)
	}
}
