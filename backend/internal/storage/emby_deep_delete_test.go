package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestEmbyDeepDeleteQueuePersistsAndNeverReplaysInterruptedWrites(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "delete.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	jobs := []model.EmbyDeepDeleteJob{
		{Key: "accepted", AccountID: "primary", UID: "123", FileID: "1", Revision: 2},
		{Key: "interrupted", FileID: "2"},
		{Key: "skipped", FileID: "3"},
		{Key: "pending", FileID: "4"},
	}
	count, err := s.QueueEmbyDeepDeletes(ctx, append(jobs, jobs[0]))
	if err != nil || count != 4 {
		t.Fatalf("queue count=%d err=%v", count, err)
	}
	for key, state := range map[string]string{"accepted": "accepted", "interrupted": "sending", "skipped": "skipped"} {
		if err := s.SetEmbyDeepDeleteState(ctx, []string{key}, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverEmbyDeepDeletes(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	count, err = s.QueueEmbyDeepDeletes(ctx, jobs)
	if err != nil || count != 0 {
		t.Fatalf("duplicates resurrected count=%d err=%v", count, err)
	}
	got, err := s.PendingEmbyDeepDeletes(ctx, 50)
	if err != nil || !reflect.DeepEqual(got, jobs[3:]) {
		t.Fatalf("pending=%+v err=%v", got, err)
	}
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM emby_deep_deletes WHERE key='interrupted'`).Scan(&state); err != nil || state != "uncertain" {
		t.Fatalf("interrupted state=%q err=%v", state, err)
	}
	// 不完整的一批不能提交前半段，否则 Webhook 重试会丢掉后半段任务。
	if _, err := s.QueueEmbyDeepDeletes(ctx, []model.EmbyDeepDeleteJob{{Key: "rollback"}, {}}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	got, err = s.PendingEmbyDeepDeletes(ctx, 50)
	if err != nil || !reflect.DeepEqual(got, jobs[3:]) {
		t.Fatalf("partial batch persisted: %+v err=%v", got, err)
	}
}

func TestEmbyDeepDeleteUpgradeAndExactSourceLookup(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "delete.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.db.ExecContext(ctx, `DROP TABLE emby_deep_deletes; DROP INDEX drive_strm_file_identity; PRAGMA user_version=67`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueEmbyDeepDeletes(ctx, []model.EmbyDeepDeleteJob{{Key: "upgraded"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second", "directory"} {
		if err := s.SaveDriveSTRM(ctx, model.DriveSTRM{ID: id, AccountID: "primary", SourceCID: "100", UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	files := []model.DriveSTRMFile{
		{MappingID: "first", FileID: "10", Path: "movie.strm", Digest: "first"},
		{MappingID: "second", FileID: "10", Path: "mirror/movie.strm", Digest: "second"},
		{MappingID: "first", FileID: "100", Path: "different.strm", Digest: "different"},
		{MappingID: "directory", FileID: "10", Path: "folder", Directory: true},
	}
	if err := s.SaveDriveSTRMFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	got, err := s.FindDriveSTRMDeleteCandidates(ctx, "10")
	if err != nil || len(got) != 2 {
		t.Fatalf("candidates=%+v err=%v", got, err)
	}
	seen := make(map[string]bool, len(got))
	for _, file := range got {
		if file.FileID != "10" || file.Directory || seen[file.MappingID] {
			t.Fatalf("imprecise candidates=%+v", got)
		}
		seen[file.MappingID] = true
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("source ambiguity lost: %+v", got)
	}
}
