package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestDriveSTRMEventsDoNotPostponeReconciliation(t *testing.T) {
	s, db, client, value, _ := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	before, _ := db.GetDriveSTRM(context.Background(), value.ID)
	if err := s.NotifyDirectories(context.Background(), value.AccountID, []string{"100"}); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetDriveSTRM(context.Background(), value.ID)
	if after.Mode != "changes" || !after.NextScanAt.Equal(before.NextScanAt) || !after.LastFullScanAt.Equal(before.LastFullScanAt) {
		t.Fatalf("event moved periodic deadlines: before=%+v after=%+v", before, after)
	}
}

func TestDriveSTRMDueReconciliationWinsOverQueuedEvents(t *testing.T) {
	s, db, client, value, _ := driveSTRMTreeSetup(t)
	driveSTRMTreeDue(t, db, value.ID)
	if err := s.NotifyDirectories(context.Background(), value.AccountID, []string{"200"}); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	saved, _ := db.GetDriveSTRM(context.Background(), value.ID)
	if saved.Mode != "tree" || len(client.exportCalls) == 0 {
		t.Fatalf("queued event starved due tree: %+v", saved)
	}
	saved.NextScanAt = time.Now().Add(-time.Second)
	saved.LastFullScanAt = time.Now().Add(-driveSTRMFullInterval - time.Hour)
	if err := db.SaveDriveSTRM(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	saved, _ = db.GetDriveSTRM(context.Background(), value.ID)
	if saved.Mode != "full" || time.Since(saved.LastFullScanAt) > time.Minute {
		t.Fatalf("weekly metadata scan skipped: %+v", saved)
	}
}

func TestDriveSTRMFailedScanRetainsFullRetry(t *testing.T) {
	s, db, client, value, _ := driveSTRMTreeSetup(t)
	driveSTRMTreeDue(t, db, value.ID)
	client.pending = true
	// An earlier accepted export is pending; this scan must fail and request
	// a full metadata retry without submitting another cloud export.
	if err := db.SaveDriveSTRMExport(context.Background(), value.AccountID, "12345"); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err == nil {
		t.Fatal("pending cleanup did not stop another export")
	}
	saved, _ := db.GetDriveSTRM(context.Background(), value.ID)
	saved.NextScanAt = time.Now().Add(-time.Second)
	if err := db.SaveDriveSTRM(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	saved, _ = db.GetDriveSTRM(context.Background(), value.ID)
	if saved.Mode != "full" {
		t.Fatalf("full retry was erased: %+v", saved)
	}
}

func TestDriveSTRMUnsupportedTreeRecordsCompletedFullFallback(t *testing.T) {
	s, db, client, value, _ := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMTreeDue(t, db, value.ID)
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	saved, _ := db.GetDriveSTRM(context.Background(), value.ID)
	if saved.Mode != "full" || time.Since(saved.LastFullScanAt) > time.Minute {
		t.Fatalf("fallback wasn't checkpointed: %+v", saved)
	}
}

func TestDriveSTRMTreeReusedDirectoryPathFallsBackToRoot(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "renamed", true: "deleted"}[deleted], func(t *testing.T) {
			s, db, client, value, root := driveSTRMTreeSetup(t)
			client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv"), {ID: "300", Name: "Season 01", Directory: true}}
			client.files["200"] = nil
			client.paths["200"] = "/library/moved"
			if deleted {
				delete(client.paths, "200")
			}
			client.paths["300"] = "/library/Season 01"
			client.files["300"] = []p115.ShareFile{driveSTRMTestVideo("3", "new.mkv")}
			client.leaves = []string{"movie.mkv", "Season 01/new.mkv", "poster.jpg"}
			driveSTRMTreeDue(t, db, value.ID)
			if stats, err := driveSTRMTestAttempt(t, s); err != nil || stats.Written != 1 {
				t.Fatalf("path reuse not repaired: %+v %v", stats, err)
			}
			if _, err := os.Stat(filepath.Join(root, "strm", "Season 01", "new.strm")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDriveSTRMChangedScopeCannotMixCollidingDirectory(t *testing.T) {
	s, _, client, value, root := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{{ID: "200", Name: "same?", Directory: true}, {ID: "300", Name: "other", Directory: true}}
	client.paths["200"], client.paths["300"] = "/library/same?", "/library/other"
	client.files["200"] = []p115.ShareFile{driveSTRMTestVideo("1", "first.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	client.files["100"][1].Name = "same*"
	client.paths["300"] = "/library/same*"
	client.files["300"] = []p115.ShareFile{driveSTRMTestVideo("2", "second.mkv")}
	if err := s.NotifyDirectories(context.Background(), value.AccountID, []string{"300"}); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err == nil {
		t.Fatal("collision wasn't reported")
	}
	if _, err := os.Stat(filepath.Join(root, "strm", "same_", "second.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scope mixed in another directory: %v", err)
	}
}

func TestDriveSTRMCaseOnlyRenameUsesPlatformPathIdentity(t *testing.T) {
	s, _, client, value, root := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	client.files["100"][0].Name = "Movie.mkv"
	if err := s.Queue(context.Background(), value.ID, true); err != nil {
		t.Fatal(err)
	}
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Written != 1 {
		t.Fatalf("rename stats=%+v err=%v", stats, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "strm", "Movie.strm"))
	if err != nil || !strings.Contains(string(data), "Movie.mkv") {
		t.Fatalf("rename output incorrect: %v", err)
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(filepath.Join(root, "strm", "movie.strm")); err != nil {
			t.Fatal("case-sensitive old output removed")
		}
	}
}
