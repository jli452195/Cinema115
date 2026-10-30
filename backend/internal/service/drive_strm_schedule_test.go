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
	"strm115/backend/internal/model"
)

func TestDriveSTRMPeriodsAreIndependentAndChangingSettingsDoesNotQueue(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		reconcile, full               int
		reconcileEnabled, fullEnabled bool
		want                          string
	}{
		{"disabled with retained periods", 24, 720, false, false, ""},
		{"full before next tree", 168, 24, true, true, "full"},
		{"full without tree", 6, 24, false, true, "full"},
		{"tree without full", 6, 24, true, false, "tree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, _, mapping, _ := driveSTRMTestSetup(t)
			ctx := context.Background()
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			value, _ := db.GetDriveSTRM(ctx, mapping.ID)
			value.LastReconcileAt = time.Now().Add(-7 * time.Hour)
			value.LastFullScanAt = time.Now().Add(-25 * time.Hour)
			if err := db.SaveDriveSTRM(ctx, value); err != nil {
				t.Fatal(err)
			}
			settings, _ := s.Settings(ctx)
			settings.ReconcileIntervalHours, settings.FullScanIntervalHours = tc.reconcile, tc.full
			settings.ReconcileEnabled, settings.FullScanEnabled = tc.reconcileEnabled, tc.fullEnabled
			if _, err := s.SaveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			if saved, err := s.Settings(ctx); err != nil || saved != settings {
				t.Fatalf("switches changed selected periods: %+v err=%v", saved, err)
			}
			stored, _ := db.GetDriveSTRM(ctx, mapping.ID)
			if stored.Queued || stored.Status != "idle" || stored.NextScanAt != value.NextScanAt {
				t.Fatalf("settings save changed idle mapping: %+v", stored)
			}
			listed, err := s.List(ctx)
			if err != nil || len(listed) != 1 || listed[0].NextScanAt.IsZero() != (tc.want == "") {
				t.Fatalf("projected schedule=%+v err=%v", listed, err)
			}
			storedAfterList, _ := db.GetDriveSTRM(ctx, mapping.ID)
			if storedAfterList.NextScanAt != stored.NextScanAt {
				t.Fatal("GET schedule projection persisted a retry deadline")
			}
			attempt, _, found, err := s.next(ctx)
			if err != nil || found != (tc.want != "") || found && attempt.Mode != tc.want {
				t.Fatalf("periodic selection: found=%v mode=%s err=%v", found, attempt.Mode, err)
			}
			if found {
				if err := s.finish(ctx, mapping.ID, model.DriveSTRMStats{}, nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDriveSTRMDisabledPeriodsKeepAcceptedWorkAndFailedRetries(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	settings, _ := s.Settings(ctx)
	settings.EventIntervalSeconds = 0
	settings.ReconcileEnabled, settings.FullScanEnabled = false, false
	if _, err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	// Creation was already accepted before all automatic periods were closed.
	client.pageErr = errors.New("temporary network failure")
	if _, err := driveSTRMTestAttempt(t, s); err == nil {
		t.Fatal("failed scan succeeded")
	}
	value, _ := db.GetDriveSTRM(ctx, mapping.ID)
	value.NextScanAt = time.Now().Add(-time.Second)
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMonitoring(ctx, mapping.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatalf("paused directory automatically retried: found=%v err=%v", found, err)
	}
	// Re-enable without creating a fresh queue: the persisted retry remains
	// eligible even though both scheduled scans are disabled globally.
	value, _ = db.GetDriveSTRM(ctx, mapping.ID)
	value.Enabled = true
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	client.pageErr = nil
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMonitoring(ctx, mapping.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(ctx, mapping.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatalf("manual full scan blocked by disabled periods or monitoring: %v", err)
	}
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatalf("disabled zero timestamps unexpectedly due: found=%v err=%v", found, err)
	}
}

func TestDriveSTRMPausedDirectoryCompletesAlreadyAcceptedEvent(t *testing.T) {
	s, db, _, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	if err := s.NotifyDirectories(ctx, mapping.AccountID, []string{mapping.SourceCID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMonitoring(ctx, mapping.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	if err := s.NotifyDirectories(ctx, mapping.AccountID, []string{mapping.SourceCID}); err != nil {
		t.Fatal(err)
	}
	value, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if value.Queued {
		t.Fatal("paused directory accepted a new automatic event")
	}
}

func TestDriveSTRMDisabledPeriodsDoNotTreatZeroCheckpointsAsDue(t *testing.T) {
	s, db, _, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	settings, _ := s.Settings(ctx)
	settings.ReconcileEnabled, settings.FullScanEnabled = false, false
	if _, err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	mapping.Queued, mapping.FullScan, mapping.Status = false, false, "idle"
	if err := db.SaveDriveSTRM(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatalf("closed periods with zero checkpoints triggered: found=%v err=%v", found, err)
	}
}

func TestDriveSTRMStoppingDoesNotForceDisabledFullScan(t *testing.T) {
	s, db, _, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	settings, _ := s.Settings(ctx)
	settings.ReconcileEnabled, settings.ReconcileIntervalHours, settings.FullScanEnabled = true, 6, false
	if _, err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, mapping.ID); err != nil {
		t.Fatal(err)
	}
	value, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if value.FullScan || value.Queued {
		t.Fatal("stopped attempt retained accepted full scan")
	}
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatalf("stop immediately restarted due periodic work: found=%v err=%v", found, err)
	}
	value.UpdatedAt = time.Now().Add(-7 * time.Hour)
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	attempt, _, found, err := s.next(ctx)
	if err != nil || !found || attempt.Mode != "tree" {
		t.Fatalf("stopped work forced full scan: found=%v mode=%s err=%v", found, attempt.Mode, err)
	}
	if err := s.finish(ctx, mapping.ID, model.DriveSTRMStats{}, nil); err != nil {
		t.Fatal(err)
	}
}

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
	if after.Mode != "changes" || !after.LastReconcileAt.Equal(before.LastReconcileAt) || !after.LastFullScanAt.Equal(before.LastFullScanAt) {
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
	settings, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.FullScanEnabled = true
	if _, err := s.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	saved.LastReconcileAt = time.Now().Add(-25 * time.Hour)
	saved.LastFullScanAt = time.Now().Add(-721 * time.Hour)
	if err := db.SaveDriveSTRM(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	saved, _ = db.GetDriveSTRM(context.Background(), value.ID)
	if saved.Mode != "full" || time.Since(saved.LastFullScanAt) > time.Minute {
		t.Fatalf("monthly metadata scan skipped: %+v", saved)
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
