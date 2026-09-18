package storage

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestFolderMonitorCheckpointRoundTrip(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	value := model.FolderMonitor{
		ID: "monitor-1", Name: "下载收件箱", AccountID: "account-1", SourceCID: "123",
		SourcePath: "/待处理", OutputDir: "strm/inbox", BaseURL: "http://nas:3002",
		VideoExtensions: "mkv,mp4", MetadataExtensions: "srt", Recursive: true,
		Enabled: true, IntervalSeconds: 300, Stage: "waiting", StableScans: 1,
		BatchIDs: []string{"11", "12"}, BatchFingerprint: "fingerprint", UpdatedAt: time.Now().UTC(),
	}
	if err := store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetFolderMonitor(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != value.ID || got.AccountID != value.AccountID || got.Stage != value.Stage || len(got.BatchIDs) != 2 || got.BatchFingerprint != value.BatchFingerprint {
		t.Fatalf("got=%+v", got)
	}
	list, err := store.ListFolderMonitors(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if err := store.DeleteFolderMonitor(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFolderMonitor(ctx, value.ID); err != ErrNotFound {
		t.Fatalf("missing error=%v", err)
	}
}

func TestFolderMonitorRejectsUnsafeCheckpoint(t *testing.T) {
	store, _ := testStore(t)
	value := model.FolderMonitor{ID: "x", Name: "x", AccountID: "a", SourceCID: "1", Stage: "waiting", IntervalSeconds: 1, UpdatedAt: time.Now().UTC()}
	if err := store.SaveFolderMonitor(context.Background(), value); err == nil {
		t.Fatal("accepted interval below safety limit")
	}
}
