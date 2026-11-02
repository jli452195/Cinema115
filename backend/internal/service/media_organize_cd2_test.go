package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

type mediaCD2Fake struct {
	files       map[string]clouddrive2.File
	statErrors  map[string]error
	moveFailure error
	changeIDs   bool
	pathIDs     bool
	deleted     []string
	writes      int
	serial      int
	onList      func(string) error
	listed      []string
	statCalls   []string
}

func newMediaCD2Fake() *mediaCD2Fake {
	f := &mediaCD2Fake{files: make(map[string]clouddrive2.File), statErrors: make(map[string]error)}
	for _, directory := range []string{"/", "/其他网盘", "/其他网盘/inbox", "/另一个账号", "/另一个账号/library", "/其他网盘/failed"} {
		f.add(directory, true, 0)
	}
	return f
}

func (f *mediaCD2Fake) add(full string, directory bool, size int64) clouddrive2.File {
	f.serial++
	file := clouddrive2.File{ID: fmt.Sprintf("native-%d", f.serial), Path: full, Name: path.Base(full), Directory: directory, Size: size, Modified: time.Now().Add(-time.Minute), CloudName: "模拟其他网盘", CloudAccount: strings.Split(strings.TrimPrefix(full, "/"), "/")[0]}
	if !directory {
		file.Hashes = map[uint32]string{2: fmt.Sprintf("hash-%d", f.serial)}
	}
	f.files[full] = file
	return file
}

func (f *mediaCD2Fake) Stat(ctx context.Context, full string, force bool) (clouddrive2.File, error) {
	f.statCalls = append(f.statCalls, full)
	if err := ctx.Err(); err != nil {
		return clouddrive2.File{}, err
	}
	if err := f.statErrors[full]; err != nil {
		return clouddrive2.File{}, err
	}
	file, ok := f.files[full]
	if !ok {
		return clouddrive2.File{}, clouddrive2.ErrNotFound
	}
	file.Hashes = maps.Clone(file.Hashes)
	return file, nil
}

func (f *mediaCD2Fake) List(ctx context.Context, directory string, force bool) ([]clouddrive2.File, error) {
	f.listed = append(f.listed, directory)
	if f.onList != nil {
		if err := f.onList(directory); err != nil {
			return nil, err
		}
	}
	info, err := f.Stat(ctx, directory, force)
	if err != nil {
		return nil, err
	}
	if !info.Directory {
		return nil, errors.New("不是目录")
	}
	files := make([]clouddrive2.File, 0)
	for full, file := range f.files {
		if full != directory && path.Dir(full) == directory {
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(a, b clouddrive2.File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func (f *mediaCD2Fake) CreateDirectory(ctx context.Context, parent, name string) (clouddrive2.File, error) {
	if _, err := f.Stat(ctx, parent, true); err != nil {
		return clouddrive2.File{}, err
	}
	full := path.Join(parent, name)
	if _, ok := f.files[full]; ok {
		return clouddrive2.File{}, clouddrive2.ErrExists
	}
	f.writes++
	return f.add(full, true, 0), nil
}

func (f *mediaCD2Fake) Rename(ctx context.Context, full, name string) (string, error) {
	file, err := f.Stat(ctx, full, true)
	if err != nil {
		return "", err
	}
	parent, destination := path.Dir(full), path.Join(path.Dir(full), name)
	for other, item := range f.files {
		if other != full && path.Dir(other) == parent && strings.EqualFold(item.Name, name) {
			return "", clouddrive2.ErrExists
		}
	}
	f.writes++
	file.Name, file.Path = name, destination
	if f.pathIDs {
		file.ID, file.Modified = destination, time.Now()
	}
	f.files[destination] = file
	delete(f.files, full)
	return destination, nil
}

func (f *mediaCD2Fake) Move(ctx context.Context, sources []string, destination string) ([]string, error) {
	if f.moveFailure != nil {
		err := f.moveFailure
		f.moveFailure = nil
		return nil, err
	}
	results := make([]string, 0, len(sources))
	for _, full := range sources {
		file, err := f.Stat(ctx, full, true)
		if err != nil {
			return nil, err
		}
		target := path.Join(destination, file.Name)
		if _, ok := f.files[target]; ok {
			return nil, clouddrive2.ErrExists
		}
		f.writes++
		if f.changeIDs {
			file.ID = "跨云-" + file.ID
		}
		if f.pathIDs {
			file.ID, file.Modified = target, time.Now()
		}
		file.Path = target
		f.files[target] = file
		delete(f.files, full)
		results = append(results, target)
	}
	return results, nil
}

func (f *mediaCD2Fake) Delete(ctx context.Context, paths []string) error {
	for _, full := range paths {
		file, err := f.Stat(ctx, full, true)
		if err != nil {
			return err
		}
		f.writes++
		f.deleted = append(f.deleted, full)
		delete(f.files, full)
		if file.Directory {
			for child := range f.files {
				if mediaCloudWithin(full, child) {
					delete(f.files, child)
				}
			}
		}
	}
	return nil
}

func (f *mediaCD2Fake) RemoveEmptyDirectory(ctx context.Context, full, expectedID string) (bool, error) {
	file, err := f.Stat(ctx, full, true)
	if errors.Is(err, clouddrive2.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !file.Directory || file.ReadOnly || expectedID != "" && file.ID != expectedID {
		return false, nil
	}
	children, err := f.List(ctx, full, true)
	if err != nil {
		return false, err
	}
	if len(children) != 0 {
		return false, nil
	}
	if err := f.Delete(ctx, []string{full}); err != nil {
		return false, err
	}
	return true, nil
}

type mediaCD2AcquirerFake struct {
	filesystem *mediaCD2Fake
	revision   uint64
	calls      int
	closed     int
}

func (a *mediaCD2AcquirerFake) Acquire(ctx context.Context, id string, revision uint64) (*CD2Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id != "independent-cd2" || revision != a.revision {
		return nil, errors.New("CD2 配置已改变，请重新选择目录")
	}
	a.calls++
	return &CD2Lease{ID: id, Revision: revision, Client: a.filesystem, release: func() error { a.closed++; return nil }}, nil
}

func newMediaCD2Organizer(t *testing.T) (*MediaOrganizer, *mediaRecognitionFake, *failingAccountStore, *mediaCD2AcquirerFake, MediaOrganizeRule) {
	t.Helper()
	s, recognition, store, _ := newMediaOrganizeTest(t)
	connection := &mediaCD2AcquirerFake{filesystem: newMediaCD2Fake(), revision: 3}
	s.cd2 = connection
	rule, err := s.SaveRule(MediaOrganizeRule{Name: "独立 CD2", Storage: "cd2", Source: "/其他网盘/inbox", Target: "/另一个账号/library", ConnectionID: "independent-cd2", ConnectionRevision: 3, IntervalSeconds: 20, AttachmentCleanupEnabled: true, AttachmentGraceSeconds: 600, AccountID: "不得使用的115账号", SourceID: "10", TargetID: "20"})
	if err != nil {
		t.Fatal(err)
	}
	return s, recognition, store, connection, rule
}

func TestMediaOrganizerCD2IndependentNativeScanAndMove(t *testing.T) {
	s, _, store, connection, rule := newMediaCD2Organizer(t)
	if rule.AccountID != "" || rule.SourceID != "" || rule.TargetID != "" || rule.ConnectionID == "" {
		t.Fatal("CD2 规则不得携带 115 账号或目录编号", rule)
	}
	fs := connection.filesystem
	fs.changeIDs = true
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 128)
	fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 16)
	fs.add(path.Join(rule.Source, "advertisement.zip"), false, 4)
	fs.add(path.Join(rule.Source, "empty"), true, 0)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if connection.closed != connection.calls {
		t.Fatal("保存和运行取得的连接必须全部释放", connection.calls, connection.closed)
	}
	if run.Status != "completed" {
		t.Fatalf("原生 CD2 整理未完成：%+v", run)
	}
	for _, entry := range run.Entries {
		if entry.File.MediaType == "cleanup" {
			continue
		}
		if entry.Status != "done" || entry.CD2FileID == "" || entry.CD2Hashes[2] == "" {
			t.Fatal("媒体应保留原生身份并整理成功", entry)
		}
		if _, err := fs.Stat(context.Background(), path.Join(rule.Target, entry.Relative), true); err != nil {
			t.Fatal("目标缺少媒体或字幕", err)
		}
	}
	if len(fs.deleted) != 2 || !slices.Contains(fs.deleted, path.Join(rule.Source, "advertisement.zip")) || !slices.Contains(fs.deleted, path.Join(rule.Source, "empty")) {
		t.Fatal("应清理普通无关文件和空目录", fs.deleted)
	}
	if _, ok := fs.files[path.Join(rule.Source, "empty")]; ok {
		t.Fatal("CD2 空目录应在复核后清理")
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.Snapshot(run.ID)
	if err != nil || snapshot.Rule.ConnectionID != rule.ConnectionID || snapshot.Rule.AccountID != "" {
		t.Fatal("CD2 独立连接未持久化", snapshot, err)
	}
	for i := range snapshot.Entries {
		if len(snapshot.Entries[i].CD2Hashes) > 0 {
			snapshot.Entries[i].CD2Hashes[2] = "被修改的副本"
		}
	}
	again, _ := restarted.Snapshot(run.ID)
	for _, entry := range again.Entries {
		if entry.CD2Hashes[2] == "被修改的副本" {
			t.Fatal("条目哈希不应共享可变状态")
		}
	}
}

func mediaCD2Entry(file clouddrive2.File, root, relative string) MediaOrganizeEntry {
	return MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: path.Dir(file.Path), SourcePath: strings.TrimPrefix(file.Path, root+"/"), MediaType: "video"}, CD2FileID: file.ID, CD2Hashes: maps.Clone(file.Hashes), Modified: file.Modified.UnixNano(), Relative: relative}
}

func TestMediaOrganizerCD2RenameRecoveryAndFrozenConnection(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	file := fs.add(path.Join(rule.Source, "original.mkv"), false, 123)
	entry := mediaCD2Entry(file, rule.Source, "renamed.mkv")
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	defer files.close()
	fs.moveFailure = errors.New("模拟移动暂时失败")
	if err := files.move(context.Background(), entry); err == nil {
		t.Fatal("首次移动应返回失败")
	}
	if _, ok := fs.files[path.Join(rule.Source, "renamed.mkv")]; !ok {
		t.Fatal("重命名阶段应已完成")
	}
	connection.filesystem, connection.revision = newMediaCD2Fake(), 4
	if err := files.move(context.Background(), entry); err != nil {
		t.Fatal("已有任务应继续使用固定连接，并恢复重命名后的来源", err)
	}
	if _, ok := fs.files[path.Join(rule.Target, "renamed.mkv")]; !ok {
		t.Fatal("旧连接目标未完成")
	}
	if connection.filesystem.writes != 0 {
		t.Fatal("不得在任务中途改用新连接")
	}
	if _, err := s.files(context.Background(), rule); err == nil {
		t.Fatal("新任务不得使用失效修订的目录")
	}
}

func TestMediaOrganizerCD2ConflictAndLookupErrorsNeverWrite(t *testing.T) {
	t.Run("目标同名保留来源", func(t *testing.T) {
		s, _, _, connection, rule := newMediaCD2Organizer(t)
		fs := connection.filesystem
		file := fs.add(path.Join(rule.Source, "original.mkv"), false, 123)
		fs.add(path.Join(rule.Target, "RENAMED.mkv"), false, 999)
		files, err := s.files(context.Background(), rule)
		if err != nil {
			t.Fatal(err)
		}
		defer files.close()
		if err := files.move(context.Background(), mediaCD2Entry(file, rule.Source, "renamed.mkv")); !errors.Is(err, errMediaTargetExists) {
			t.Fatal("应报告目标冲突", err)
		}
		if fs.writes != 0 || fs.files[file.Path].ID != file.ID {
			t.Fatal("冲突时不能重命名或移动来源")
		}
	})
	t.Run("查询异常不得视为目录缺失", func(t *testing.T) {
		s, _, _, connection, rule := newMediaCD2Organizer(t)
		fs := connection.filesystem
		file := fs.add(path.Join(rule.Source, "original.mkv"), false, 123)
		files, err := s.files(context.Background(), rule)
		if err != nil {
			t.Fatal(err)
		}
		defer files.close()
		failure := errors.New("模拟目录查询失败")
		fs.statErrors[path.Join(rule.Target, "movie")] = failure
		if err := files.move(context.Background(), mediaCD2Entry(file, rule.Source, "movie/renamed.mkv")); !errors.Is(err, failure) {
			t.Fatal("应保留查询错误", err)
		}
		if fs.writes != 0 {
			t.Fatal("目录查询失败后不能创建或移动")
		}
	})
}

func TestMediaOrganizerCD2FailureDirectoryAndCrossCloudIdentity(t *testing.T) {
	s, recognition, store, connection, rule := newMediaCD2Organizer(t)
	recognition.items = nil
	rule.Failure = "/其他网盘/failed"
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	fs := connection.filesystem
	fs.changeIDs = true
	fs.add(path.Join(rule.Source, "Unknown.S01E01.mkv"), false, 123)
	fs.add(path.Join(rule.Source, "Unknown.S01E01.ass"), false, 8)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	for _, entry := range run.Entries {
		if entry.Failure == nil || entry.Failure.State != "stored" || entry.Failure.CD2FileID == entry.CD2FileID {
			t.Fatal("失败目录应保存移动后原生身份", entry)
		}
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	videoID := ""
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" {
			videoID = entry.File.ID
		}
	}
	if _, err := restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: videoID, Kind: "tv", TMDBID: 42}); err != nil {
		t.Fatal(err)
	}
	corrected, _ := restarted.Snapshot(run.ID)
	mediaTestProcess(t, restarted, &corrected, "retry")
	for _, entry := range corrected.Entries {
		if entry.Status != "done" || entry.Failure != nil {
			t.Fatal("修正后应从失败目录恢复视频和字幕", entry)
		}
		if _, ok := fs.files[path.Join(rule.Target, entry.Relative)]; !ok {
			t.Fatal("修正目标缺少文件", entry.Relative)
		}
	}
}

func TestMediaOrganizerCD2CleanupRechecksNativeIdentityAndNonemptyFolders(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "changed.zip"), false, 10)
	fs.add(path.Join(rule.Source, "empty"), true, 0)
	fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 8)
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	defer files.close()
	if _, err := files.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	fs.add(path.Join(rule.Source, "changed.zip"), false, 10)
	fs.add(path.Join(rule.Source, "empty/new.mkv"), false, 99)
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	run, _ := s.Start(rule.ID)
	if err := s.cleanupCD2(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if len(fs.deleted) != 0 {
		t.Fatal("变化文件和非空目录都应保留", fs.deleted)
	}
}

func TestMediaOrganizerCD2RejectsOverlapReadOnlyAndChangedRevision(t *testing.T) {
	for _, scenario := range []string{"overlap", "read-only", "revision"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			rule.ID = ""
			switch scenario {
			case "overlap":
				rule.Target = path.Join(rule.Source, "library")
			case "read-only":
				file := connection.filesystem.files[rule.Target]
				file.ReadOnly = true
				connection.filesystem.files[rule.Target] = file
			case "revision":
				rule.ConnectionRevision++
			}
			if _, err := s.SaveRule(rule); err == nil {
				t.Fatal("应拒绝无效 CD2 规则")
			}
		})
	}
}

func TestMediaOrganizerCD2LateCompanionsAndTemporaryDownloadsStayTogether(t *testing.T) {
	for _, lateName := range []string{"Work.S01E01.nfo", "Work.S01E01.mkv.part"} {
		t.Run(lateName, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := connection.filesystem
			fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
			fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 8)
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			entries, err := files.scan(context.Background())
			if err != nil || len(entries) != 2 {
				t.Fatal("初始视频与字幕应已稳定", entries, err)
			}
			for i := range entries {
				entries[i].Status, entries[i].Relative = "ready", entries[i].File.Name
			}
			late := fs.add(path.Join(rule.Source, lateName), false, 4)
			late.Modified = time.Now()
			fs.files[late.Path] = late
			run := MediaOrganizeRun{Rule: rule, Entries: entries}
			if err := files.recheckLocalCompanions(context.Background(), &run); err != nil {
				t.Fatal(err)
			}
			for _, entry := range run.Entries {
				if entry.Status != "retained" || entry.Relative != "" {
					t.Fatal("新附件或临时下载出现后应保留整组", entry)
				}
			}
		})
	}
}

func TestMediaOrganizerCD2SameIDChangedContentIsRejected(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	file := fs.add(path.Join(rule.Source, "original.mkv"), false, 123)
	entry := mediaCD2Entry(file, rule.Source, "renamed.mkv")
	file.Hashes = map[uint32]string{2: "同编号文件的新内容"}
	fs.files[file.Path] = file
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	defer files.close()
	if err := files.move(context.Background(), entry); err == nil || fs.writes != 0 {
		t.Fatal("相同编号与大小不能覆盖内容校验冲突", err)
	}
}

func TestMediaOrganizerCD2PathBasedIDsResumeFromPersistedWriteReceipt(t *testing.T) {
	s, _, store, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.pathIDs = true
	file := fs.add(path.Join(rule.Source, "original.mkv"), false, 123)
	file.Hashes = nil
	fs.files[file.Path] = file
	entry := mediaCD2Entry(file, rule.Source, "renamed.mkv")
	entry.Status = "ready"
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Entries = []MediaOrganizeEntry{entry}
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	defer files.close()
	s.bindCD2Checkpoint(files, &run)
	fs.moveFailure = errors.New("模拟移动暂时失败")
	if err := files.move(context.Background(), run.Entries[0]); err == nil {
		t.Fatal("首次移动应失败")
	}
	location := run.Entries[0].CD2Current
	if location == nil || location.FileID != path.Join(rule.Source, "renamed.mkv") || location.Modified == entry.Modified {
		t.Fatal("应保存重命名成功后的路径型编号和新时间戳", location)
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	snapshot, err := restarted.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := restarted.files(context.Background(), snapshot.Rule)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.close()
	restarted.bindCD2Checkpoint(resumed, &snapshot)
	if err := resumed.move(context.Background(), snapshot.Entries[0]); err != nil {
		t.Fatal("重启后应使用写入回执中的新身份恢复", err)
	}
	writes := fs.writes
	if err := resumed.move(context.Background(), snapshot.Entries[0]); err != nil || fs.writes != writes {
		t.Fatal("已完成回执应支持核对且不重复移动", err, fs.writes, writes)
	}
}
