package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/storage"
)

func newMediaSQLiteTest(t *testing.T) (*MediaOrganizer, *storage.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, organizeDefaultTemplates{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, dir
}

func mediaRecordFixture(id, status string, count int) MediaOrganizeRun {
	at := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	r := MediaOrganizeRun{ID: id, Status: status, Rule: MediaOrganizeRule{ID: "rule", Name: "测试规则", Storage: "local", Source: "/source"}, UpdatedAt: at, Groups: []MediaOrganizeGroup{{ID: "group", Title: "作品", Kind: "tv"}}}
	for i := range count {
		name := fmt.Sprintf("Work.S01E%02d.strm", i+1)
		r.Entries = append(r.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: name, Name: name}, MediaType: "video", SourcePath: name, Season: 1, Episode: i + 1}, Group: "group", Status: "done", Target: "/target/" + name, Result: &MediaOrganizeResult{Status: "done", At: at}})
	}
	return r
}

func saveMediaFixtures(t *testing.T, s *MediaOrganizer, runs ...MediaOrganizeRun) {
	t.Helper()
	s.doc.Runs = append(s.doc.Runs, runs...)
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	if err := s.persistLocked(ids...); err != nil {
		t.Fatal(err)
	}
}

func mediaHistoryTest(t *testing.T, s *MediaOrganizer, query, status string, page int) MediaOrganizeHistory {
	t.Helper()
	h, err := s.History(context.Background(), query, status, page)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestMediaOrganizerSQLiteMigrationAndNoResurrection(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old, err := NewSessionStore(filepath.Join(dir, "media-organize.enc"), "test")
	if err != nil {
		t.Fatal(err)
	}
	active, finished := mediaRecordFixture("active", "running", 1), mediaRecordFixture("finished", "completed", 1)
	active.Entries[0].Status, active.Entries[0].Result = "ready", nil
	raw, err := json.Marshal(struct {
		Rules []MediaOrganizeRule `json:"rules"`
		Runs  []MediaOrganizeRun  `json:"runs"`
		Seen  map[string]string   `json:"seen"`
	}{[]MediaOrganizeRule{finished.Rule}, []MediaOrganizeRun{active, finished}, map[string]string{"old": "obsolete"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	s, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.State().Rules) != 1 || mediaHistoryTest(t, s, "", "all", 1).Total != 2 {
		t.Fatal("legacy records were not imported")
	}
	if run, err := s.Snapshot(active.ID); err != nil || run.Status != "interrupted" {
		t.Fatal("active migration must interrupt execution", run, err)
	}
	if _, err := os.Stat(old.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("committed legacy snapshot was not removed", err)
	}
	deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: finished.ID, FileID: finished.Entries[0].File.ID}}})
	if err != nil || deleted != 1 {
		t.Fatal(deleted, err)
	}
	// Simulate an undeletable or restored stale snapshot after the first import.
	if err := old.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	s, err = OpenMediaOrganizerSQLite(context.Background(), db, dir, "different-key", nil, nil, nil, nil, nil, nil)
	if err != nil || mediaHistoryTest(t, s, "", "all", 1).Total != 1 {
		t.Fatal("initialized SQLite reread old snapshot or revived deleted rows", err)
	}
	if err := os.Mkdir(old.path, 0700); err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, old.path, "keep", "simulate nonempty obsolete path")
	if _, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal("leftover cleanup failure must not prevent startup", err)
	}
}

func TestMediaOrganizerSQLiteFailedMigrationRetainsSnapshot(t *testing.T) {
	dir := t.TempDir()
	old, err := NewSessionStore(filepath.Join(dir, "media-organize.enc"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Save(`{"rules":[],"runs":[]}`); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(old.path)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "state.db")
	db, err := storage.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	control, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if _, err := control.Exec(`CREATE TRIGGER reject_organize_import BEFORE INSERT ON media_organize_settings BEGIN SELECT RAISE(ABORT,'test import failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("database unexpectedly committed rejected import")
	}
	after, err := os.ReadFile(old.path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed import must preserve encrypted data", err)
	}
	loaded, err := db.LoadMediaOrganize(context.Background())
	if err != nil || loaded.Initialized {
		t.Fatal("failed import must not mark SQLite initialized", loaded, err)
	}
	if _, err := control.Exec(`DROP TRIGGER reject_organize_import`); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal("retry after failed import must succeed", err)
	}
}

func TestMediaOrganizerSQLiteSearchPaginationAndStatuses(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	done := mediaRecordFixture("done", "completed", 45)
	done.Entries[0].File.Name = "Épisode_literal_%_.strm"
	running := mediaRecordFixture("running", "running", 2)
	running.Entries[0].Status, running.Entries[0].Result = "ready", nil
	running.Entries[1].Status, running.Entries[1].Result = "invalid", nil
	partial := mediaRecordFixture("partial", "partial", 1)
	partial.Entries = append(partial.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "sub"}, MediaType: "subtitle"}, VideoID: partial.Entries[0].File.ID, Group: "group", Status: "error"})
	newer := mediaRecordFixture("newer", "completed", 1)
	newer.Entries[0].Result.At = newer.UpdatedAt.Add(100 * time.Millisecond)
	saveMediaFixtures(t, s, done, running, partial, newer)
	h := mediaHistoryTest(t, s, "", "all", 1)
	if h.Total != 49 || h.Pages != 3 || h.Items[0].RunID != "newer" || !reflect.DeepEqual(h.Counts, map[string]int{"all": 49, "done": 46, "running": 1, "attention": 2}) {
		t.Fatal("incorrect counts or subsecond order", h)
	}
	seen := map[string]bool{}
	for page := 1; page <= h.Pages; page++ {
		first := mediaHistoryTest(t, s, "", "all", page)
		again := mediaHistoryTest(t, s, "", "all", page)
		if !reflect.DeepEqual(first.Items, again.Items) {
			t.Fatal("same-time pagination is unstable")
		}
		for _, item := range first.Items {
			key := item.RunID + "/" + item.FileID
			if seen[key] {
				t.Fatal("overlapping pages", key)
			}
			seen[key] = true
		}
	}
	for _, q := range []string{"%", "_", "ÉPISODE"} {
		if h := mediaHistoryTest(t, s, q, "all", 1); h.Total != 1 {
			t.Fatal("search must be literal and case insensitive", q, h)
		}
	}
	if h := mediaHistoryTest(t, s, "作品", "done", 99); h.Total != 46 || h.Page != 3 || len(h.Items) != 6 || h.Counts["attention"] != 2 {
		t.Fatal("filter must precede pagination without narrowing counts", h)
	}
}

func TestMediaOrganizerSQLiteDeleteTaskAndActiveProtection(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	regular := mediaRecordFixture("regular", "completed", 2)
	regular.Rule.Source = t.TempDir()
	mediaTestFile(t, regular.Rule.Source, regular.Entries[0].File.ID, "untouched-source")
	fileDir := t.TempDir()
	mediaTestFile(t, fileDir, "kept.strm", "untouched-target")
	regular.Entries[0].Target = filepath.Join(fileDir, "kept.strm")
	regular.Groups = append(regular.Groups, MediaOrganizeGroup{ID: "other"})
	regular.Entries[1].Group = "other"
	regular.Entries = append(regular.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "sub"}, MediaType: "subtitle"}, VideoID: regular.Entries[0].File.ID, Group: "group", Status: "done"})
	active, action, partial := mediaRecordFixture("active", "completed", 1), mediaRecordFixture("action", "completed", 1), mediaRecordFixture("partial", "partial", 1)
	partial.Entries = append(partial.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "bad-sub"}, MediaType: "subtitle"}, VideoID: partial.Entries[0].File.ID, Status: "error"})
	saveMediaFixtures(t, s, regular, active, action, partial)
	s.active, s.actions[action.ID] = active.ID, "retry"
	if task, err := s.Task(active.ID, active.Entries[0].File.ID); err != nil || task.CanDelete {
		t.Fatal("completed checkpoint is still owned by worker", task, err)
	}
	items := []MediaOrganizeDeleteItem{{RunID: regular.ID, FileID: regular.Entries[0].File.ID}, {RunID: active.ID, FileID: active.Entries[0].File.ID}}
	if _, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: items}); !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("mixed request must reject atomically", err)
	}
	if h := mediaHistoryTest(t, s, "", "all", 1); h.Total != 5 {
		t.Fatal("busy rejection removed other records", h)
	}
	if deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: active.ID, FileID: "nonexistent"}}}); err != nil || deleted != 0 {
		t.Fatal("nonexistent task delete must be idempotent", deleted, err)
	}
	snapshot, _ := s.Task(regular.ID, regular.Entries[0].File.ID)
	if deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: items[:1]}); err != nil || deleted != 1 {
		t.Fatal(deleted, err)
	}
	remaining, _ := s.Snapshot(regular.ID)
	if len(remaining.Entries) != 1 || len(remaining.Groups) != 1 || remaining.Groups[0].ID != "other" || len(snapshot.Entries) != 2 {
		t.Fatal("task deletion leaked companions/groups or mutated snapshot", remaining, snapshot)
	}
	for filename, expected := range map[string]string{filepath.Join(regular.Rule.Source, regular.Entries[0].File.ID): "untouched-source", regular.Entries[0].Target: "untouched-target"} {
		data, err := os.ReadFile(filename)
		if err != nil || string(data) != expected {
			t.Fatal("deleting history changed a real file", filename, err)
		}
	}
	if deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Completed: true}); err != nil || deleted != 1 {
		t.Fatal("clear must skip partial and all runtime-owned tasks", deleted, err)
	}
	if _, err := s.Snapshot(regular.ID); !errors.Is(err, ErrMediaOrganizeInput) {
		t.Fatal("last child deletion retained empty parent", err)
	}
	reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, nil, nil, nil, nil, nil)
	if err != nil || mediaHistoryTest(t, reopened, "", "all", 1).Total != 3 {
		t.Fatal("deleted task was not persisted", err)
	}
}

func TestMediaOrganizerSQLiteGrayReturnToSourceAndRetention(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	metadata := &mediaRecognitionFake{items: []tmdb.Item{{ID: 42, Kind: "tv", Title: "作品", OriginalTitle: "Work", Date: "2024-01-01"}}}
	s.metadata = metadata
	rule, err := s.SaveRule(MediaOrganizeRule{Name: "灰度", Storage: "local", Source: t.TempDir(), Target: t.TempDir(), IntervalMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	const name, content = "Work.S01E01.strm", "http://example.invalid/unchanged\r\n"
	mediaTestFile(t, rule.Source, name, content)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	task, err := s.Task(run.ID, name)
	if err != nil || task.Status != "done" {
		t.Fatal(task, err)
	}
	if err := os.Rename(task.Target, filepath.Join(rule.Source, name)); err != nil {
		t.Fatal(err)
	}
	next, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &next, "auto")
	if h := mediaHistoryTest(t, s, "", "done", 1); h.Total != 2 || next.ID == run.ID {
		t.Fatal("returned source must be processed despite old history", h)
	}
	data, err := os.ReadFile(task.Target)
	if err != nil || string(data) != content {
		t.Fatal("STRM content changed", err)
	}
	// An unmatched source must also be eligible after correcting metadata.
	items := metadata.items
	metadata.items = nil
	mediaTestFile(t, rule.Source, "Work.S01E02.strm", content)
	failed, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &failed, "auto")
	if h := mediaHistoryTest(t, s, "", "attention", 1); h.Total != 1 {
		t.Fatal("unmatched source failure was not retained", h)
	}
	metadata.items = items
	corrected, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &corrected, "auto")
	if h := mediaHistoryTest(t, s, "", "done", 1); h.Total != 3 {
		t.Fatal("corrected source was skipped because of previous failure", h)
	}
	for i := range 55 {
		saveMediaFixtures(t, s, mediaRecordFixture(fmt.Sprintf("old-%02d", i), "completed", 1))
	}
	next, err = s.Start(rule.ID)
	if err != nil {
		t.Fatal("history must never block new scan", err)
	}
	mediaTestProcess(t, s, &next, "auto")
	if h := mediaHistoryTest(t, s, "", "all", 1); h.Total != 59 {
		t.Fatal("retention silently removed real tasks", h.Total)
	}
	t.Log("isolated SQLite/filesystem gray run: moved STRM returned to source and processed again; >50 real batches retained; no NAS/115 calls")
}

type canceledMediaStore struct {
	mediaOrganizeStorage
	failRestore bool
	restored    bool
}

func (s *canceledMediaStore) SaveMediaOrganize(ctx context.Context, _ string, _ []storage.MediaOrganizeRunRow, _ []string) error {
	return ctx.Err()
}

func (s *canceledMediaStore) LoadMediaOrganize(ctx context.Context) (storage.MediaOrganizeRows, error) {
	if err := ctx.Err(); err != nil {
		return storage.MediaOrganizeRows{}, err
	}
	s.restored = true
	if s.failRestore {
		return storage.MediaOrganizeRows{}, errors.New("disk unavailable")
	}
	return s.mediaOrganizeStorage.LoadMediaOrganize(ctx)
}

func TestMediaOrganizerSQLiteCanceledCommitRestoresWithFreshContext(t *testing.T) {
	for _, failRestore := range []bool{false, true} {
		t.Run(fmt.Sprint(failRestore), func(t *testing.T) {
			s, _, _ := newMediaSQLiteTest(t)
			store := &canceledMediaStore{mediaOrganizeStorage: s.db, failRestore: failRestore}
			s.db = store
			s.doc.Rules = []MediaOrganizeRule{{ID: "uncommitted"}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := s.persistSQLiteLockedContext(ctx, nil); !errors.Is(err, context.Canceled) || !store.restored {
				t.Fatal("cancelled commit failed to use an independent restore context", err)
			}
			if !failRestore && len(s.doc.Rules) != 0 {
				t.Fatal("uncommitted state remained visible")
			}
			if failRestore {
				if s.persistErr == nil {
					t.Fatal("unrecoverable storage must stop further execution")
				}
				if err := s.Run(context.Background()); err == nil {
					t.Fatal("worker accepted uncertain persistence state")
				}
			}
		})
	}
}
