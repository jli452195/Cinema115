package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func TestDriveSTRMSameSourcePathRefreshesFileIDWithoutExtraRequests(t *testing.T) {
	for _, mode := range []string{"bulk", "changes", "old ID moved"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, db, base, mapping, root := driveSTRMTestSetup(t)
			base.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			next := driveSTRMTestVideo("2", "movie.mkv")
			if mode != "bulk" {
				next.SHA1, next.Size, next.PickCode = strings.Repeat("B", 40), 200, "newpickcode"
			}
			base.files["100"] = []p115.ShareFile{next}
			wantCalls := []string{"identity:100:UID=42_A", "list:100:UID=42_A"}
			wantWritten := 1
			if mode == "bulk" {
				s.Client = &driveSTRMBulkTestClient{driveSTRMTestClient: base, batches: [][]p115.DriveTreeEntry{{{ShareFile: next, ParentCID: "100", RelativePath: next.Name}}}}
				wantCalls = wantCalls[:1]
			}
			if mode == "old ID moved" {
				base.files["100"] = append([]p115.ShareFile{driveSTRMTestVideo("1", "moved.mkv")}, next)
				wantWritten++
			}
			base.calls = nil
			stats, err := driveSTRMReplacementTestScan(t, s, mapping, mode == "changes")
			if err != nil || stats.Written != wantWritten || !reflect.DeepEqual(base.calls, wantCalls) {
				t.Fatalf("stats=%+v requests=%v err=%v", stats, base.calls, err)
			}
			filename := filepath.Join(root, "strm", "movie.strm")
			out, err := os.ReadFile(filename)
			if err != nil || !strings.Contains(string(out), "/"+next.PickCode+"/2/"+next.SHA1+"/") {
				t.Fatalf("output=%q err=%v", out, err)
			}
			owners, err := db.ListDriveSTRMFilesByPaths(ctx, mapping.ID, []string{"movie.strm"})
			if err != nil || len(owners) != 1 || owners[0].FileID != "2" {
				t.Fatalf("owners=%+v err=%v", owners, err)
			}
			if mode == "old ID moved" {
				moved, err := db.ListDriveSTRMFilesByIDs(ctx, mapping.ID, []string{"1"})
				if err != nil || len(moved) != 1 || moved[0].Path != "moved.strm" {
					t.Fatalf("moved=%+v err=%v", moved, err)
				}
			}
			jobs, err := db.ListDriveSTRMCleanup(ctx, 10)
			if err != nil || len(jobs) != 0 {
				t.Fatalf("obsolete cleanup=%+v err=%v", jobs, err)
			}
			stats, err = driveSTRMReplacementTestScan(t, s, mapping, mode == "changes")
			if err != nil || stats.Written != 0 || stats.Unchanged != wantWritten {
				t.Fatalf("repeat stats=%+v err=%v", stats, err)
			}
		})
	}
}

func driveSTRMReplacementTestScan(t *testing.T, s *DriveSTRMService, value model.DriveSTRM, changes bool) (model.DriveSTRMStats, error) {
	t.Helper()
	ctx := context.Background()
	var err error
	if changes {
		err = s.NotifyDirectories(ctx, value.AccountID, []string{value.SourceCID})
	} else {
		err = s.Queue(ctx, value.ID, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	return driveSTRMTestAttempt(t, s)
}

func TestDriveSTRMSameOutputUsesLastProcessedVideo(t *testing.T) {
	for _, splitPages := range []bool{false, true} {
		t.Run(map[bool]string{false: "same page", true: "different pages"}[splitPages], func(t *testing.T) {
			s, db, client, mapping, root := driveSTRMTestSetup(t)
			client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("2", "movie.mp4")}
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			first := driveSTRMBulkVideo("1", "100", "movie.mkv", "movie.mkv")
			last := driveSTRMBulkVideo("2", "100", "movie.mp4", "movie.mp4")
			batches := [][]p115.DriveTreeEntry{{first, last}}
			if splitPages {
				batches = [][]p115.DriveTreeEntry{{first}, {last}}
			}
			s.Client = &driveSTRMBulkTestClient{driveSTRMTestClient: client, batches: batches}
			stats, err := driveSTRMReplacementTestScan(t, s, mapping, false)
			if err != nil || stats.Videos != 2 || stats.Failed != 0 {
				t.Fatalf("stats=%+v err=%v", stats, err)
			}
			output, err := os.ReadFile(filepath.Join(root, "strm", "movie.strm"))
			if err != nil || !strings.Contains(string(output), "/2/") || !strings.HasSuffix(string(output), "/movie.mp4\n") {
				t.Fatalf("output=%q err=%v", output, err)
			}
			owners, err := db.ListDriveSTRMFilesByPaths(context.Background(), mapping.ID, []string{"movie.strm"})
			if err != nil || len(owners) != 1 || owners[0].FileID != "2" || owners[0].Digest != driveSTRMDigest(output) {
				t.Fatalf("owners=%+v err=%v", owners, err)
			}
		})
	}
}

type driveSTRMInterruptedReplacementStore struct{ DriveSTRMStore }

func (s driveSTRMInterruptedReplacementStore) SaveDriveSTRMScanFiles(ctx context.Context, values []model.DriveSTRMFile) error {
	if len(values) == 1 && values[0].FileID == "2" {
		return errors.New("test interruption after file write")
	}
	return s.DriveSTRMStore.SaveDriveSTRMScanFiles(ctx, values)
}

func TestDriveSTRMRepairsIndexAfterInterruptedSave(t *testing.T) {
	ctx := context.Background()
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	client.files["100"][0] = driveSTRMTestVideo("2", "movie.mkv")
	s.Store = driveSTRMInterruptedReplacementStore{DriveSTRMStore: db}
	if stats, err := driveSTRMReplacementTestScan(t, s, mapping, false); err == nil || stats.Written != 1 {
		t.Fatalf("scan did not interrupt after writing: stats=%+v err=%v", stats, err)
	}
	s.Store = db
	stats, err := driveSTRMReplacementTestScan(t, s, mapping, false)
	if err != nil || stats.Written != 0 || stats.Unchanged != 1 {
		t.Fatalf("resumed stats=%+v err=%v", stats, err)
	}
	owners, err := db.ListDriveSTRMFilesByPaths(ctx, mapping.ID, []string{"movie.strm"})
	if err != nil || len(owners) != 1 || owners[0].FileID != "2" {
		t.Fatalf("resumed owners=%+v err=%v", owners, err)
	}
}
