package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type driveSTRMTestClient struct {
	mu            sync.Mutex
	files         map[string][]p115.ShareFile
	paths         map[string]string
	metadata      map[string][]byte
	metadataErr   map[string]error
	calls         []string
	metadataCalls int
	onList        func(context.Context)
	pageErr       error
	onIdentity    func(context.Context)
	onMetadata    func()
	changedCount  bool
}

func (f *driveSTRMTestClient) ReadDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "identity:"+cid+":"+cookie)
	p, ok := f.paths[cid]
	hook := f.onIdentity
	f.onIdentity = nil
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if !ok {
		return p115.DriveDirectory{}, fmt.Errorf("missing directory")
	}
	return p115.DriveDirectory{CID: cid, Path: p}, nil
}

func (f *driveSTRMTestClient) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "list:"+cid+":"+cookie)
	hook := f.onList
	f.onList = nil
	data := f.files[cid]
	err := f.pageErr
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if err != nil {
		return nil, err
	}
	if offset > len(data) {
		return nil, fmt.Errorf("invalid offset")
	}
	count := len(data)
	if f.changedCount && offset > 0 {
		count--
	}
	return &p115.SharePage{Files: append([]p115.ShareFile(nil), data[offset:min(offset+2, len(data))]...), Count: count, Offset: offset}, nil
}

func (f *driveSTRMTestClient) ReadDriveMetadata(_ context.Context, file p115.ShareFile, cookie string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadataCalls++
	if f.onMetadata != nil {
		f.onMetadata()
	}
	f.calls = append(f.calls, "metadata:"+file.ID+":"+cookie)
	return append([]byte(nil), f.metadata[file.ID]...), f.metadataErr[file.ID]
}

func driveSTRMTestVideo(id, name string) p115.ShareFile {
	return p115.ShareFile{ID: id, Name: name, Size: 100, PickCode: "ecjq9ichcb40lzlvx", SHA1: strings.Repeat("A", 40)}
}

func driveSTRMTestSetup(t *testing.T) (*DriveSTRMService, *storage.Store, *driveSTRMTestClient, model.DriveSTRM, string) {
	t.Helper()
	root := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	secret, err := NewSessionStore(filepath.Join(root, "accounts.enc"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(secret, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("primary")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := accounts.Get(id)
	cookie := "UID=42_A"
	if err := accounts.Update(id, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	client := &driveSTRMTestClient{files: map[string][]p115.ShareFile{}, paths: map[string]string{"100": "/library", "200": "/library/Season 01", "300": "/other"}, metadata: map[string][]byte{}, metadataErr: map[string]error{}}
	s := NewDriveSTRMService(db, accounts, client, root)
	value, err := s.Save(context.Background(), model.DriveSTRM{Name: "library", AccountID: id, SourceCID: "100", OutputDir: "strm", BaseURL: "https://cinema.example", VideoExtensions: "mkv,mp4", MetadataExtensions: "ass,nfo,jpg", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, client, value, root
}

func driveSTRMTestAttempt(t *testing.T, s *DriveSTRMService) (model.DriveSTRMStats, error) {
	t.Helper()
	value, ctx, found, err := s.next(context.Background())
	if err != nil || !found {
		t.Fatalf("next: found=%v err=%v", found, err)
	}
	stats, scanErr := s.scan(ctx, value)
	if err := s.finish(context.Background(), value.ID, stats, scanErr); err != nil {
		t.Fatal(err)
	}
	return stats, scanErr
}

func TestDriveSTRMScanPersistsOwnershipAndRepairsMissingOutputs(t *testing.T) {
	s, db, client, value, root := driveSTRMTestSetup(t)
	ctx := context.Background()
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv"), {ID: "200", Name: "Season 01", Directory: true}, {ID: "10", Name: "poster.jpg", Size: 5, SHA1: strings.Repeat("B", 40)}, {ID: "11", Name: "ignored.zip", Size: 3}}
	client.files["200"] = []p115.ShareFile{driveSTRMTestVideo("2", "Episode 01.mkv"), driveSTRMTestVideo("3", "Episode 02.mp4"), {ID: "12", Name: "Episode 01.ass", Size: 3, SHA1: strings.Repeat("C", 40)}}
	client.metadata["10"], client.metadata["12"] = []byte("image"), []byte("sub")
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Videos != 3 || stats.Written != 3 || stats.MetadataWritten != 2 || stats.Directories != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	filename := filepath.Join(root, "strm", "Season 01", "Episode 01.strm")
	data, err := os.ReadFile(filename)
	if err != nil || !strings.Contains(string(data), "/api/drive115/ecjq9ichcb40lzlvx/2/") || strings.Contains(string(data), "UID=") {
		t.Fatalf("data=%q err=%v", data, err)
	}
	info, _ := os.Stat(filename)
	if err := s.Queue(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	stats, err = driveSTRMTestAttempt(t, s)
	unchangedInfo, _ := os.Stat(filename)
	if err != nil || stats.Written != 0 || stats.Unchanged != 5 || client.metadataCalls != 2 || !info.ModTime().Equal(unchangedInfo.ModTime()) {
		t.Fatalf("repeat stats=%+v downloads=%d err=%v", stats, client.metadataCalls, err)
	}
	// Recreate the service as a restart; no in-memory ownership is required.
	s = NewDriveSTRMService(db, s.Accounts, client, root)
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	stats, err = driveSTRMTestAttempt(t, s)
	if err != nil || stats.Written != 1 || stats.Unchanged != 4 {
		t.Fatalf("repair stats=%+v err=%v", stats, err)
	}
	// Update managed bytes after a service-address change, but preserve a hand edit.
	value.BaseURL = "https://new.example"
	if _, err := s.Save(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("manual output\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err = driveSTRMTestAttempt(t, s)
	if err == nil || stats.Failed != 1 || stats.Written != 2 {
		t.Fatalf("edit stats=%+v err=%v", stats, err)
	}
	data, _ = os.ReadFile(filename)
	if string(data) != "manual output\n" {
		t.Fatal("hand-edited file overwritten")
	}
	if err := s.Delete(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filename); err != nil {
		t.Fatal("deleting mapping deleted local output")
	}
	indexed, err := db.ListDriveSTRMFiles(ctx, value.ID)
	if err != nil || len(indexed) != 0 {
		t.Fatalf("orphan index=%+v err=%v", indexed, err)
	}
}

func TestDriveSTRMMetadataFailureDoesNotBlockVideosOrRefetchUnchangedAttachments(t *testing.T) {
	s, _, client, value, _ := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{{ID: "10", Name: "poster.jpg", Size: 5, SHA1: strings.Repeat("B", 40)}, driveSTRMTestVideo("1", "movie.mkv"), {ID: "12", Name: "movie.ass", Size: 3, SHA1: strings.Repeat("C", 40)}}
	client.metadataErr["10"] = errors.New("download failed")
	client.metadata["12"] = []byte("sub")
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Written != 1 || stats.MetadataWritten != 1 || stats.Failed != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	client.metadataErr["10"], client.metadata["10"] = nil, []byte("image")
	if err := s.Queue(context.Background(), value.ID, true); err != nil {
		t.Fatal(err)
	}
	stats, err = driveSTRMTestAttempt(t, s)
	if err != nil || stats.Written != 0 || stats.MetadataWritten != 1 || stats.Unchanged != 2 || client.metadataCalls != 3 {
		t.Fatalf("retry stats=%+v downloads=%d err=%v", stats, client.metadataCalls, err)
	}
}

func TestDriveSTRMMappingRootsResetOwnershipAndRejectOverlap(t *testing.T) {
	s, db, client, value, root := driveSTRMTestSetup(t)
	ctx := context.Background()
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{".", "strm", "strm/child"} {
		copy := value
		copy.ID = ""
		copy.OutputDir = output
		if _, err := s.Save(ctx, copy); !errors.Is(err, ErrGenerationInput) {
			t.Fatalf("overlap %q: %v", output, err)
		}
	}
	value.SourceCID = "300"
	value, err := s.Save(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	index, err := db.ListDriveSTRMFiles(ctx, value.ID)
	if err != nil || len(index) != 0 {
		t.Fatalf("index=%+v err=%v", index, err)
	}
	client.files["300"] = []p115.ShareFile{driveSTRMTestVideo("5", "movie.mkv")}
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Failed != 1 {
		t.Fatalf("foreign output stats=%+v err=%v", stats, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "strm", "movie.strm"))
	if !strings.Contains(string(data), "/1/") {
		t.Fatalf("old root output overwritten: %q", data)
	}
}

func TestDriveSTRMDirectoryEventsAreScopedAndCoalesced(t *testing.T) {
	s, db, client, value, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	client.files["100"] = []p115.ShareFile{{ID: "200", Name: "Season 01", Directory: true}, driveSTRMTestVideo("1", "movie.mkv")}
	client.files["200"] = []p115.ShareFile{driveSTRMTestVideo("2", "E01.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	if err := s.NotifyDirectories(ctx, value.AccountID, []string{"200", "200", "999"}); err != nil {
		t.Fatal(err)
	}
	saved, _ := db.GetDriveSTRM(ctx, value.ID)
	if !saved.Queued || len(saved.PendingDirectories) != 1 || saved.FullScan {
		t.Fatalf("queue=%+v", saved)
	}
	client.calls = nil
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	for _, call := range client.calls {
		if strings.HasPrefix(call, "list:100:") {
			t.Fatalf("scoped event scanned root: %v", client.calls)
		}
	}
	// A previously indexed directory moved outside the source must not leak in.
	client.paths["200"] = "/other/Season 01"
	if err := s.NotifyDirectories(ctx, value.AccountID, []string{"200"}); err != nil {
		t.Fatal(err)
	}
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Directories != 0 {
		t.Fatalf("moved scope stats=%+v err=%v", stats, err)
	}
	if _, err := s.SetMonitoring(ctx, value.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.NotifyDirectories(ctx, value.AccountID, []string{"100"}); err != nil {
		t.Fatal(err)
	}
	saved, _ = db.GetDriveSTRM(ctx, value.ID)
	if saved.Queued {
		t.Fatal("disabled mapping queued by events")
	}
	if _, err := s.SetMonitoring(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	saved, _ = db.GetDriveSTRM(ctx, value.ID)
	if !saved.Queued || !saved.FullScan {
		t.Fatal("enabling monitoring failed to repair paused period")
	}
}

func TestDriveSTRMCredentialsRemainPinnedAndWorkerStops(t *testing.T) {
	s, db, client, value, _ := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "a.mkv"), driveSTRMTestVideo("2", "b.mkv"), driveSTRMTestVideo("3", "c.mkv")}
	client.onList = func(context.Context) {
		a, _ := s.Accounts.Get(value.AccountID)
		cookie := "UID=42_B"
		if err := s.Accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
			t.Error(err)
		}
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	for _, call := range client.calls {
		if !strings.HasSuffix(call, ":UID=42_A") {
			t.Fatalf("run changed cookie: %s", call)
		}
	}
	// Cancellation returns promptly and persists the interrupted attempt.
	if err := s.Queue(context.Background(), value.ID, true); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	client.onList = func(ctx context.Context) { close(started); <-ctx.Done() }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker failed to stop")
	}
	saved, err := db.GetDriveSTRM(context.Background(), value.ID)
	if err != nil || !saved.Queued || !saved.FullScan {
		t.Fatalf("shutdown checkpoint=%+v err=%v", saved, err)
	}
}

func TestDriveSTRMCollisionsAndUnrelatedFilesArePreserved(t *testing.T) {
	s, _, client, _, root := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "same.mkv"), driveSTRMTestVideo("2", "same.mp4"), driveSTRMTestVideo("3", "unrelated.mkv")}
	if err := os.MkdirAll(filepath.Join(root, "strm"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "strm", "unrelated.strm"), []byte("https://other/file"), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Written != 1 || stats.Failed != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "strm", "unrelated.strm"))
	if string(data) != "https://other/file" {
		t.Fatal("foreign output overwritten")
	}
}

func TestDriveSTRMReplacementFileCannotOverwriteOldManagedOutput(t *testing.T) {
	s, _, client, value, root := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "same.mkv")}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(root, "strm", "same.strm")
	original, _ := os.ReadFile(filename)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("2", "same.mp4")}
	if err := s.Queue(context.Background(), value.ID, true); err != nil {
		t.Fatal(err)
	}
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Failed != 1 || stats.Written != 0 {
		t.Fatalf("replacement stats=%+v err=%v", stats, err)
	}
	actual, _ := os.ReadFile(filename)
	if string(actual) != string(original) {
		t.Fatal("different source file overwrote previous owned output")
	}
}

func TestDriveSTRMSanitizedDirectoryCollisionIsReported(t *testing.T) {
	s, _, client, _, root := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{{ID: "200", Name: "same?", Directory: true}, {ID: "300", Name: "same*", Directory: true}}
	client.files["200"] = []p115.ShareFile{driveSTRMTestVideo("1", "first.mkv")}
	client.files["300"] = []p115.ShareFile{driveSTRMTestVideo("2", "second.mkv")}
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Failed != 1 || stats.Written != 1 {
		t.Fatalf("collision stats=%+v err=%v", stats, err)
	}
	if _, err := os.Stat(filepath.Join(root, "strm", "same_", "second.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conflicting directory was mixed in: %v", err)
	}
}

func TestDriveSTRMStoppedAttemptDoesNotRestartOrDisableMonitoring(t *testing.T) {
	s, db, client, value, _ := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "a.mkv")}
	value, ctx, found, err := s.next(context.Background())
	if err != nil || !found {
		t.Fatalf("next found=%v err=%v", found, err)
	}
	if _, err := s.Save(context.Background(), value); !errors.Is(err, ErrDriveSTRMBusy) {
		t.Fatalf("save during scan: %v", err)
	}
	if err := s.Delete(context.Background(), value.ID); !errors.Is(err, ErrDriveSTRMBusy) {
		t.Fatalf("delete during scan: %v", err)
	}
	if err := s.Stop(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	stats, scanErr := s.scan(ctx, value)
	if err := s.finish(context.Background(), value.ID, stats, scanErr); err != nil {
		t.Fatal(err)
	}
	saved, _ := db.GetDriveSTRM(context.Background(), value.ID)
	if saved.Queued || !saved.Enabled || saved.Status != "stopped" {
		t.Fatalf("stopped=%+v", saved)
	}
	if _, _, found, err := s.next(context.Background()); err != nil || found {
		t.Fatalf("stopped restarted found=%v err=%v", found, err)
	}
}

func TestDriveSTRMChangedPaginationIsNotReportedComplete(t *testing.T) {
	s, _, client, _, _ := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "a.mkv"), driveSTRMTestVideo("2", "b.mkv"), driveSTRMTestVideo("3", "c.mkv")}
	client.changedCount = true
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Failed != 1 || stats.Written != 2 {
		t.Fatalf("pagination stats=%+v err=%v", stats, err)
	}
}

func TestDriveSTRMAllVideosPrecedeAttachmentDownloads(t *testing.T) {
	s, _, client, _, root := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{{ID: "10", Name: "poster.jpg", Size: 5, SHA1: strings.Repeat("A", 40)}, driveSTRMTestVideo("1", "a.mkv"), driveSTRMTestVideo("2", "last.mkv")}
	client.metadata["10"] = []byte("image")
	client.onMetadata = func() {
		if _, err := os.Stat(filepath.Join(root, "strm", "last.strm")); err != nil {
			t.Error("attachment download started before later video was generated")
		}
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
}

func TestDriveSTRMFailedScopeDoesNotBlockAnotherScope(t *testing.T) {
	s, _, client, value, _ := driveSTRMTestSetup(t)
	client.paths["300"] = "/library/Season 02"
	client.files["100"] = []p115.ShareFile{{ID: "200", Name: "Season 01", Directory: true}, {ID: "300", Name: "Season 02", Directory: true}}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	delete(client.paths, "200")
	client.files["300"] = []p115.ShareFile{driveSTRMTestVideo("1", "episode.mkv")}
	if err := s.NotifyDirectories(context.Background(), value.AccountID, []string{"200", "300"}); err != nil {
		t.Fatal(err)
	}
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Failed != 1 || stats.Written != 1 {
		t.Fatalf("scope stats=%+v err=%v", stats, err)
	}
}

func TestDriveSTRMSaveRemoteValidationDoesNotBlockStop(t *testing.T) {
	s, _, client, value, _ := driveSTRMTestSetup(t)
	started, release := make(chan struct{}), make(chan struct{})
	client.onIdentity = func(context.Context) { close(started); <-release }
	done := make(chan error, 1)
	other := value
	other.ID = ""
	other.OutputDir = "other-strm"
	go func() { _, err := s.Save(context.Background(), other); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("save did not reach validation")
	}
	stopDone := make(chan error, 1)
	go func() { stopDone <- s.Stop(context.Background(), value.ID) }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("remote validation blocked stop")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("save did not finish")
	}
}

func TestDriveSTRMFailureBackoffIsPersistentAndManualRetryBypassesIt(t *testing.T) {
	s, db, client, value, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	client.pageErr = errors.New("temporarily unavailable")
	for attempt, want := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 24 * time.Hour} {
		if attempt > 0 {
			saved, _ := db.GetDriveSTRM(ctx, value.ID)
			saved.NextScanAt = time.Now().Add(-time.Second)
			if err := db.SaveDriveSTRM(ctx, saved); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := driveSTRMTestAttempt(t, s); err == nil {
			t.Fatal("failed scan succeeded")
		}
		saved, _ := db.GetDriveSTRM(ctx, value.ID)
		if saved.FailureCount != attempt+1 || saved.NextScanAt.Sub(saved.LastScanAt) != want {
			t.Fatalf("backoff=%+v", saved)
		}
		if err := s.NotifyDirectories(ctx, value.AccountID, []string{"100"}); err != nil {
			t.Fatal(err)
		}
		if _, _, found, err := s.next(ctx); err != nil || found {
			t.Fatalf("event bypassed backoff found=%v err=%v", found, err)
		}
	}
	if err := s.Queue(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := s.next(ctx); err != nil || !found {
		t.Fatalf("manual retry did not bypass backoff found=%v err=%v", found, err)
	}
	if err := s.Stop(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(ctx, value.ID, model.DriveSTRMStats{}, context.Canceled); err != nil {
		t.Fatal(err)
	}
}
