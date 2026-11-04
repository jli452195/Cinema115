package service

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

func TestMediaAttachmentCopyFamilyLocalUsesOriginal(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for _, name := range []string{"Work.S01E01.mkv", "Work.S01E01.nfo", "Work.S01E01(1).nfo", "Work.S01E01.chs.ass", "Work.S01E01.chs(2).ass", "Work.S01E01.cht.ass", "Work.S01E01.chs.forced.ass", "Work.S01E01.chs.sdh.ass"} {
		mediaTestFile(t, rule.Source, name, "内容不同："+name)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	kept := 0
	for _, entry := range run.Entries {
		if entry.File.MediaType == "cleanup" {
			continue
		}
		if entry.Status != "done" {
			t.Fatalf("同角色附件不应阻塞整理：%+v", entry)
		}
		if entry.Result != nil && entry.Result.Action == "attachment_kept" {
			kept++
			if entry.AttachmentKeep != nil {
				t.Fatal("已完成附件只保留结果，清除恢复快照", entry)
			}
		}
		if strings.HasSuffix(entry.File.Name, "01.nfo") {
			data, err := os.ReadFile(filepath.FromSlash(entry.Target))
			if err != nil || string(data) != "内容不同：Work.S01E01.nfo" {
				t.Fatal("应保留无复制后缀的原名内容", string(data), err)
			}
		}
	}
	if kept != 2 {
		t.Fatalf("应只回收同语言同角色复制附件，实际=%d，条目=%+v", kept, run.Entries)
	}
}

func TestMediaAttachmentChoiceKeyKeepsVersionsScopesAndRoles(t *testing.T) {
	video := MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, MediaType: "video"}, Group: "1", Relative: "Work/Season 01/Work.S01E01.mkv"}
	run := MediaOrganizeRun{Groups: []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}, Entries: []MediaOrganizeEntry{video}}
	sub := MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "sub"}, MediaType: "subtitle"}, Group: "1", VideoID: "video", Relative: "Work/Season 01/Work.S01E01.chs.ass"}
	key := mediaAttachmentChoiceKey(&run, sub)
	for _, name := range []string{"Work.S01E01.cht.ass", "Work.S01E01.eng.ass", "Work.S01E01.chs.eng.ass", "Work.S01E01.chs.forced.ass", "Work.S01E01.chs.sdh.ass", "Work.S01E01.chs.default.ass", "Work.S01E01.chs.commentary.ass", "Work.S01E01.chs.srt", "tvshow.nfo", "season.nfo", "poster.jpg", "Work.S01E01-thumb.jpg"} {
		other := sub
		other.Relative = path.Join(path.Dir(sub.Relative), name)
		if got := mediaAttachmentChoiceKey(&run, other); got == key {
			t.Fatal("不同语言角色或格式不能合并", name, got)
		}
	}
	alias := sub
	alias.Relative = "Work/Season 01/Work.S01E01.zh-Hans.ass"
	if mediaAttachmentChoiceKey(&run, alias) != key {
		t.Fatal("明确语言别名应统一")
	}
	otherVideo := video
	otherVideo.File.ID, otherVideo.Relative = "other-video", "Work/Season 01/Work.S01E01.2160p.mkv"
	run.Entries = append(run.Entries, otherVideo)
	other := sub
	other.VideoID = "other-video"
	if mediaAttachmentChoiceKey(&run, other) == key {
		t.Fatal("不同视频版本不能共用附件选择")
	}
	if mediaAttachmentCanonicalName("Work (2008).nfo") != "Work (2008).nfo" {
		t.Fatal("年份不得作为复制后缀删除")
	}
	if mediaAttachmentIsCopy("Work.NFO") || !mediaAttachmentIsCopy("Work(1).NFO") {
		t.Fatal("扩展名大小写不能改变原名优先级")
	}
}

func TestMediaAttachmentCopySuffixKeepsExplicitVideoVersion(t *testing.T) {
	video := func(id, name string) OrganizeFile {
		return OrganizeFile{ShareFile: p115.ShareFile{ID: id, Name: name}, MediaType: "video", ParentID: "folder", SourcePath: "folder/" + name}
	}
	sidecar := func(id, name, kind string) OrganizeFile {
		return OrganizeFile{ShareFile: p115.ShareFile{ID: id, Name: name}, MediaType: kind, ParentID: "folder", SourcePath: "folder/" + name}
	}
	files := []OrganizeFile{
		video("v1", "Work.S01E01.mkv"), video("v2", "Work.S01E01(1).mkv"),
		sidecar("n1", "Work.S01E01.nfo", "attachment"), sidecar("n2", "Work.S01E01(1).nfo", "attachment"), sidecar("n2-copy", "Work.S01E01(1)(2).nfo", "attachment"),
		sidecar("s2", "Work.S01E01(1).ass", "subtitle"), sidecar("s2-copy", "Work.S01E01(1)(2).ass", "subtitle"),
	}
	owners := mediaLocalCompanionOwners(files)
	for _, id := range []string{"n2", "n2-copy", "s2", "s2-copy"} {
		if !slices.Equal(owners[id], []string{"v2"}) {
			t.Fatal("主视频原名中括号编号优先，不得归给另一个视频版本", id, owners)
		}
	}
	run := MediaOrganizeRun{Groups: []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}}
	for _, file := range files[:5] {
		entry := MediaOrganizeEntry{File: file, Group: "1", Status: "ready", Relative: path.Join("Work/Season 01", file.Name)}
		if file.MediaType != "video" {
			entry.VideoID = owners[file.ID][0]
			entry.Relative = path.Join("Work/Season 01", mediaAttachmentMatchName(file, files))
		}
		run.Entries = append(run.Entries, entry)
	}
	mediaPlanAttachmentChoices(&run)
	if run.Entries[2].AttachmentKeep != nil || run.Entries[3].AttachmentKeep != nil || run.Entries[4].AttachmentKeep == nil || run.Entries[4].AttachmentKeep.KeeperID != "n2" {
		t.Fatal("版本隔离后仍按该视频对应的原名附件优先", run.Entries)
	}
}

func TestMediaAttachmentLocalTargetRootCanKeepExistingFile(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01(1).nfo", "来源附件")
	mediaTestFile(t, rule.Target, "Work.S01E01.nfo", "保留目标附件")
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(files.close)
	entries, err := files.scan(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	entry := entries[0]
	entry.Group, entry.VideoID, entry.Relative, entry.Status = "1", "video", "Work.S01E01.nfo", "ready"
	run.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}
	run.Entries = []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, MediaType: "video"}, Group: "1", Relative: "Work.S01E01.mkv", Status: "done"},
		entry,
	}
	if err := s.prepareAttachmentChoices(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if err := s.executePendingAttachmentChoices(context.Background(), files, &run, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(rule.Target, "Work.S01E01.nfo"))
	if err != nil || string(data) != "保留目标附件" || run.Entries[1].Status != "done" {
		t.Fatal("直接位于目标根目录的附件也应支持留一", string(data), run.Entries[1], err)
	}
}

type mediaAttachmentDeleteFake struct {
	*mediaCD2Fake
	beforeDelete func()
	beforeStat   func(string)
	deleteErr    error
	deleteCalls  int
}

func (f *mediaAttachmentDeleteFake) Stat(ctx context.Context, full string, force bool) (clouddrive2.File, error) {
	if f.beforeStat != nil {
		f.beforeStat(full)
	}
	return f.mediaCD2Fake.Stat(ctx, full, force)
}

func (f *mediaAttachmentDeleteFake) Delete(ctx context.Context, paths []string) error {
	f.deleteCalls++
	if f.beforeDelete != nil {
		f.beforeDelete()
	}
	if err := f.mediaCD2Fake.Delete(ctx, paths); err != nil {
		return err
	}
	return f.deleteErr
}

func mediaAttachmentCD2Fixture(t *testing.T) (*MediaOrganizer, *mediaOrganizeFiles, *mediaAttachmentDeleteFake, *MediaOrganizeRun, string, string) {
	t.Helper()
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := &mediaAttachmentDeleteFake{mediaCD2Fake: connection.filesystem}
	for _, directory := range []string{path.Join(rule.Target, "Work"), path.Join(rule.Target, "Work/Season 01")} {
		fs.add(directory, true, 0)
	}
	sourcePath, targetPath := path.Join(rule.Source, "Work.S01E01(1).nfo"), path.Join(rule.Target, "Work/Season 01/Work.S01E01.nfo")
	source, target := fs.add(sourcePath, false, 13), fs.add(targetPath, false, 29)
	source.Hashes, target.Hashes = nil, nil
	fs.files[sourcePath], fs.files[targetPath] = source, target
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}
	run.Entries = []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, ParentID: rule.Source, SourcePath: "Work.S01E01.mkv", MediaType: "video"}, Group: "1", Relative: "Work/Season 01/Work.S01E01.mkv", Status: "done"},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: sourcePath, Name: source.Name, Size: source.Size}, ParentID: rule.Source, SourcePath: source.Name, MediaType: "attachment"}, Group: "1", VideoID: "video", Relative: "Work/Season 01/Work.S01E01.nfo", Target: targetPath, Status: "ready", CD2FileID: source.ID, Modified: source.Modified.UnixNano()},
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(files.close)
	files.cd2 = fs
	if err := s.prepareAttachmentChoices(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	return s, files, fs, &run, sourcePath, targetPath
}

func TestMediaAttachmentTargetPreferredWithoutHashAndDifferentContents(t *testing.T) {
	s, files, fs, run, source, target := mediaAttachmentCD2Fixture(t)
	targetID := fs.files[target].ID
	fs.beforeDelete = func() {
		selection := run.Entries[1].AttachmentKeep
		if selection == nil || selection.Source == nil || selection.Keep.ID != targetID || selection.Phase == "recycling" {
			t.Fatal("回收前只在本次运行核对双方身份", run.Entries)
		}
	}
	if err := s.executePendingAttachmentChoices(context.Background(), files, run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "done" || run.Entries[1].Result.Action != "attachment_kept" || fs.files[target].ID != targetID || !slices.Equal(fs.deleted, []string{source}) {
		t.Fatal("目标已有有效附件优先，不要求内容或SHA1相等", run.Entries[1], fs.deleted)
	}
}

func TestMediaAttachmentChoicePersistenceAndIdentityFailuresKeepSource(t *testing.T) {
	for _, scenario := range []string{"persist", "target-changed", "target-read-failed"} {
		t.Run(scenario, func(t *testing.T) {
			s, files, fs, run, source, target := mediaAttachmentCD2Fixture(t)
			switch scenario {
			case "persist":
				s.persistErr = errors.New("模拟意图保存失败")
			case "target-changed":
				fs.beforeStat = func(full string) {
					if full == target && run.Entries[1].AttachmentKeep.Source != nil {
						file := fs.files[target]
						file.ID = "替换后的目标"
						fs.files[target] = file
					}
				}
			case "target-read-failed":
				fs.statErrors[target] = context.DeadlineExceeded
			}
			err := s.executePendingAttachmentChoices(context.Background(), files, run, nil)
			if scenario == "persist" && !errors.Is(err, s.persistErr) {
				t.Fatal("保存失败应向执行层返回原始错误", err)
			}
			if fs.deleteCalls != 0 || fs.files[source].ID == "" || run.Entries[1].Status == "done" || run.Entries[1].AttachmentKeep != nil {
				t.Fatal("核验或持久化失败不得回收来源", fs.deleted, run.Entries[1])
			}
		})
	}
}

func TestMediaAttachmentAppliedDeletionRetryReadsCurrentFacts(t *testing.T) {
	s, files, fs, run, source, target := mediaAttachmentCD2Fixture(t)
	fs.deleteErr = context.DeadlineExceeded
	if err := s.executePendingAttachmentChoices(context.Background(), files, run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status == "done" || run.Entries[1].AttachmentKeep != nil || fs.files[source].ID != "" || fs.deleteCalls != 1 {
		t.Fatal("响应超时结束本次尝试，清除附件选择", run.Entries[1], fs.deleted)
	}
	fs.deleteErr = nil
	if err := s.prepareAttachmentChoices(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if err := s.executePendingAttachmentChoices(context.Background(), files, run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "done" || run.Entries[1].AttachmentKeep != nil || fs.files[target].ID == "" || fs.deleteCalls != 1 {
		t.Fatal("重试按当前目录确认原来源已消失及目标仍在，不重复删除", run.Entries[1], fs.deleted)
	}
}

func TestMediaAttachmentPendingSourceRenameIsNotCompletedDeletion(t *testing.T) {
	s, files, fs, run, sourcePath, _ := mediaAttachmentCD2Fixture(t)
	source, err := files.attachmentSource(context.Background(), run.Entries[1])
	if err != nil || source == nil {
		t.Fatal(source, err)
	}
	run.Entries[1].AttachmentKeep.Source = source
	run.Entries[1].AttachmentKeep.Phase = "recycling"
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	renamed, err := fs.Rename(context.Background(), sourcePath, "人为改名.nfo")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.executePendingAttachmentChoices(context.Background(), files, run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status == "done" || run.Entries[1].AttachmentKeep != nil || fs.deleteCalls != 0 || fs.files[renamed].ID == "" {
		t.Fatal("来源改名后不得误判回收成功或改删其他文件", run.Entries[1], fs.deleted)
	}
}

func TestMediaAttachment115KeepsExistingDifferentFile(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 0)
	source := p115.ShareFile{ID: "501", Name: "Work.S01E01(1).nfo", Size: 13}
	target := p115.ShareFile{ID: "601", Name: "Work.S01E01.nfo", Size: 27}
	drive.dirs["10"], drive.dirs["20"] = []p115.ShareFile{source}, []p115.ShareFile{target}
	run.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}
	run.Entries = []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, MediaType: "video"}, Group: "1", Relative: "Work.S01E01.mkv", Status: "done"},
		{File: OrganizeFile{ShareFile: source, ParentID: "10", SourcePath: source.Name, MediaType: "attachment"}, Group: "1", VideoID: "video", Relative: target.Name, Target: path.Join(run.Rule.Target, target.Name), Status: "ready"},
	}
	if err := files.conflict(context.Background(), run.Entries[1]); err != nil {
		t.Fatal("附件冲突应进入留一流程", err)
	}
	if err := s.prepareAttachmentChoices(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	drive.beforeDelete = func() {
		selection := run.Entries[1].AttachmentKeep
		if selection.Source.ID != source.ID || selection.Keep.ID != target.ID {
			t.Fatal("115 回收前必须核对本次来源与保留文件", run.Entries[1])
		}
	}
	if err := s.executePendingAttachmentChoices(context.Background(), files, run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "done" || len(drive.dirs["10"]) != 0 || len(drive.dirs["20"]) != 1 || drive.dirs["20"][0].ID != target.ID {
		t.Fatal("115同角色择一不能修改保留目标", run.Entries[1], drive.dirs)
	}
}

func TestMediaAttachmentCD2LateCopyUsesPersistedVideoAndTarget(t *testing.T) {
	s, _, store, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	for _, name := range []string{"Work.S01E01.mkv", "Work.S01E01.nfo", "Work.S01E01.chs.ass"} {
		fs.add(path.Join(rule.Source, name), false, 123)
	}
	first, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &first, "auto")
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	for _, name := range []string{"Work.S01E01(1).nfo", "Work.S01E01.chs(2).ass"} {
		file := fs.add(path.Join(rule.Source, name), false, 77)
		file.Modified = time.Now().Add(-time.Minute)
		fs.files[file.Path] = file
	}
	second, err := restarted.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, restarted, &second, "auto")
	references, kept := 0, 0
	for _, entry := range second.Entries {
		if entry.Reference {
			references++
			if entry.Result != nil || entry.Failure != nil || entry.AttachmentKeep != nil {
				t.Fatal("历史主视频只复制关联必需信息", entry)
			}
		} else if mediaSidecar(entry) {
			if entry.Status != "done" || entry.Result == nil || entry.Result.Action != "attachment_kept" {
				t.Fatal("主视频移走后的复制附件应复用目标并留一", entry)
			}
			kept++
		}
	}
	if references != 1 || kept != 2 || len(fs.deleted) != 2 {
		t.Fatal("晚到复制附件不能成为孤立附件", second.Entries, fs.deleted)
	}
}
