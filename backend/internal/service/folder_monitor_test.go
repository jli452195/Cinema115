package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type folderMonitorClientFake struct {
	drive           map[string][]p115.ShareFile
	share           map[string][]p115.ShareFile
	deleted         []string
	addOnShare      p115.ShareFile
	nextDirectoryID string
	createdNames    []string
	moveCalls       [][]string
	shareCalls      [][]string
	configureErr    error
	onShare         func()
	onMove          func()
	listHook        func(context.Context, string, int, string) error
	driveHook       func(context.Context, string, int, string) error
}

func (f *folderMonitorClientFake) CreateDirectory(_ context.Context, parent, name, _ string) (string, error) {
	if f.nextDirectoryID == "" {
		f.nextDirectoryID = "900"
	}
	f.createdNames = append(f.createdNames, name)
	f.drive[f.nextDirectoryID] = nil
	f.drive[parent] = append(f.drive[parent], p115.ShareFile{ID: f.nextDirectoryID, Name: name, Directory: true})
	return f.nextDirectoryID, nil
}

func (f *folderMonitorClientFake) MoveEntries(_ context.Context, ids []string, cid, _ string) error {
	f.moveCalls = append(f.moveCalls, append([]string(nil), ids...))
	items := f.drive["100"]
	remaining := make([]p115.ShareFile, 0, len(items))
	for _, item := range items {
		if containsID(ids, item.ID) {
			f.drive[cid] = append(f.drive[cid], item)
			continue
		}
		remaining = append(remaining, item)
	}
	f.drive["100"] = remaining
	if f.onMove != nil {
		f.onMove()
	}
	return nil
}

func TestFolderMonitorLegacyMoveFreezesRemainingSourceIDs(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(secret, "")
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := accounts.AddPrimary("monitor")
	if err != nil {
		t.Fatal(err)
	}
	account, _ := accounts.Get(accountID)
	cookie := "UID=42_A"
	if err := accounts.Update(accountID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	client := &folderMonitorClientFake{drive: map[string][]p115.ShareFile{
		"100": {{ID: "11", Name: "a.mkv", Size: 10}, {ID: "12", Name: "b.mkv", Size: 20}},
		"900": {},
	}, share: map[string][]p115.ShareFile{}}
	svc := NewFolderMonitorService(db, accounts, client, &folderMonitorGeneratorFake{}, t.TempDir(), nil)
	value := model.FolderMonitor{ID: "drain", Name: "monitor", AccountID: accountID, SourceCID: "100", SourcePath: "/inbox", OutputDir: "strm", BaseURL: "http://nas:3002", VideoExtensions: "mkv", MetadataExtensions: "srt", Recursive: true, Enabled: true, IntervalSeconds: 30, Stage: "moving", BatchDirectoryID: "900", BatchDirectoryName: "Cinema115-临时-test", BatchParentCID: "0", UpdatedAt: time.Now().UTC()}
	if err := db.SaveFolderMonitor(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if err := svc.moveBatch(context.Background(), &value, account); err != nil {
		t.Fatal(err)
	}
	if value.Stage != "sharing" || value.BatchMoveIndex != 2 || len(value.BatchIDs) != 2 {
		t.Fatalf("value=%+v", value)
	}
	if len(client.moveCalls) != 1 || len(client.moveCalls[0]) != 2 {
		t.Fatalf("moveCalls=%v", client.moveCalls)
	}
	if len(client.drive["100"]) != 0 || len(client.drive["900"]) != 2 {
		t.Fatalf("drive=%+v", client.drive)
	}
	saved, err := db.GetFolderMonitor(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(saved.BatchIDs, []string{"11", "12"}) {
		t.Fatalf("source IDs were not frozen: %+v", saved.BatchIDs)
	}
}

func TestBatchDirectoryNameUsesCinema115TemporaryPrefix(t *testing.T) {
	first, second := batchDirectoryName(), batchDirectoryName()
	if !strings.HasPrefix(first, "Cinema115-临时-") || !strings.HasPrefix(second, "Cinema115-临时-") {
		t.Fatalf("names=%q,%q", first, second)
	}
	if first == second {
		t.Fatalf("temporary directory names unexpectedly repeated: %q", first)
	}
}

func TestFolderMonitorSharesTemporaryDirectoryChildren(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(secret, "")
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := accounts.AddPrimary("monitor")
	if err != nil {
		t.Fatal(err)
	}
	account, _ := accounts.Get(accountID)
	cookie := "UID=42_A"
	if err := accounts.Update(accountID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	configureErr := errors.New("stop after share")
	client := &folderMonitorClientFake{
		drive: map[string][]p115.ShareFile{
			"900": {{ID: "21", Name: "show", Directory: true}, {ID: "22", Name: "movie.mkv", Size: 10}},
			"21":  {},
		},
		share:        map[string][]p115.ShareFile{},
		configureErr: configureErr,
	}
	svc := NewFolderMonitorService(db, accounts, client, &folderMonitorGeneratorFake{}, t.TempDir(), nil)
	value := model.FolderMonitor{ID: "share", Name: "monitor", AccountID: accountID, SourceCID: "100", OutputDir: "strm", BaseURL: "http://nas:3002", VideoExtensions: "mkv", MetadataExtensions: "srt", Recursive: true, Enabled: true, IntervalSeconds: 30, Stage: "sharing", BatchDirectoryID: "900", BatchDirectoryName: "Cinema115-临时-test", BatchParentCID: "0", UpdatedAt: time.Now().UTC()}
	if err := db.SaveFolderMonitor(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if err := svc.resume(context.Background(), &value, account); err == nil || !strings.Contains(err.Error(), configureErr.Error()) {
		t.Fatalf("resume error=%v", err)
	}
	if len(client.shareCalls) != 1 || !sameIDs(client.shareCalls[0], []string{"21", "22"}) {
		t.Fatalf("share calls=%v", client.shareCalls)
	}
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsID(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}

func pageOf(files []p115.ShareFile, offset int) *p115.SharePage {
	if offset >= len(files) {
		return &p115.SharePage{Count: len(files), Offset: offset, Files: []p115.ShareFile{}}
	}
	return &p115.SharePage{Count: len(files), Offset: offset, Files: append([]p115.ShareFile(nil), files[offset:]...)}
}

func (f *folderMonitorClientFake) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	if f.driveHook != nil {
		if err := f.driveHook(ctx, cid, offset, cookie); err != nil {
			return nil, err
		}
	}
	return pageOf(f.drive[cid], offset), nil
}

func (f *folderMonitorClientFake) SendShare(_ context.Context, ids []string, _ string) (*p115.ShareInfo, error) {
	if f.onShare != nil {
		f.onShare()
	}
	f.shareCalls = append(f.shareCalls, append([]string(nil), ids...))
	f.share["0"] = make([]p115.ShareFile, 0, len(ids))
	for _, id := range ids {
		for _, items := range f.drive {
			for _, item := range items {
				if item.ID == id {
					f.share["0"] = append(f.share["0"], item)
				}
			}
		}
	}
	if f.addOnShare.ID != "" {
		f.drive["100"] = append(f.drive["100"], f.addOnShare)
	}
	return &p115.ShareInfo{Code: "own", Password: "old", URL: "https://115cdn.com/s/own?password=old"}, nil
}

func (f *folderMonitorClientFake) ConfigureShare(_ context.Context, share *p115.ShareInfo, password, _ string) error {
	if f.configureErr != nil {
		return f.configureErr
	}
	share.Password = password
	share.URL = "https://115cdn.com/s/own?password=" + password
	return nil
}

func (f *folderMonitorClientFake) List(ctx context.Context, _ *p115.ShareInfo, cid string, offset int, cookie string) (*p115.SharePage, error) {
	if f.listHook != nil {
		if err := f.listHook(ctx, cid, offset, cookie); err != nil {
			return nil, err
		}
	}
	return pageOf(f.share[cid], offset), nil
}

func (f *folderMonitorClientFake) DownloadURL(context.Context, *p115.ShareInfo, string, string) (string, error) {
	return "https://example.115cdn.com/file", nil
}

func (f *folderMonitorClientFake) DeleteEntries(_ context.Context, ids []string, _ string) error {
	for _, id := range ids {
		f.deleted = append(f.deleted, id)
		for cid, entries := range f.drive {
			items := entries[:0]
			for _, item := range entries {
				if item.ID != id {
					items = append(items, item)
				}
			}
			f.drive[cid] = items
		}
		delete(f.drive, id)
	}
	return nil
}

type folderMonitorGeneratorFake struct {
	calls int
	hook  func(context.Context, GenerateInput) error
}

func (f *folderMonitorGeneratorFake) Generate(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
	f.calls++
	if f.hook != nil {
		if err := f.hook(ctx, input); err != nil {
			return nil, err
		}
	}
	return &GenerationResult{Written: 1, Files: []GeneratedSTRM{{Path: "movie.strm"}}}, nil
}

func TestManifestFingerprintDirectorySizeCompatibility(t *testing.T) {
	drive := []folderManifestEntry{
		{Path: "show", ID: "11", Directory: true},
		{Path: "show/episode.mkv", ID: "12", Size: 1234},
	}
	shared := []folderManifestEntry{
		{Path: "show", ID: "11", Size: 1234, Directory: true},
		{Path: "show/episode.mkv", ID: "12", Size: 1234},
	}
	if manifestFingerprint(drive) != manifestFingerprint(shared) {
		t.Fatal("directory aggregate size must not change the normalized fingerprint")
	}
	legacyDriveFingerprint := manifestFingerprintHash(drive, true, false)
	if !manifestMatchesFingerprint(shared, legacyDriveFingerprint) {
		t.Fatal("share snapshot must resume a compatible pre-v2 drive checkpoint")
	}
	shared[1].Size++
	if manifestMatchesFingerprint(shared, manifestFingerprint(drive)) || manifestMatchesFingerprint(shared, legacyDriveFingerprint) {
		t.Fatal("file size changes must still fail snapshot verification")
	}
}
