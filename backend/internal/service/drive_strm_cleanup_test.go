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
	"strm115/backend/internal/storage"
)

func driveSTRMCleanupTestFile(t *testing.T, db *storage.Store, mapping model.DriveSTRM, root, id, relative, content string) model.DriveSTRMFile {
	t.Helper()
	filename := filepath.Join(root, mapping.OutputDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	record := model.DriveSTRMFile{MappingID: mapping.ID, FileID: id, ParentCID: mapping.SourceCID, Path: relative, Digest: driveSTRMDigest([]byte(content))}
	if err := db.SaveDriveSTRMFile(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	return record
}

func driveSTRMCleanupTestDrain(t *testing.T, s *DriveSTRMService) {
	t.Helper()
	for i := 0; i < 32; i++ {
		found, err := s.cleanupDriveSTRMOutput(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return
		}
	}
	t.Fatal("cleanup did not yield after draining due work")
}

type driveSTRMCleanupRetryRecorder struct {
	DriveSTRMStore
	retries int
	next    time.Time
}

func (r *driveSTRMCleanupRetryRecorder) RetryDriveSTRMCleanup(ctx context.Context, job model.DriveSTRMCleanup, reason string, next time.Time) error {
	if err := r.DriveSTRMStore.RetryDriveSTRMCleanup(ctx, job, reason, next); err != nil {
		return err
	}
	r.retries++
	r.next = next
	return nil
}

func TestDriveSTRMCleanupMovedFileSurvivesRestartAndPrunesOnlyOldParents(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "current worker", true: "restart"}[restart], func(t *testing.T) {
			s, db, client, mapping, root := driveSTRMTestSetup(t)
			client.files["100"] = []p115.ShareFile{{ID: "200", Name: "old", Directory: true}}
			client.files["200"] = []p115.ShareFile{{ID: "201", Name: "deep", Directory: true}}
			client.files["201"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			client.files["100"] = []p115.ShareFile{{ID: "300", Name: "new", Directory: true}}
			client.files["300"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
			if err := s.Queue(context.Background(), mapping.ID, true); err != nil {
				t.Fatal(err)
			}
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			if restart {
				s = NewDriveSTRMService(db, s.Accounts, client, root)
			}
			before := len(client.calls)
			driveSTRMCleanupTestDrain(t, s)
			if len(client.calls) != before {
				t.Fatal("local cleanup requested 115")
			}
			if _, err := os.Lstat(filepath.Join(root, "strm", "old")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old file or empty parent chain survived: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(root, "strm", "new", "movie.strm"))
			if err != nil || !strings.Contains(string(data), "/api/drive115/ecjq9ichcb40lzlvx/1/") {
				t.Fatalf("current output changed: %q err=%v", data, err)
			}
			if info, err := os.Stat(filepath.Join(root, "strm")); err != nil || !info.IsDir() {
				t.Fatalf("mapping root was removed: %v", err)
			}
		})
	}
}

func TestDriveSTRMCleanupHandlesChangedContentAndReusedPaths(t *testing.T) {
	for _, action := range []string{"missing new", "modified new", "modified old", "reused old path"} {
		t.Run(action, func(t *testing.T) {
			s, db, client, mapping, root := driveSTRMTestSetup(t)
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			driveSTRMCleanupTestFile(t, db, mapping, root, "1", "old/movie.strm", "old output")
			driveSTRMCleanupTestFile(t, db, mapping, root, "1", "new/movie.strm", "new output")
			oldPath := filepath.Join(root, "strm", "old", "movie.strm")
			newPath := filepath.Join(root, "strm", "new", "movie.strm")
			wantNew := "new output"
			var err error
			switch action {
			case "missing new":
				err = os.Remove(newPath)
			case "modified new":
				wantNew = "user new output"
				err = os.WriteFile(newPath, []byte(wantNew), 0644)
			case "modified old":
				err = os.WriteFile(oldPath, []byte("user old output"), 0644)
			case "reused old path":
				// An attachment or other managed file can have identical bytes.
				// Its new owner still forbids deletion using the first file's digest.
				driveSTRMCleanupTestFile(t, db, mapping, root, "2", "old/movie.strm", "old output")
			}
			if err != nil {
				t.Fatal(err)
			}
			retried := &driveSTRMCleanupRetryRecorder{DriveSTRMStore: s.Store}
			s.Store = retried
			before := len(client.calls)
			driveSTRMCleanupTestDrain(t, s)
			if len(client.calls) != before {
				t.Fatal("local cleanup requested 115")
			}
			if action == "missing new" || action == "reused old path" {
				if data, err := os.ReadFile(oldPath); err != nil || string(data) != "old output" {
					t.Fatalf("missing replacement or new owner lost its output: %q err=%v", data, err)
				}
			} else if _, err := os.Lstat(oldPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("changed content prevented obsolete output cleanup: %v", err)
			}
			if action != "missing new" {
				if data, err := os.ReadFile(newPath); err != nil || string(data) != wantNew {
					t.Fatalf("cleanup changed the current output: %q err=%v", data, err)
				}
			}
			if action == "missing new" {
				jobs, err := db.ListDriveSTRMCleanup(context.Background(), 10)
				if err != nil || len(jobs) != 0 || retried.retries != 1 || !retried.next.After(time.Now()) {
					t.Fatalf("unverified destination did not defer cleanup: due=%+v retries=%d next=%v err=%v", jobs, retried.retries, retried.next, err)
				}
			}
		})
	}
}

func TestDriveSTRMCleanupMoveBackKeepsCurrentPath(t *testing.T) {
	s, db, _, mapping, root := driveSTRMTestSetup(t)
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMCleanupTestFile(t, db, mapping, root, "1", "a/movie.strm", "current output")
	driveSTRMCleanupTestFile(t, db, mapping, root, "1", "b/movie.strm", "intermediate output")
	driveSTRMCleanupTestFile(t, db, mapping, root, "1", "a/movie.strm", "current output")
	driveSTRMCleanupTestDrain(t, s)
	data, err := os.ReadFile(filepath.Join(root, "strm", "a", "movie.strm"))
	if err != nil || string(data) != "current output" {
		t.Fatalf("move back deleted the current output: %q err=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "strm", "b")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("intermediate output or empty directory survived: %v", err)
	}
}

func TestDriveSTRMCleanupWindowsCaseOnlyMoveDoesNotDeleteOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path identity")
	}
	s, db, _, mapping, root := driveSTRMTestSetup(t)
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMCleanupTestFile(t, db, mapping, root, "1", "Season 01/Movie.strm", "current output")
	driveSTRMCleanupTestFile(t, db, mapping, root, "1", "season 01/movie.strm", "current output")
	driveSTRMCleanupTestDrain(t, s)
	if data, err := os.ReadFile(filepath.Join(root, "strm", "season 01", "movie.strm")); err != nil || string(data) != "current output" {
		t.Fatalf("case-only rename removed the same physical file: %q err=%v", data, err)
	}
}

func TestDriveSTRMCleanupAttachmentWaitsForSuccessfulCompletion(t *testing.T) {
	s, db, client, mapping, root := driveSTRMTestSetup(t)
	ctx := context.Background()
	poster := driveSTRMTestPoster()
	poster.Name = "old.jpg"
	client.files["100"] = []p115.ShareFile{poster}
	client.metadata[poster.ID] = []byte("image")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMTestDrainAttachments(t, s)
	poster.Name = "new.jpg"
	poster.SHA1 = strings.Repeat("C", 40) // A changed source cannot reuse old bytes.
	client.files["100"] = []p115.ShareFile{poster}
	client.metadataErr[poster.ID] = errors.New("temporary download failure")
	if err := s.Queue(ctx, mapping.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMTestDrainAttachments(t, s)
	driveSTRMCleanupTestDrain(t, s)
	if data, err := os.ReadFile(filepath.Join(root, "strm", "old.jpg")); err != nil || string(data) != "image" {
		t.Fatalf("failed attachment deleted its old output: %q err=%v", data, err)
	}
	jobs, err := db.ListDriveSTRMAttachments(ctx, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("failed attachment was not queued: %+v %v", jobs, err)
	}
	client.metadataErr[poster.ID] = nil
	if err := db.RetryDriveSTRMAttachment(ctx, jobs[0], "retry now", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	driveSTRMTestDrainAttachments(t, s)
	before := len(client.calls)
	driveSTRMCleanupTestDrain(t, s)
	if len(client.calls) != before {
		t.Fatal("successful attachment cleanup requested 115")
	}
	if _, err := os.Lstat(filepath.Join(root, "strm", "old.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed attachment retained old output: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "strm", "new.jpg")); err != nil || string(data) != "image" {
		t.Fatalf("completed attachment is missing: %q err=%v", data, err)
	}
}

func TestDriveSTRMCleanupMovedAttachmentReusesVerifiedLocalBytes(t *testing.T) {
	s, _, client, mapping, root := driveSTRMTestSetup(t)
	poster := driveSTRMTestPoster()
	poster.Name = "old.jpg"
	client.files["100"] = []p115.ShareFile{poster}
	client.metadata[poster.ID] = []byte("image")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMTestDrainAttachments(t, s)
	poster.Name = "new.jpg"
	client.files["100"] = []p115.ShareFile{poster}
	if err := s.Queue(context.Background(), mapping.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	before := len(client.calls)
	driveSTRMTestDrainAttachments(t, s)
	driveSTRMCleanupTestDrain(t, s)
	if len(client.calls) != before || client.metadataCalls != 1 {
		t.Fatalf("verified attachment move requested 115 again: %v", client.calls[before:])
	}
	if _, err := os.Lstat(filepath.Join(root, "strm", "old.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("moved attachment retained its old output: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "strm", "new.jpg")); err != nil || string(data) != "image" {
		t.Fatalf("local attachment copy is incorrect: %q err=%v", data, err)
	}
}

func TestDriveSTRMCleanupRespectsStopAndChangedRoot(t *testing.T) {
	for _, action := range []string{"stop", "root"} {
		t.Run(action, func(t *testing.T) {
			s, db, _, mapping, root := driveSTRMTestSetup(t)
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			driveSTRMCleanupTestFile(t, db, mapping, root, "1", "old/movie.strm", "old output")
			driveSTRMCleanupTestFile(t, db, mapping, root, "1", "new/movie.strm", "new output")
			var err error
			if action == "stop" {
				err = s.Stop(context.Background(), mapping.ID)
			} else {
				mapping.OutputDir = "other-strm"
				_, err = s.Save(context.Background(), mapping)
			}
			if err != nil {
				t.Fatal(err)
			}
			driveSTRMCleanupTestDrain(t, s)
			if data, err := os.ReadFile(filepath.Join(root, "strm", "old", "movie.strm")); err != nil || string(data) != "old output" {
				t.Fatalf("cleanup continued after %s: %q err=%v", action, data, err)
			}
		})
	}
}

func TestDriveSTRMCleanupDoesNotFollowChangedDirectoryLink(t *testing.T) {
	s, db, _, mapping, root := driveSTRMTestSetup(t)
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMCleanupTestFile(t, db, mapping, root, "1", "old/movie.strm", "old output")
	driveSTRMCleanupTestFile(t, db, mapping, root, "1", "new/movie.strm", "new output")
	oldDirectory := filepath.Join(root, "strm", "old")
	if err := os.Remove(filepath.Join(oldDirectory, "movie.strm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldDirectory); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "movie.strm"), []byte("old output"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, oldDirectory); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	driveSTRMCleanupTestDrain(t, s)
	if data, err := os.ReadFile(filepath.Join(other, "movie.strm")); err != nil || string(data) != "old output" {
		t.Fatalf("cleanup followed a changed directory link: %q err=%v", data, err)
	}
}
