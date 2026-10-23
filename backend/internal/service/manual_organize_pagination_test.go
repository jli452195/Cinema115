package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type organizePaginationDrive struct {
	OrganizeDrive
	read func(context.Context, string, int, string) (*p115.SharePage, error)
}

func (d organizePaginationDrive) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	return d.read(ctx, cid, offset, cookie)
}

func TestManualOrganizeRestartsUnstablePagination(t *testing.T) {
	for _, mode := range []string{"count-changed", "overflow", "offset-reset", "duplicate", "empty", "nil"} {
		t.Run(mode, func(t *testing.T) {
			s, drive, _, _ := newOrganizeTest(t)
			offsets := make([]int, 0, 4)
			first := true
			s.drive = organizePaginationDrive{OrganizeDrive: drive, read: func(_ context.Context, _ string, offset int, _ string) (*p115.SharePage, error) {
				offsets = append(offsets, offset)
				page := &p115.SharePage{Count: 2, Offset: offset, Files: []p115.ShareFile{{ID: "1"}}}
				if offset == 1 {
					page.Files[0].ID = "2"
				}
				if first && offset == 1 {
					first = false
					switch mode {
					case "count-changed":
						page.Count = 3
					case "overflow":
						page.Files = append(page.Files, p115.ShareFile{ID: "3"})
					case "offset-reset":
						page.Offset = 0
					case "duplicate":
						page.Files[0].ID = "1"
					case "empty":
						page.Files = nil
					case "nil":
						return nil, nil
					}
				}
				return page, nil
			}}
			files, err := s.list(context.Background(), "30", "cookie")
			if err != nil || len(files) != 2 || files[0].ID != "1" || files[1].ID != "2" || !reflect.DeepEqual(offsets, []int{0, 1, 0, 1}) {
				t.Fatalf("inconsistent pages reused: files=%v offsets=%v err=%v", files, offsets, err)
			}
		})
	}
}

func TestManualOrganizePaginationRetriesAreBoundedAndCancelable(t *testing.T) {
	s, drive, _, _ := newOrganizeTest(t)
	calls := 0
	s.drive = organizePaginationDrive{OrganizeDrive: drive, read: func(context.Context, string, int, string) (*p115.SharePage, error) {
		calls++
		return &p115.SharePage{Count: 1}, nil
	}}
	if files, err := s.list(context.Background(), "30", "cookie"); files != nil || !errors.Is(err, errOrganizePagination) || calls != 3 {
		t.Fatalf("unbounded or incomplete listing: calls=%d files=%v err=%v", calls, files, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls = 0
	s.drive = organizePaginationDrive{OrganizeDrive: drive, read: func(context.Context, string, int, string) (*p115.SharePage, error) {
		calls++
		cancel()
		return nil, nil
	}}
	started := time.Now()
	if _, err := s.list(ctx, "30", "cookie"); !errors.Is(err, context.Canceled) || calls != 1 || time.Since(started) > 500*time.Millisecond {
		t.Fatal("canceled retry did not stop promptly", calls, err)
	}
	calls = 0
	failure := errors.New("directory identity changed")
	s.drive = organizePaginationDrive{OrganizeDrive: drive, read: func(context.Context, string, int, string) (*p115.SharePage, error) { calls++; return nil, failure }}
	if _, err := s.list(context.Background(), "30", "cookie"); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("unrelated failure retried", calls, err)
	}
}

func TestManualOrganize150FilesRecoverAfterMoveWithoutReplayingWrites(t *testing.T) {
	s, drive, id, plan := organizeBatchFixture(t, 150)
	drive.pageSize = 100
	reads := 0
	s.drive = organizePaginationDrive{OrganizeDrive: drive, read: func(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
		page, err := drive.DriveFiles(ctx, cid, offset, cookie)
		if err == nil && cid == "30" && drive.moves == 1 {
			reads++
			if reads <= 2 {
				page.Count = 150
				if offset == 100 {
					page.Offset = 0
					page.Files = append([]p115.ShareFile(nil), drive.dirs[cid]...)
				}
			}
		}
		return page, err
	}}
	job := organizeTestRun(t, s, id, plan)
	if job.Status != "completed" || drive.moves != 3 || drive.renames != 3 || len(drive.dirs["50"]) != 150 || len(drive.dirs["30"]) != 0 {
		t.Fatalf("150-file recovery failed: status=%s moves=%d renames=%d source=%d target=%d", job.Status, drive.moves, drive.renames, len(drive.dirs["30"]), len(drive.dirs["50"]))
	}
	seen := make(map[string]bool, 150)
	for _, batch := range drive.moveBatches {
		for _, id := range batch {
			if seen[id] {
				t.Fatal("move replayed", id)
			}
			seen[id] = true
		}
	}
}

func TestManualOrganizeWaitsForBatchResultsWithoutReplayingWrites(t *testing.T) {
	for _, phase := range []string{"rename", "move"} {
		t.Run(phase, func(t *testing.T) {
			s, drive, id, plan := organizeBatchFixture(t, 2)
			stale := false
			s.drive = organizePaginationDrive{OrganizeDrive: drive, read: func(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
				page, err := drive.DriveFiles(ctx, cid, offset, cookie)
				if err != nil {
					return page, err
				}
				if !stale && (phase == "rename" && drive.renames == 1 && drive.moves == 0 && cid == "30" || phase == "move" && drive.moves == 1 && cid == "50") {
					stale = true
					page.Count, page.Files = 1, page.Files[:1]
				}
				return page, nil
			}}
			job := organizeTestRun(t, s, id, plan)
			if !stale || job.Status != "completed" || drive.renames != 1 || drive.moves != 1 {
				t.Fatal("stale write result caused failure or write replay", stale, job.Status, drive.renames, drive.moves)
			}
		})
	}
}
