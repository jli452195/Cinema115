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
		{`{"baseUrl":"https://cinema.example","videoExtensions":"mkv"}`, 20, 24, 720},
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

func TestDriveSTRMAttachmentsPreserveRetryAcrossDiscoveryAndRestart(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "drive.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, id := range []string{"mapping", "other"} {
		if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: id, AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	job := driveSTRMStoredAttachment("mapping", "10")
	other := driveSTRMStoredAttachment("other", "20")
	if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{job, other}); err != nil {
		t.Fatal(err)
	}
	queued, err := s.ListDriveSTRMAttachments(ctx, 10)
	if err != nil || len(queued) != 2 {
		t.Fatalf("initial jobs=%+v err=%v", queued, err)
	}
	job = queued[0]
	next := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
	if err := s.RetryDriveSTRMAttachment(ctx, job, "附件暂不可用", next); err != nil {
		t.Fatal(err)
	}
	discovered := job
	discovered.Settings.MetadataFilesPerSecond = 9
	discovered.Attempts, discovered.LastError, discovered.NextAttemptAt = 99, "stale state", time.Now()
	if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{discovered}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	queued, err = s.ListDriveSTRMAttachments(ctx, 10)
	if err != nil || len(queued) != 2 || queued[0].Record.MappingID != "other" {
		t.Fatalf("retry ordering lost across restart: %+v err=%v", queued, err)
	}
	retried := queued[1]
	if retried.ID != job.ID || retried.Attempts != 1 || retried.LastError != "附件暂不可用" || !retried.NextAttemptAt.Equal(next) || retried.Settings != job.Settings {
		t.Fatalf("rediscovery replaced retry or its settings: %+v", retried)
	}
	counts, err := s.DriveSTRMAttachmentCounts(ctx)
	if err != nil || counts["mapping"] != [2]int{1, 1} || counts["other"] != [2]int{1, 0} {
		t.Fatalf("counts=%v err=%v", counts, err)
	}

	changed := retried
	changed.SHA1 = strings.Repeat("B", 40)
	changed.Record.Signature = changed.SHA1 + ":12"
	if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{changed}); err != nil {
		t.Fatal(err)
	}
	staleRecord := job.Record
	staleRecord.Digest = "old-bytes"
	if err := s.CompleteDriveSTRMAttachment(ctx, job, staleRecord); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryDriveSTRMAttachment(ctx, job, "old failure", next); err != nil {
		t.Fatal(err)
	}
	files, err := s.ListDriveSTRMFiles(ctx, "mapping")
	if err != nil || len(files) != 0 {
		t.Fatalf("stale completion wrote ownership: %+v err=%v", files, err)
	}
	queued, err = s.ListDriveSTRMAttachments(ctx, 10)
	if err != nil || len(queued) != 2 || queued[1].ID == job.ID || queued[1].Attempts != 0 || !queued[1].NextAttemptAt.IsZero() {
		t.Fatalf("new content did not reset only its own job: %+v err=%v", queued, err)
	}
	current := queued[1]
	record := current.Record
	record.Digest = "new-bytes"
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_attachment_completion BEFORE INSERT ON drive_strm_files BEGIN SELECT RAISE(ABORT,'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDriveSTRMAttachment(ctx, current, record); err == nil {
		t.Fatal("failed index write acknowledged the attachment")
	}
	counts, err = s.DriveSTRMAttachmentCounts(ctx)
	if err != nil || counts["mapping"] != [2]int{1, 0} {
		t.Fatalf("failed completion lost its job: %v err=%v", counts, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_attachment_completion`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDriveSTRMAttachment(ctx, current, record); err != nil {
		t.Fatal(err)
	}
	files, err = s.ListDriveSTRMFiles(ctx, "mapping")
	if err != nil || len(files) != 1 || files[0] != record {
		t.Fatalf("completed file=%+v err=%v", files, err)
	}
	counts, err = s.DriveSTRMAttachmentCounts(ctx)
	if err != nil || len(counts) != 1 || counts["other"] != [2]int{1, 0} {
		t.Fatalf("completion removed another job or retained completed job: %v err=%v", counts, err)
	}
}

func TestDriveSTRMAttachmentsRespectMappingLifecycle(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mappings := []model.DriveSTRM{
		{ID: "running", Status: "running"},
		{ID: "stopped", Status: "stopped"},
		{ID: "queued", Status: "idle", Queued: true},
		{ID: "paused", Status: "idle", Enabled: false},
	}
	for i := range mappings {
		mapping := &mappings[i]
		mapping.AccountID, mapping.SourceCID, mapping.UpdatedAt = "primary", "100", time.Now().UTC()
		mapping.MetadataPending, mapping.MetadataFailed = 200, 100
		if err := s.SaveDriveSTRM(ctx, *mapping); err != nil {
			t.Fatal(err)
		}
		if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{driveSTRMStoredAttachment(mapping.ID, fmt.Sprint(i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := s.ListDriveSTRMAttachments(ctx, 1)
	if err != nil || len(queued) != 1 || queued[0].Record.MappingID != "paused" {
		t.Fatalf("running/stopped/queued mapping filled candidate limit or paused work stalled: %+v err=%v", queued, err)
	}
	stored, err := s.GetDriveSTRM(ctx, "paused")
	if err != nil || stored.MetadataPending != 0 || stored.MetadataFailed != 0 {
		t.Fatalf("runtime counts persisted: %+v err=%v", stored, err)
	}
	if err := s.DeleteDriveSTRMAttachment(ctx, queued[0].ID); err != nil {
		t.Fatal(err)
	}
	mappings[0].SourceCID = "300"
	if err := s.SaveDriveSTRMConfiguration(ctx, mappings[0], true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDriveSTRM(ctx, "stopped"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDriveSTRM(ctx, "queued"); err != nil {
		t.Fatal(err)
	}
	counts, err := s.DriveSTRMAttachmentCounts(ctx)
	if err != nil || len(counts) != 0 {
		t.Fatalf("mapping reset/deletion left attachment jobs: %v err=%v", counts, err)
	}
	if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{driveSTRMStoredAttachment("paused", "4"), driveSTRMStoredAttachment("missing", "5")}); err == nil {
		t.Fatal("invalid mapping in attachment batch did not fail")
	}
	counts, err = s.DriveSTRMAttachmentCounts(ctx)
	if err != nil || len(counts) != 0 {
		t.Fatalf("failed attachment batch left partial work: %v err=%v", counts, err)
	}
	invalidFile := driveSTRMStoredAttachment("paused", "4")
	invalidFile.PickCode, invalidFile.SHA1, invalidFile.Size = "", "", 0
	if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{invalidFile}); err != nil {
		t.Fatal(err)
	}
	queued, err = s.ListDriveSTRMAttachments(ctx, 10)
	if err != nil || len(queued) != 1 || queued[0].Record.FileID != "4" {
		t.Fatalf("invalid attachment identity rejected the scan instead of its own worker: %+v err=%v", queued, err)
	}
	if err := s.DeleteDriveSTRMAttachments(ctx, "paused"); err != nil {
		t.Fatal(err)
	}
	counts, err = s.DriveSTRMAttachmentCounts(ctx)
	if err != nil || len(counts) != 0 {
		t.Fatalf("stop cleanup left jobs: %v err=%v", counts, err)
	}
}

func driveSTRMStoredAttachment(mappingID, fileID string) model.DriveSTRMAttachment {
	sha1 := strings.Repeat("A", 40)
	return model.DriveSTRMAttachment{
		Record:    model.DriveSTRMFile{MappingID: mappingID, FileID: fileID, ParentCID: "100", Path: fileID + ".ass", Signature: sha1 + ":12"},
		AccountID: "primary",
		SourceCID: "100",
		OutputDir: "strm",
		Settings:  model.DefaultDriveSTRMSettings(),
		Name:      fileID + ".ass",
		PickCode:  "abcdef",
		SHA1:      sha1,
		Size:      12,
	}
}

func TestDriveSTRMCleanupTracksOnlyCommittedReplacements(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	old := model.DriveSTRMFile{MappingID: "mapping", FileID: "1", ParentCID: "100", Path: "old/a.strm", Digest: "old-bytes"}
	if err := s.SaveDriveSTRMFile(ctx, old); err != nil {
		t.Fatal(err)
	}
	moved := old
	moved.Path, moved.Digest = "new/a.strm", "new-bytes"
	if err := s.SaveDriveSTRMFiles(ctx, []model.DriveSTRMFile{moved, {MappingID: "missing", FileID: "2"}}); err == nil {
		t.Fatal("invalid batch succeeded")
	}
	queued, err := s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(queued) != 0 {
		t.Fatalf("failed index update left cleanup: %+v err=%v", queued, err)
	}
	files, err := s.ListDriveSTRMFiles(ctx, "mapping")
	if err != nil || len(files) != 1 || files[0] != old {
		t.Fatalf("failed index update changed ownership: %+v err=%v", files, err)
	}
	if err := s.SaveDriveSTRMFile(ctx, moved); err != nil {
		t.Fatal(err)
	}
	queued, err = s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(queued) != 1 || queued[0].Record != old {
		t.Fatalf("successful replacement lost old ownership: %+v err=%v", queued, err)
	}
	for _, tc := range []struct {
		name     string
		old, new model.DriveSTRMFile
	}{
		{name: "unchanged-path", old: old, new: old},
		{name: "old-directory", old: model.DriveSTRMFile{Directory: true, Digest: "old"}, new: model.DriveSTRMFile{Digest: "new"}},
		{name: "new-directory", old: model.DriveSTRMFile{Digest: "old"}, new: model.DriveSTRMFile{Directory: true, Digest: "new"}},
		{name: "old-unowned", old: model.DriveSTRMFile{}, new: model.DriveSTRMFile{Digest: "new"}},
		{name: "new-unowned", old: model.DriveSTRMFile{Digest: "old"}, new: model.DriveSTRMFile{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := tc.old, tc.new
			before.MappingID, before.FileID, before.Path = "mapping", tc.name, "old/"+tc.name
			after.MappingID, after.FileID = "mapping", tc.name
			if tc.name == "unchanged-path" {
				after.Path = before.Path
				after.Digest = "changed-bytes"
			} else {
				after.Path = "new/" + tc.name
			}
			if err := s.SaveDriveSTRMFile(ctx, before); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveDriveSTRMFile(ctx, after); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.ListDriveSTRMCleanup(ctx, 10)
			if err != nil || len(jobs) != 1 || jobs[0].Record != old {
				t.Fatalf("ineligible replacement queued cleanup: %+v err=%v", jobs, err)
			}
		})
	}
}

func TestDriveSTRMCleanupAttachmentCompletionIsAtomicAndTokenBound(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	job := driveSTRMStoredAttachment("mapping", "10")
	old := job.Record
	old.Path, old.Digest = "old/10.ass", "old-bytes"
	if err := s.SaveDriveSTRMFile(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{job}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListDriveSTRMAttachments(ctx, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("attachments=%+v err=%v", jobs, err)
	}
	stale := jobs[0]
	job = stale
	job.Record.Signature = "new-content"
	if err := s.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{job}); err != nil {
		t.Fatal(err)
	}
	staleRecord := stale.Record
	staleRecord.Digest = "stale-bytes"
	if err := s.CompleteDriveSTRMAttachment(ctx, stale, staleRecord); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListDriveSTRMAttachments(ctx, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("attachments=%+v err=%v", jobs, err)
	}
	job = jobs[0]
	record := job.Record
	record.Digest = "new-bytes"
	invalid := record
	invalid.Path = "wrong.ass"
	if err := s.CompleteDriveSTRMAttachment(ctx, job, invalid); err == nil {
		t.Fatal("mismatched completion succeeded")
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_cleanup_attachment_index BEFORE INSERT ON drive_strm_files BEGIN SELECT RAISE(ABORT,'test index write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDriveSTRMAttachment(ctx, job, record); err == nil {
		t.Fatal("failed index update completed the attachment")
	}
	queued, err := s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(queued) != 0 {
		t.Fatalf("failed/stale attachment left cleanup: %+v err=%v", queued, err)
	}
	files, err := s.ListDriveSTRMFiles(ctx, "mapping")
	if err != nil || len(files) != 1 || files[0] != old {
		t.Fatalf("failed/stale completion changed index: %+v err=%v", files, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_cleanup_attachment_index`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDriveSTRMAttachment(ctx, job, record); err != nil {
		t.Fatal(err)
	}
	queued, err = s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(queued) != 1 || queued[0].Record != old {
		t.Fatalf("successful attachment lost cleanup: %+v err=%v", queued, err)
	}
	files, err = s.ListDriveSTRMFiles(ctx, "mapping")
	if err != nil || len(files) != 1 || files[0] != record {
		t.Fatalf("successful attachment ownership=%+v err=%v", files, err)
	}
}

func TestDriveSTRMCleanupPreservesRetryAcrossRestartAndRejectsStaleToken(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "drive.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	record := model.DriveSTRMFile{MappingID: "mapping", FileID: "1", ParentCID: "100", Path: "old/a.strm", Digest: "old-bytes"}
	if err := s.QueueDriveSTRMCleanup(ctx, []model.DriveSTRMFile{record, record}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("cleanup=%+v err=%v", jobs, err)
	}
	job := jobs[0]
	next := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	if err := s.RetryDriveSTRMCleanup(ctx, job, "文件被占用", next); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueDriveSTRMCleanup(ctx, []model.DriveSTRMFile{record}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Record != record || jobs[0].Attempts != 1 || jobs[0].LastError != "文件被占用" || !jobs[0].NextAttemptAt.Equal(next) {
		t.Fatalf("retry changed after rediscovery/restart: %+v err=%v", jobs, err)
	}
	record.Digest = "different-bytes"
	if err := s.QueueDriveSTRMCleanup(ctx, []model.DriveSTRMFile{record}); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryDriveSTRMCleanup(ctx, job, "stale retry", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDriveSTRMCleanup(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID <= job.ID || jobs[0].Record != record || jobs[0].Attempts != 0 || !jobs[0].NextAttemptAt.IsZero() || jobs[0].LastError != "" {
		t.Fatalf("stale token changed replacement job: %+v err=%v", jobs, err)
	}
	if err := s.DeleteDriveSTRMCleanup(ctx, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("completed cleanup remained: %+v err=%v", jobs, err)
	}
}

func TestDriveSTRMCleanupFiltersLifecycleAndDeadlinesBeforeLimit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, value := range []model.DriveSTRM{
		{ID: "running", Status: "running"},
		{ID: "stopped", Status: "stopped"},
		{ID: "queued", Status: "idle", Queued: true},
		{ID: "paused", Status: "idle", Enabled: false},
	} {
		value.AccountID, value.SourceCID, value.UpdatedAt = "primary", "100", time.Now().UTC()
		if err := s.SaveDriveSTRM(ctx, value); err != nil {
			t.Fatal(err)
		}
		record := model.DriveSTRMFile{MappingID: value.ID, FileID: "1", Path: "old/a.strm", Digest: "old-bytes"}
		if err := s.QueueDriveSTRMCleanup(ctx, []model.DriveSTRMFile{record}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.ListDriveSTRMCleanup(ctx, 1)
	if err != nil || len(jobs) != 1 || jobs[0].Record.MappingID != "paused" {
		t.Fatalf("lifecycle filled candidate limit or paused work stalled: %+v err=%v", jobs, err)
	}
	if err := s.RetryDriveSTRMCleanup(ctx, jobs[0], "稍后重试", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	due := model.DriveSTRMFile{MappingID: "paused", FileID: "2", Path: "old/b.strm", Digest: "old-bytes"}
	if err := s.QueueDriveSTRMCleanup(ctx, []model.DriveSTRMFile{due}); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListDriveSTRMCleanup(ctx, 1)
	if err != nil || len(jobs) != 1 || jobs[0].Record != due {
		t.Fatalf("future deadline filled candidate limit: %+v err=%v", jobs, err)
	}
	for _, limit := range []int{0, -1, 1001} {
		if _, err := s.ListDriveSTRMCleanup(ctx, limit); err == nil {
			t.Fatalf("invalid limit %d succeeded", limit)
		}
	}
	paused, err := s.GetDriveSTRM(ctx, "paused")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDriveSTRMConfiguration(ctx, paused, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"running", "stopped", "queued"} {
		if err := s.DeleteDriveSTRM(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM drive_strm_cleanup`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reset/delete left cleanup: count=%d err=%v", count, err)
	}
}

func TestDriveSTRMCleanupValidatesEvidenceAndQueuesAtomically(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "mapping", AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	valid := model.DriveSTRMFile{MappingID: "mapping", FileID: "1", Path: "old/a.strm", Digest: "old-bytes"}
	for _, field := range []string{"mapping", "file", "path", "digest", "directory", "foreign-key"} {
		invalid := valid
		switch field {
		case "mapping":
			invalid.MappingID = ""
		case "file":
			invalid.FileID = ""
		case "path":
			invalid.Path = ""
		case "digest":
			invalid.Digest = ""
		case "directory":
			invalid.Directory = true
		case "foreign-key":
			invalid.MappingID = "missing"
		}
		if err := s.QueueDriveSTRMCleanup(ctx, []model.DriveSTRMFile{valid, invalid}); err == nil {
			t.Fatalf("invalid %s evidence was accepted", field)
		}
		jobs, err := s.ListDriveSTRMCleanup(ctx, 10)
		if err != nil || len(jobs) != 0 {
			t.Fatalf("invalid %s batch left partial jobs: %+v err=%v", field, jobs, err)
		}
	}
	if err := s.QueueDriveSTRMCleanup(ctx, nil); err != nil {
		t.Fatal(err)
	}
	other := valid
	other.Path = "another/a.strm"
	if err := s.QueueDriveSTRMCleanup(ctx, []model.DriveSTRMFile{valid, valid, other}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListDriveSTRMCleanup(ctx, 10)
	if err != nil || len(jobs) != 2 || jobs[0].Record != valid || jobs[1].Record != other {
		t.Fatalf("distinct old paths were collapsed or duplicates retained: %+v err=%v", jobs, err)
	}
}
