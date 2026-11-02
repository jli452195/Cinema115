package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

func TestMediaOrganizerCD2EmptyDirectoriesCleanDeepestFirstWithAttachmentCleanupDisabled(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	rule.AttachmentCleanupEnabled = false
	var err error
	rule, err = s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	fs := connection.filesystem
	for _, name := range []string{"empty", "empty/a", "empty/a/b", "download.partial", "keep"} {
		fs.add(path.Join(rule.Source, name), true, 0)
	}
	fs.add(path.Join(rule.Source, "keep/orphan.ass"), false, 4)
	mediaOrphanScan(t, s, rule)
	want := []string{path.Join(rule.Source, "empty/a/b"), path.Join(rule.Source, "empty/a"), path.Join(rule.Source, "download.partial"), path.Join(rule.Source, "empty")}
	if !slices.Equal(fs.deleted, want) {
		t.Fatal("应先删最深空子目录，包含空临时目录", fs.deleted, want)
	}
	if _, ok := fs.files[rule.Source]; !ok {
		t.Fatal("来源根目录必须保留")
	}
	if _, ok := fs.files[path.Join(rule.Source, "keep/orphan.ass")]; !ok {
		t.Fatal("附件清理关闭时非空目录和附件必须保留")
	}
}

func TestMediaOrganizerCD2PartialScanStillCleansIndependentEmptyDirectories(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	empty, broken := path.Join(rule.Source, "a-empty"), path.Join(rule.Source, "z-unreadable")
	fs.add(empty, true, 0)
	fs.add(broken, true, 0)
	failure := errors.New("模拟子目录读取失败")
	fs.onList = func(directory string) error {
		if directory == broken {
			return failure
		}
		return nil
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = s.process(context.Background(), &run, "auto")
	if !errors.Is(err, failure) {
		t.Fatal("应保留原扫描错误", err)
	}
	if _, ok := fs.files[empty]; ok {
		t.Fatal("其他目录读取失败不能阻止已发现空目录的收尾")
	}
	if _, ok := fs.files[broken]; !ok {
		t.Fatal("无法复核的目录不得删除")
	}
}

func TestMediaOrganizerCD2EmptyDirectoryRechecksIdentityAndNewContent(t *testing.T) {
	for _, scenario := range []string{"replacement", "new-content", "no-capability"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := connection.filesystem
			full := path.Join(rule.Source, "empty")
			fs.add(full, true, 0)
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "replacement":
				fs.add(full, true, 0)
			case "new-content":
				fs.onList = func(directory string) error {
					if directory == full {
						fs.add(path.Join(full, "new.mkv"), false, 4)
					}
					return nil
				}
			case "no-capability":
				files.cd2 = struct{ clouddrive2.Filesystem }{fs}
			}
			run, _ := s.Start(rule.ID)
			err = s.cleanupEmptyDirectories(context.Background(), files, &run)
			if scenario == "no-capability" && err == nil {
				t.Fatal("缺少复核接口不能回退到递归Delete")
			}
			if scenario != "no-capability" && err != nil {
				t.Fatal(err)
			}
			if len(fs.deleted) != 0 {
				t.Fatal("替换目录、新内容或缺少复核能力都必须保留", fs.deleted)
			}
		})
	}
}

func TestMediaOrganizer115EmptyDirectoryIgnoresStaleScanListing(t *testing.T) {
	s, drive, rule := newMediaCleanupCloud(t)
	drive.dirs[rule.SourceID] = []p115.ShareFile{{ID: "30", Name: "nested", Directory: true}}
	drive.paths["30"] = path.Join(rule.Source, "nested")
	drive.dirs["30"] = []p115.ShareFile{{ID: "500", Name: "old.mkv", Size: 4}}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	drive.dirs["30"] = nil
	run, _ := s.Start(rule.ID)
	if err := s.cleanupEmptyDirectories(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(drive.deleted, "30") {
		t.Fatal("整理前非空的目录应重新读取，不能被扫描缓存跳过", drive.deleted)
	}
	if slices.Contains(drive.deleted, rule.SourceID) {
		t.Fatal("来源根目录必须保留")
	}
}

func TestMediaOrganizerLocalEmptyDirectoryReplacementIsRetained(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	full := filepath.Join(rule.Source, "empty")
	if err := os.Mkdir(full, 0755); err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(full, filepath.Join(t.TempDir(), "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(full, 0755); err != nil {
		t.Fatal(err)
	}
	run, _ := s.Start(rule.ID)
	if err := s.cleanupEmptyDirectories(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(full); err != nil {
		t.Fatal("替换后的同路径空目录必须保留", err)
	}
}

type mediaCompletionAuditStore struct {
	accountSecretStore
	inspect func(string)
}

func (store mediaCompletionAuditStore) Save(raw string) error {
	store.inspect(raw)
	return store.accountSecretStore.Save(raw)
}

func TestMediaOrganizerTerminalStateWaitsForEmptyDirectoryCleanup(t *testing.T) {
	for _, storage := range []string{"local", "115", "cd2"} {
		for _, status := range []string{"completed", "partial"} {
			t.Run(storage+"/"+status, func(t *testing.T) {
				fixture := newMediaOrphanFixture(t, storage)
				s, rule := fixture.organizer, fixture.rule
				var present func() bool
				switch storage {
				case "local":
					full := filepath.Join(rule.Source, "empty")
					if err := os.Mkdir(full, 0755); err != nil {
						t.Fatal(err)
					}
					present = func() bool { _, err := os.Stat(full); return err == nil }
				case "115":
					drive := s.drive.(*mediaCleanupDrive)
					drive.dirs[rule.SourceID] = append(drive.dirs[rule.SourceID], p115.ShareFile{ID: "30", Name: "empty", Directory: true})
					drive.paths["30"], drive.dirs["30"] = path.Join(rule.Source, "empty"), nil
					present = func() bool { return !slices.Contains(drive.deleted, "30") }
				case "cd2":
					fs := s.cd2.(*mediaCD2AcquirerFake).filesystem
					full := path.Join(rule.Source, "empty")
					fs.add(full, true, 0)
					present = func() bool { _, exists := fs.files[full]; return exists }
				}
				if status == "partial" {
					s.metadata.(*mediaRecognitionFake).items = nil
					fixture.add("Unknown.mkv")
				}
				run, err := s.Start(rule.ID)
				if err != nil {
					t.Fatal(err)
				}
				published := 0
				repository := s.db.(*testMediaOrganizeStore)
				repository.source = mediaCompletionAuditStore{repository.source, func(raw string) {
					var saved mediaOrganizeDocument
					if err := json.Unmarshal([]byte(raw), &saved); err != nil {
						t.Fatal(err)
					}
					for _, current := range saved.Runs {
						if current.ID == run.ID && !mediaRunActive(current.Status) {
							published++
							if present() {
								t.Error("空目录尚未清理就发布了终态", current.Status)
							}
							if current.Status != status {
								t.Error("收尾改变了原始整理终态", current.Status, status)
							}
						}
					}
				}}
				mediaTestProcess(t, s, &run, "auto")
				if published != 1 || present() || run.Status != status {
					t.Fatal("应在完成空目录收尾后统一公布终态", published, run.Status)
				}
			})
		}
	}
}
