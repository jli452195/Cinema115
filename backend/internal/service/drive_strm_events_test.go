package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type driveSTRMLifeFake func(context.Context, int, int, string) (p115.LifeEventPage, error)

func (f driveSTRMLifeFake) LifeEvents(ctx context.Context, offset, limit int, cookie string) (p115.LifeEventPage, error) {
	return f(ctx, offset, limit, cookie)
}

func driveSTRMEventFixture(t *testing.T) (*DriveSTRMService, *storage.Store) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "drive-events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	accounts := &P115Accounts{value: accountDocument{Accounts: []P115Account{{ID: "main", Primary: true, Cookie: "UID=42_A", Revision: 1}}}}
	s := NewDriveSTRMService(db, accounts, nil, t.TempDir())
	mapping := model.DriveSTRM{ID: "mapping", AccountID: "main", SourceCID: "10", Enabled: true, UpdatedAt: time.Now()}
	if err := db.SaveDriveSTRM(context.Background(), mapping); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveDriveSTRMFile(context.Background(), model.DriveSTRMFile{MappingID: mapping.ID, FileID: "20", ParentCID: "10", Path: "Season 01", Directory: true}); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestDriveSTRMEventsBaselineThenCoalesceOnlyKnownScopes(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	var events []p115.LifeEvent
	client := driveSTRMLifeFake(func(_ context.Context, offset, limit int, cookie string) (p115.LifeEventPage, error) {
		if cookie != "UID=42_A" || offset != 0 {
			t.Fatal("reader changed credentials or unexpectedly paged")
		}
		if len(events) == 1 && limit != 1 {
			t.Fatal("initial baseline should read only the head")
		}
		return p115.LifeEventPage{Count: len(events), Events: events}, nil
	})
	r := NewDriveSTRMEvents(s, client)
	events = []p115.LifeEvent{{ID: "100", Type: 8}}
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := db.GetDriveSTRM(ctx, "mapping")
	if !first.Queued || !reflect.DeepEqual(first.PendingDirectories, []string{"10"}) {
		t.Fatalf("initial scan not durable before baseline: %+v", first)
	}
	first.Queued, first.PendingDirectories = false, nil
	if err := db.SaveDriveSTRM(ctx, first); err != nil {
		t.Fatal(err)
	}
	events = []p115.LifeEvent{
		{ID: "104", Type: 2, ParentID: "20", FileID: "30"},
		{ID: "103", Type: 2, ParentID: "20", FileID: "31"},
		{ID: "102", Type: 2, ParentID: "999", FileID: "32"},
		{ID: "101", Type: 8, ParentID: "10", FileID: "33"},
		{ID: "100", Type: 8},
	}
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	updated, _ := db.GetDriveSTRM(ctx, "mapping")
	cursor, _ := db.LoadDriveSTRMCursor(ctx, "main")
	if cursor != "104" || !reflect.DeepEqual(updated.PendingDirectories, []string{"20"}) {
		t.Fatalf("unrelated scope triggered scan or cursor lost: cursor=%s pending=%v", cursor, updated.PendingDirectories)
	}
	if r.State().LastCheckedAt.IsZero() || r.State().LastError != "" {
		t.Fatal("reader status unavailable")
	}
}

type driveSTRMFailQueueStore struct {
	DriveSTRMStore
	fail bool
}

func (s *driveSTRMFailQueueStore) SaveDriveSTRM(ctx context.Context, value model.DriveSTRM) error {
	if s.fail {
		return errors.New("queue disk failure")
	}
	return s.DriveSTRMStore.SaveDriveSTRM(ctx, value)
}

func TestDriveSTRMEventsDoNotAdvancePastUncommittedQueue(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	s.Store = &driveSTRMFailQueueStore{DriveSTRMStore: db, fail: true}
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		return p115.LifeEventPage{Count: 1, Events: []p115.LifeEvent{{ID: "100", Type: 2, ParentID: "10"}}}, nil
	}))
	if err := r.Check(context.Background()); err == nil {
		t.Fatal("queue failure hidden")
	}
	if cursor, _ := db.LoadDriveSTRMCursor(context.Background(), "main"); cursor != "" {
		t.Fatal("cursor advanced without durable queue")
	}
	s.Store.(*driveSTRMFailQueueStore).fail = false
	if err := r.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := db.LoadDriveSTRMCursor(context.Background(), "main"); cursor != "100" {
		t.Fatal("failed event not replayed")
	}
}

func TestDriveSTRMEventsCanceledWaitDoesNotStartConcurrentRead(t *testing.T) {
	s, _ := driveSTRMEventFixture(t)
	started := make(chan struct{})
	var calls atomic.Int32
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(ctx context.Context, _, _ int, _ string) (p115.LifeEventPage, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-ctx.Done()
		return p115.LifeEventPage{}, ctx.Err()
	}))
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	finished := make(chan error, 1)
	go func() { finished <- r.Check(firstCtx) }()
	<-started
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Check(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatal("queued manual check ignored cancellation", err)
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent upstream reads escaped gate")
	}
	firstCancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDriveSTRMEventsDisabledMappingsMakeNoUpstreamRequests(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	value, _ := db.GetDriveSTRM(ctx, "mapping")
	value.Enabled = false
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		t.Error("disabled mappings should make no history requests")
		return p115.LifeEventPage{}, nil
	}))
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDriveSTRMEventsPersistHeadAfterAllHistoryPagesAreQueued(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.SaveDriveSTRMCursor(ctx, "main", "1"); err != nil {
		t.Fatal(err)
	}
	pages := make([]int, 0, 2)
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(_ context.Context, offset, limit int, _ string) (p115.LifeEventPage, error) {
		pages = append(pages, offset)
		out := p115.LifeEventPage{Count: 1001}
		if offset == 0 {
			for id := 1001; id >= 938; id-- {
				out.Events = append(out.Events, p115.LifeEvent{ID: strconv.Itoa(id), Type: 8})
			}
		} else if offset == 64 {
			for id := 937; id >= 1; id-- {
				out.Events = append(out.Events, p115.LifeEvent{ID: strconv.Itoa(id), Type: 8})
			}
		} else {
			t.Fatal("invalid event pagination")
		}
		return out, nil
	}))
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	cursor, _ := db.LoadDriveSTRMCursor(ctx, "main")
	value, _ := db.GetDriveSTRM(ctx, "mapping")
	if cursor != "1001" || !reflect.DeepEqual(pages, []int{0, 64}) || value.Queued {
		t.Fatalf("history pagination or no-change handling failed: cursor=%s pages=%v mapping=%+v", cursor, pages, value)
	}
}

func TestDriveSTRMEventsSnapshotDoesNotSkipArrivalsBetweenPages(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.SaveDriveSTRMCursor(ctx, "main", "1"); err != nil {
		t.Fatal(err)
	}
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(_ context.Context, offset, _ int, _ string) (p115.LifeEventPage, error) {
		if offset == 0 {
			return p115.LifeEventPage{Count: 3, Events: []p115.LifeEvent{{ID: "4", Type: 8}}}, nil
		}
		// The history changed while paging. Event 5 was unseen at the head;
		// event 6 appearing on a later page must not advance past that gap.
		return p115.LifeEventPage{Count: 3, Events: []p115.LifeEvent{{ID: "6", Type: 8}, {ID: "1", Type: 8}}}, nil
	}))
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := db.LoadDriveSTRMCursor(ctx, "main"); cursor != "4" {
		t.Fatalf("new arrivals skipped by moving snapshot watermark: %s", cursor)
	}
}

func TestDriveSTRMEventsTruncatedHistoryQueuesFullRepair(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.SaveDriveSTRMCursor(ctx, "main", "1"); err != nil {
		t.Fatal(err)
	}
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(_ context.Context, offset, limit int, _ string) (p115.LifeEventPage, error) {
		out := p115.LifeEventPage{Count: 10000, Events: make([]p115.LifeEvent, 0, limit)}
		for i := 0; i < limit; i++ {
			out.Events = append(out.Events, p115.LifeEvent{ID: strconv.Itoa(20000 - offset - i), Type: 8})
		}
		return out, nil
	}))
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	value, _ := db.GetDriveSTRM(ctx, "mapping")
	if !value.Queued || !reflect.DeepEqual(value.PendingDirectories, []string{"10"}) {
		t.Fatalf("truncated history did not leave durable full repair: %+v", value)
	}
}

func TestDriveSTRMEventsClearedHistoryRepairsOnce(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.SaveDriveSTRMCursor(ctx, "main", "100"); err != nil {
		t.Fatal(err)
	}
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		return p115.LifeEventPage{Count: 0, Events: []p115.LifeEvent{}}, nil
	}))
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	value, _ := db.GetDriveSTRM(ctx, "mapping")
	cursor, _ := db.LoadDriveSTRMCursor(ctx, "main")
	if !value.Queued || cursor != "0" {
		t.Fatalf("cleared history not repaired/reset: %+v cursor=%s", value, cursor)
	}
	value.Queued, value.PendingDirectories = false, nil
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	value, _ = db.GetDriveSTRM(ctx, "mapping")
	if value.Queued {
		t.Fatal("empty retained history repeatedly scans library")
	}
}
