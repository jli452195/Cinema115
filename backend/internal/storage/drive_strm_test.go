package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

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
			_, schemaErr := db.Exec(`DROP INDEX drive_strm_directories; ALTER TABLE drive_strm_files DROP COLUMN directory; PRAGMA user_version=63;`)
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
