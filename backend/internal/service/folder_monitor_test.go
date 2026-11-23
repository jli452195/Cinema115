package service

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
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

func TestFolderMonitorMovingRequiresFrozenIDs(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	value.Stage, value.BatchDirectoryID, value.BatchDirectoryName = "moving", "900", "Cinema115-临时-test"
	value.TaskPending = true
	if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "blocked" || got.BatchDirectoryID != "900" || len(client.moveCalls) != 0 {
		t.Fatalf("没有本批范围时不应挪动来源: %+v, calls=%v", got, client.moveCalls)
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

func TestFolderMonitorSharesOnlySavedItems(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	client.drive["900"] = []p115.ShareFile{
		{ID: "21", Name: "show", Directory: true},
		{ID: "22", Name: "movie.mkv", Size: 10},
		{ID: "23", Name: "other.txt", Size: 20},
	}
	value.Stage, value.BatchDirectoryID, value.BatchDirectoryName = "sharing", "900", "Cinema115-临时-test"
	value.BatchIDs, value.BatchMoveIndex, value.TaskPending = []string{"21", "22"}, 2, true
	if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "completed" || len(client.shareCalls) != 1 || !sameIDs(client.shareCalls[0], []string{"21", "22"}) {
		t.Fatalf("分享范围不等于本批范围: %+v, calls=%v", got, client.shareCalls)
	}
	if len(client.moveCalls) != 0 || len(client.deleted) != 0 {
		t.Fatal("已搬运批次不应再次搬运或回收")
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
