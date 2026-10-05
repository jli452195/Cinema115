package service

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func transferTestPage(count int, ids ...string) *p115.SharePage {
	p := &p115.SharePage{Count: count}
	for _, id := range ids {
		p.Files = append(p.Files, p115.ShareFile{ID: id, Name: id + ".mkv"})
	}
	return p
}

func TestTransferDirectoryRejectsChangingPages(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second *p115.SharePage
		want          error
	}{
		{"overlap", transferTestPage(3, "1", "2"), transferTestPage(3, "2"), errTransferDirectoryChanging},
		{"growth", transferTestPage(3, "1", "2"), transferTestPage(4, "3", "4"), errTransferDirectoryChanging},
		{"shrink", transferTestPage(3, "1", "2"), transferTestPage(2), errTransferDirectoryChanging},
		{"empty page", transferTestPage(3, "1", "2"), transferTestPage(3), errTransferDirectoryChanging},
		{"overflow", transferTestPage(3, "1", "2"), transferTestPage(3, "3", "4"), errTransferDirectoryChanging},
		{"same page duplicate", transferTestPage(2, "1", "1"), nil, errTransferDirectoryChanging},
		{"invalid ID", transferTestPage(1, "bad"), nil, errTransferListingInvalid},
		{"zero ID", transferTestPage(1, "0"), nil, errTransferListingInvalid},
		{"missing page", nil, nil, errTransferListingInvalid},
		{"too many", transferTestPage(20001, "1"), nil, errTransferListingInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages, err := readTransferDirectory(context.Background(), "99", func(_ context.Context, _ string, offset int) (*p115.SharePage, error) {
				if offset == 0 {
					return tc.first, nil
				}
				return tc.second, nil
			})
			if !errors.Is(err, tc.want) || pages != nil {
				t.Fatalf("partial listing accepted: %v, %v", pages, err)
			}
		})
	}
}

func TestTransferScanRetriesOnlyChangingDirectory(t *testing.T) {
	w := &ShareWorkflow{PollInterval: time.Millisecond}
	calls := map[string]int{}
	var offsets []int
	list := w.transferPages(func(_ context.Context, cid string, offset int) (*p115.SharePage, error) {
		calls[cid]++
		switch cid {
		case "99":
			return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "10", Directory: true}, {ID: "20", Directory: true}}}, nil
		case "10":
			return transferTestPage(1, "11"), nil
		default:
			offsets = append(offsets, offset)
			if offset == 0 {
				return transferTestPage(2, "21"), nil
			}
			if calls[cid] == 2 {
				return transferTestPage(2, "21"), nil
			}
			return transferTestPage(2, "22"), nil
		}
	})
	entries, roots, files, err := w.scanDirectory(context.Background(), "99", "test", list)
	if err != nil || len(entries) != 5 || len(roots) != 2 || len(files) != 3 {
		t.Fatalf("scan: %v %v %v %v", entries, roots, files, err)
	}
	if calls["99"] != 1 || calls["10"] != 1 || !reflect.DeepEqual(offsets, []int{0, 1, 0, 1}) {
		t.Fatal("reread stable sibling or skipped retry", calls, offsets)
	}
	if files[1].ID != "21" || files[2].ID != "22" {
		t.Fatal("retained partial attempt", files)
	}
}

func TestTransferStableScanHasNoExtraRequestsOrWait(t *testing.T) {
	w := &ShareWorkflow{PollInterval: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var offsets []int
	list := w.transferPages(func(_ context.Context, _ string, offset int) (*p115.SharePage, error) {
		offsets = append(offsets, offset)
		if offset == 0 {
			return transferTestPage(3, "1", "2"), nil
		}
		return transferTestPage(3, "3"), nil
	})
	_, _, files, err := w.scanDirectory(ctx, "99", "test", list)
	if err != nil || len(files) != 3 || !reflect.DeepEqual(offsets, []int{0, 2}) {
		t.Fatal(err, offsets, files)
	}
}

func TestTransferChangingDirectoryRetryBoundAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		w := &ShareWorkflow{PollInterval: time.Millisecond}
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		list := w.transferPages(func(context.Context, string, int) (*p115.SharePage, error) {
			calls++
			if cancelled {
				cancel()
			}
			return transferTestPage(2, "1", "1"), nil
		})
		_, err := list(ctx, "99", 0)
		cancel()
		if !errors.Is(err, errTransferDirectoryChanging) {
			t.Fatal(err)
		}
		if cancelled {
			if calls != 1 || !errors.Is(err, context.Canceled) {
				t.Fatal(calls, err)
			}
		} else if calls != 3 {
			t.Fatal("unbounded retries", calls)
		}
	}
}

type changingTransferFake struct {
	*transferFake
	reads   int
	invalid bool
}

func (f *changingTransferFake) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	if cid != "20" {
		return f.transferFake.DriveFiles(ctx, cid, offset, cookie)
	}
	f.reads++
	if f.invalid {
		return transferTestPage(1, "bad"), nil
	}
	if offset == 0 || f.reads == 2 {
		return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "21", Name: "第一集.mkv", Size: 100}}}, nil
	}
	return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "22", Name: "ignored.txt", Size: 10}}}, nil
}

func TestTransferWorkflowDoesNotReplayReceiveOrSharePartialScan(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		base := &transferFake{}
		w, input := setupWorkflow(t, base)
		fake := &changingTransferFake{transferFake: base, invalid: invalid}
		w.Client = fake
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := w.Generate(ctx, input)
		cancel()
		calls := strings.Join(base.calls, ",")
		if invalid {
			if !errors.Is(err, errTransferListingInvalid) || calls != "create,receive" || base.deleted {
				t.Fatal(err, calls)
			}
			if result == nil || result.TempDirectoryID != "999" {
				t.Fatal("lost checkpoint", result)
			}
		} else if err != nil || fake.reads != 4 || calls != "create,receive,share,permanent,generate,delete" {
			t.Fatal(err, fake.reads, calls)
		}
		jobs := workflowJobs(t, w)
		if len(jobs) != 1 || jobs[0].Stats == nil {
			t.Fatal("missing final metrics", jobs)
		}
		stats := jobs[0].Stats
		if stats.SourceFiles != 1 || stats.ScanRounds != 1 || stats.Directories["transfer/20"].Requests != fake.reads {
			t.Fatal("incorrect persisted requests", stats)
		}
		if _, ok := stats.StageMS["verifying_transfer"]; !ok {
			t.Fatal("missing transfer duration")
		}
		if !invalid {
			if stats.Directories["transfer/20"].Retries != 1 || stats.Directories["transfer/20"].FullReads != 1 {
				t.Fatal("missing retries", stats)
			}
			for _, stage := range []string{"source", "receiving", "sharing", "waiting_snapshot", "generating", "deleting"} {
				if _, ok := stats.StageMS[stage]; !ok {
					t.Fatal("missing stage", stage)
				}
			}
		}
	}
}

func TestTransferLargeWaitBudgetAndParentCancellation(t *testing.T) {
	if transferWaitBudget(1000) != 90*time.Second || transferWaitBudget(4848) != 10*time.Minute {
		t.Fatal("incorrect size budget")
	}
	w := &ShareWorkflow{PollInterval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := w.untilBudget(ctx, transferWaitBudget(4848), func(ctx context.Context) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 9*time.Minute {
			t.Error("large task retained 90s budget")
		}
		cancel()
		return errTransferDirectoryChanging
	})
	if calls != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, errTransferDirectoryChanging) {
		t.Fatal(calls, err)
	}
}

func TestGrowingLargeDirectoryProbesBeforeReadingRemainingPages(t *testing.T) {
	ctx := withReshareMetrics(context.Background())
	w := &ShareWorkflow{PollInterval: time.Millisecond}
	var offsets []int
	counts := []int{300, 320, 330, 340, 340, 340, 340, 340}
	read := measuredPages(ctx, "transfer", func(_ context.Context, _ string, offset int) (*p115.SharePage, error) {
		if len(offsets) >= len(counts) {
			t.Fatal("unexpected extra request", offset)
		}
		count := counts[len(offsets)]
		offsets = append(offsets, offset)
		p := &p115.SharePage{Count: count, Offset: offset}
		for i := offset; i < min(offset+100, count); i++ {
			p.Files = append(p.Files, p115.ShareFile{ID: strconv.Itoa(i + 1), Name: "video.mkv"})
		}
		return p, nil
	})
	verified := make(map[string][]p115.ShareFile)
	_, _, files, err := w.scanDirectory(ctx, "99", "test", w.transferPages(read, verified))
	if err != nil || len(files) != 340 || len(verified["99"]) != 340 {
		t.Fatal(err, len(files))
	}
	if !reflect.DeepEqual(offsets, []int{0, 100, 0, 0, 0, 100, 200, 300}) {
		t.Fatal("probe not reused or full read during growth", offsets)
	}
	d := directoryMetrics(ctx, "transfer", "99")
	if d.Requests != 8 || d.Probes != 3 || d.Retries != 1 || d.FullReads != 1 || d.FirstPages != 4 {
		t.Fatalf("wrong metrics: %+v", d)
	}
}

func TestProbePlateauStillRequiresCompleteUniqueListing(t *testing.T) {
	w := &ShareWorkflow{PollInterval: time.Millisecond}
	ctx, cancel := context.WithTimeout(withReshareMetrics(context.Background()), time.Second)
	defer cancel()
	read := measuredPages(ctx, "transfer", func(_ context.Context, _ string, offset int) (*p115.SharePage, error) {
		p := transferTestPage(101, "1")
		if offset == 0 {
			for i := 2; i <= 100; i++ {
				p.Files = append(p.Files, p115.ShareFile{ID: strconv.Itoa(i)})
			}
		}
		return p, nil
	})
	_, _, files, err := w.scanDirectory(ctx, "99", "test", w.transferPages(read))
	if !errors.Is(err, errTransferDirectoryChanging) || files != nil {
		t.Fatal("plateau accepted duplicate listing", err)
	}
	if directoryMetrics(ctx, "transfer", "99").Probes != 2 {
		t.Fatal("probe retry limit not applied")
	}
}

func TestProbeCancellationAndBound(t *testing.T) {
	w := &ShareWorkflow{PollInterval: time.Millisecond}
	ctx := withReshareMetrics(context.Background())
	calls := 0
	p, err := w.probeTransferDirectory(ctx, "99", 100, func(context.Context, string, int) (*p115.SharePage, error) {
		calls++
		return transferTestPage(100+calls, "1"), nil
	})
	if err != nil || calls != 4 || p.Count != 104 {
		t.Fatal(calls, p, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	calls = 0
	_, err = w.probeTransferDirectory(cancelCtx, "99", 100, func(context.Context, string, int) (*p115.SharePage, error) {
		calls++
		cancel()
		return transferTestPage(101, "1"), nil
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(calls, err)
	}
}
