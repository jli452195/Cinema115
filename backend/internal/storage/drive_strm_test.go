package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestDriveSTRMSettingsPersistIndependentlyOfMappings(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "drive.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	if _, err := s.GetDriveSTRMSettings(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsaved settings=%v", err)
	}
	settings := model.DriveSTRMSettings{BaseURL: "https://cinema.example", VideoExtensions: "mkv", MetadataExtensions: "ass", MinSizeMB: 10}
	if err := s.SaveDriveSTRMSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	mapping := model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", Settings: settings, UpdatedAt: time.Now().UTC()}
	if err := s.SaveDriveSTRM(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM drive_strm_mappings WHERE id='mapping'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "baseUrl") || strings.Contains(raw, "videoExtensions") || strings.Contains(raw, "settings") {
		t.Fatalf("runtime settings persisted inside mapping: %s", raw)
	}
	if err := s.DeleteDriveSTRM(ctx, mapping.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDriveSTRMSettings(ctx)
	if err != nil || got != settings {
		t.Fatalf("settings changed after mapping removal/restart: %+v %v", got, err)
	}
}

func TestDriveSTRMSettingsMissingPeriodsUseDefaultsAndZeroDisables(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		raw                    string
		event, reconcile, full int
	}{
		{`{"baseUrl":"https://cinema.example","videoExtensions":"mkv"}`, 20, 24, 168},
		{`{"eventIntervalSeconds":0,"reconcileIntervalHours":0,"fullScanIntervalHours":0}`, 0, 0, 0},
		{`{"eventIntervalSeconds":300,"reconcileIntervalHours":6,"fullScanIntervalHours":24}`, 300, 6, 24},
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO drive_strm_settings(id,record) VALUES(1,?)`, tc.raw); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetDriveSTRMSettings(ctx)
		if err != nil || got.EventIntervalSeconds != tc.event || got.ReconcileIntervalHours != tc.reconcile || got.FullScanIntervalHours != tc.full {
			t.Fatalf("decode settings=%+v err=%v", got, err)
		}
	}
}

func TestDriveSTRMStorageUpgradesMissingDirectoryColumn(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint("populated=", populated), func(t *testing.T) {
			ctx := context.Background()
			filename := filepath.Join(t.TempDir(), "drive.db")
			s, err := Open(ctx, filename)
			if err != nil {
				t.Fatal(err)
			}
			files := []model.DriveSTRMFile{
				{MappingID: "mapping", FileID: "1", Path: "a.strm", Digest: "bytes"},
				{MappingID: "mapping", FileID: "200", Path: "Season 01", Directory: true},
			}
			wantFiles, wantDirectories := 0, 0
			if populated {
				wantFiles, wantDirectories = len(files), 1
				if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					if err := s.SaveDriveSTRMFile(ctx, file); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.SaveDriveSTRMCursor(ctx, "primary", "123"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filename)
			if err != nil {
				t.Fatal(err)
			}
			_, schemaErr := db.Exec(`DROP INDEX drive_strm_directories; DROP INDEX drive_strm_paths; ALTER TABLE drive_strm_files DROP COLUMN directory; PRAGMA user_version=63;`)
			closeErr := db.Close()
			if schemaErr != nil || closeErr != nil {
				t.Fatalf("prepare development schema: %v, close: %v", schemaErr, closeErr)
			}
			// Reopen twice: upgrade existing development tables, then verify
			// ordinary startup does not try to add the same column again.
			for range 2 {
				s, err = Open(ctx, filename)
				if err != nil {
					t.Fatal(err)
				}
				var indexes int
				if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='drive_strm_paths'`).Scan(&indexes); err != nil || indexes != 1 {
					t.Fatalf("lookup indexes=%d err=%v", indexes, err)
				}
				all, err := s.ListDriveSTRMFiles(ctx, "mapping")
				if err != nil || len(all) != wantFiles {
					t.Fatalf("files=%+v err=%v", all, err)
				}
				directories, err := s.ListDriveSTRMDirectories(ctx, "mapping")
				if err != nil || len(directories) != wantDirectories {
					t.Fatalf("directories=%+v err=%v", directories, err)
				}
				if populated {
					if directories[0] != files[1] {
						t.Fatalf("file ownership changed: %+v", all)
					}
					for _, file := range all {
						if file != files[0] && file != files[1] {
							t.Fatalf("unexpected file ownership: %+v", file)
						}
					}
					if cursor, err := s.LoadDriveSTRMCursor(ctx, "primary"); err != nil || cursor != "123" {
						t.Fatalf("cursor=%q err=%v", cursor, err)
					}
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDriveSTRMStorageResetsOwnershipAtomicallyAndPersistsCursor(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "drive.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	value := model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}
	if err := s.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	for _, file := range []model.DriveSTRMFile{{MappingID: "mapping", FileID: "1", Path: "a.strm", Digest: "bytes"}, {MappingID: "mapping", FileID: "200", Path: "Season 01", Directory: true}} {
		if err := s.SaveDriveSTRMFile(ctx, file); err != nil {
			t.Fatal(err)
		}
	}
	directories, err := s.ListDriveSTRMDirectories(ctx, value.ID)
	if err != nil || len(directories) != 1 || directories[0].FileID != "200" {
		t.Fatalf("directories=%+v err=%v", directories, err)
	}
	if cursor, err := s.LoadDriveSTRMCursor(ctx, "primary"); err != nil || cursor != "" {
		t.Fatalf("initial cursor=%q err=%v", cursor, err)
	}
	if err := s.SaveDriveSTRMCursor(ctx, "primary", "18446744073709551616"); err != nil {
		t.Fatal(err)
	}
	value.SourceCID = "300"
	if err := s.SaveDriveSTRMConfiguration(ctx, value, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.GetDriveSTRM(ctx, value.ID)
	if err != nil || saved.SourceCID != "300" {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	files, err := s.ListDriveSTRMFiles(ctx, value.ID)
	if err != nil || len(files) != 0 {
		t.Fatalf("reset files=%+v err=%v", files, err)
	}
	if cursor, err := s.LoadDriveSTRMCursor(ctx, "primary"); err != nil || cursor != "18446744073709551616" {
		t.Fatalf("cursor=%q err=%v", cursor, err)
	}
}

func TestDriveSTRMFileBatchSkipsUnchangedAndRollsBackFailure(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	files := []model.DriveSTRMFile{
		{MappingID: "mapping", FileID: "1", ParentCID: "100", Path: "a.strm", Digest: "original", Signature: "sha:12"},
		{MappingID: "mapping", FileID: "200", ParentCID: "100", Path: "Season 01", Directory: true},
	}
	if err := s.SaveDriveSTRMFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := s.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDriveSTRMFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDriveSTRMFile(ctx, files[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("unchanged records caused %d SQLite writes", after-before)
	}
	changed := files[0]
	changed.Digest = "new"
	if err := s.SaveDriveSTRMFiles(ctx, []model.DriveSTRMFile{changed, {MappingID: "missing", FileID: "2"}}); err == nil {
		t.Fatal("batch with an invalid foreign key succeeded")
	}
	saved, err := s.ListDriveSTRMFilesByIDs(ctx, "mapping", []string{"1"})
	if err != nil || len(saved) != 1 || saved[0] != files[0] {
		t.Fatalf("failed batch changed ownership: %+v err=%v", saved, err)
	}
	if err := s.SaveDriveSTRMFiles(ctx, []model.DriveSTRMFile{changed, files[1]}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	// The last unchanged directory record should not trigger an UPDATE.
	if after != 0 {
		t.Fatalf("unchanged directory was updated: %d", after)
	}
	saved, err = s.ListDriveSTRMFilesByIDs(ctx, "mapping", []string{"1"})
	if err != nil || len(saved) != 1 || saved[0] != changed {
		t.Fatalf("changed file was not persisted: %+v err=%v", saved, err)
	}
}

func TestDriveSTRMFileLookupsPreserveConflictingOwnersAndScope(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"mapping", "other"} {
		if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: id, AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	files := []model.DriveSTRMFile{
		{MappingID: "mapping", FileID: "1", ParentCID: "100", Path: "Season 01/A.strm", Digest: "first"},
		{MappingID: "mapping", FileID: "2", ParentCID: "200", Path: "Season 01/a.strm", Digest: "conflicting owner"},
		{MappingID: "mapping", FileID: "3", ParentCID: "100", Path: "Season 01", Directory: true},
		{MappingID: "other", FileID: "1", ParentCID: "100", Path: "Season 01/A.strm", Digest: "other mapping"},
	}
	if err := s.SaveDriveSTRMFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	matched, err := s.ListDriveSTRMFilesByPaths(ctx, "mapping", []string{"Season 01/A.strm", "Season 01/A.strm"})
	if err != nil || len(matched) != 2 || matched[0] != files[0] || matched[1] != files[1] {
		t.Fatalf("path ownership was collapsed or escaped mapping: %+v err=%v", matched, err)
	}
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = "1"
	}
	ids[500] = "3"
	matched, err = s.ListDriveSTRMFilesByIDs(ctx, "mapping", ids)
	if err != nil || len(matched) != 2 || matched[0] != files[0] || matched[1] != files[2] {
		t.Fatalf("chunked ID lookup=%+v err=%v", matched, err)
	}
	matched, err = s.ListDriveSTRMFilesByIDs(ctx, "mapping", nil)
	if err != nil || len(matched) != 0 {
		t.Fatalf("empty ID lookup=%+v err=%v", matched, err)
	}
}

func TestDriveSTRMExportCleanupIdentityOutlivesMappingAndRestart(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "drive.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDriveSTRMCursor(ctx, "primary", "321"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.SaveDriveSTRMExport(ctx, "primary", "123"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveDriveSTRMExport(ctx, "different", "123"); err == nil {
		t.Fatal("export cleanup identity was reassigned")
	}
	if err := s.DeleteDriveSTRM(ctx, "mapping"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exports, err := s.ListDriveSTRMExports(ctx)
	if err != nil || len(exports) != 1 || exports["123"] != "primary" {
		t.Fatalf("exports=%+v err=%v", exports, err)
	}
	for range 2 {
		if err := s.DeleteDriveSTRMExport(ctx, "123"); err != nil {
			t.Fatal(err)
		}
	}
	exports, err = s.ListDriveSTRMExports(ctx)
	if err != nil || len(exports) != 0 {
		t.Fatalf("exports=%+v err=%v", exports, err)
	}
	if cursor, err := s.LoadDriveSTRMCursor(ctx, "primary"); err != nil || cursor != "321" {
		t.Fatalf("unrelated event cursor changed: %q err=%v", cursor, err)
	}
}
