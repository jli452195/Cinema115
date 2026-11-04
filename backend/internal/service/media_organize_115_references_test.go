package service

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

func mediaAttachment115ReferenceFixture(t *testing.T) (*MediaOrganizer, *media115BatchTestDrive, *mediaOrganizeFiles, *MediaOrganizeRun, []MediaOrganizeEntry) {
	t.Helper()
	s, drive, files, previous := newMedia115BatchTest(t, 0)
	video := p115.ShareFile{ID: "500", Name: "Work.S01E01.mkv", Size: 123, SHA1: strings.Repeat("A", 40)}
	previous.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42, Title: "Work", ResultCode: "MATCHED"}}
	previous.Entries = []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: video, ParentID: "10", SourcePath: video.Name, MediaType: "video", Season: 1, Episode: 1}, Group: "1", Status: "done", Relative: video.Name, Target: path.Join(previous.Rule.Target, video.Name), Result: &MediaOrganizeResult{Status: "done", Action: "moved"}}}
	previous.Status = "completed"
	if err := s.checkpoint(previous); err != nil {
		t.Fatal(err)
	}
	delete(s.actions, previous.ID)
	drive.dirs["20"] = []p115.ShareFile{video}
	current, err := s.Start(previous.Rule.ID)
	if err != nil || current.ID == previous.ID {
		t.Fatal("应创建新的晚到附件任务", current.ID, err)
	}
	entries := []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "601", Name: "Work.S01E01.chs(1).ass", Size: 13}, ParentID: "10", SourcePath: "Work.S01E01.chs(1).ass", MediaType: "subtitle"}, Status: "pending"},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "602", Name: "Work.S01E01(2).nfo", Size: 19}, ParentID: "10", SourcePath: "Work.S01E01(2).nfo", MediaType: "attachment"}, Status: "pending"},
	}
	return s, drive, files, &current, entries
}

func TestMediaAttachment115LateCopyRestoresOnlyConfirmedVideo(t *testing.T) {
	s, drive, files, run, entries := mediaAttachment115ReferenceFixture(t)
	if err := s.add115References(context.Background(), files, run, entries); err != nil {
		t.Fatal(err)
	}
	if len(run.Entries) != 1 || len(run.Groups) != 1 || !run.Entries[0].Reference || run.Entries[0].Status != "done" || run.Groups[0].ResultCode != "" || run.Entries[0].Result != nil {
		t.Fatal("115历史视频只恢复已核实关联必需的信息", run.Entries, run.Groups)
	}
	if !slices.Equal(drive.readOrder, []string{"20"}) || drive.moves != 0 || drive.renames != 0 || len(drive.deleted) != 0 {
		t.Fatal("晚到复制字幕只核验对应目标目录，不扫描或移动历史主视频", drive.readOrder)
	}
	if err := s.add115References(context.Background(), files, run, entries); err != nil || len(run.Entries) != 1 {
		t.Fatal("重复补引用不能重复记录历史视频", run.Entries, err)
	}
}

func TestMediaAttachment115LateCopyCanUseConfirmedSHA1Duplicate(t *testing.T) {
	s, drive, files, run, entries := mediaAttachment115ReferenceFixture(t)
	for i := range s.doc.Runs {
		if s.doc.Runs[i].ID != run.ID {
			s.doc.Runs[i].Entries[0].Result = &MediaOrganizeResult{Action: "duplicate"}
		}
	}
	drive.dirs["20"][0].ID = "保留目标文件"
	drive.dirs["20"][0].Size++
	if err := s.add115References(context.Background(), files, run, entries); err != nil || len(run.Entries) != 1 || !run.Entries[0].Reference {
		t.Fatal("已确认SHA1去重的历史视频仍可接收晚到附件", run.Entries, err)
	}
	run.Status = "completed"
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	delete(s.actions, run.ID)
	next, err := s.Start(run.Rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.add115References(context.Background(), files, &next, entries); err != nil || len(next.Entries) != 1 || !next.Entries[0].Reference {
		t.Fatal("只读引用再次压缩后仍须保留SHA1去重语义，支持连续多轮晚到附件", next.Entries, err)
	}
}

func TestMediaAttachment115ReferenceOnlyProtectsRelatedSidecarsOnReadFailure(t *testing.T) {
	for _, scenario := range []string{"missing", "file-id", "hash", "size", "read-failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, entries := mediaAttachment115ReferenceFixture(t)
			switch scenario {
			case "missing":
				drive.dirs["20"] = nil
			case "file-id":
				drive.dirs["20"][0].ID = "替换后的文件"
			case "hash":
				drive.dirs["20"][0].SHA1 = strings.Repeat("B", 40)
			case "size":
				drive.dirs["20"][0].Size++
			case "read-failure":
				drive.beforeSnapshot = func(string) error { return errors.New("模拟读取失败") }
			}
			if err := s.add115References(context.Background(), files, run, entries); err != nil || len(run.Entries) != 0 || len(drive.deleted) != 0 {
				t.Fatal("目标不确定时不能恢复附件归属或执行回收", run.Entries, err)
			}
			for _, entry := range entries {
				protected := scenario == "read-failure"
				if (entry.Status == "retained") != protected || files.localPending[entry.File.ID] != protected || files.settlingVideos[entry.File.ParentID] {
					t.Fatal("只在目标读取异常时保护相关附件，失效历史不应保护整个目录", entry, files.localPending, files.settlingVideos)
				}
			}
		})
	}
}

func TestMediaAttachment115ReferenceAcceptsCompressedSuccessIndex(t *testing.T) {
	s, _, files, run, entries := mediaAttachment115ReferenceFixture(t)
	for i := range s.doc.Runs {
		if s.doc.Runs[i].ID != run.ID {
			entry := &s.doc.Runs[i].Entries[0]
			entry.Reference, entry.Result = true, nil
		}
	}
	if err := s.add115References(context.Background(), files, run, entries); err != nil || len(run.Entries) != 1 || !run.Entries[0].Reference {
		t.Fatal("压缩的成功索引应继续提供晚到附件所需的只读来源映射", run.Entries, err)
	}
}

func TestMediaAttachment115ReferencePreservesCancellation(t *testing.T) {
	s, _, files, run, entries := mediaAttachment115ReferenceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.add115References(ctx, files, run, entries); !errors.Is(err, context.Canceled) {
		t.Fatal("取消整轮时必须向调用方返回取消信号", err)
	}
}

func TestMediaAttachment115InvalidReferenceDoesNotBlockOtherVideo(t *testing.T) {
	s, drive, files, previous, _ := newMedia115PipelineTest(t)
	video := p115.ShareFile{ID: "500", Name: "Work.S01E01.mkv", Size: 123, SHA1: strings.Repeat("A", 40)}
	previous.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42, Title: "Work"}}
	previous.Entries = []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: video, ParentID: "10", SourcePath: video.Name, MediaType: "video", Season: 1, Episode: 1}, Group: "1", Status: "done", Relative: video.Name, Target: path.Join(previous.Rule.Target, video.Name), Reference: true}}
	previous.Status = "completed"
	if err := s.checkpoint(previous); err != nil {
		t.Fatal(err)
	}
	delete(s.actions, previous.ID)
	run, err := s.Start(previous.Rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Rule.AttachmentGraceSeconds = 0
	files.rule = run.Rule
	drive.dirs["10"] = []p115.ShareFile{{ID: "601", Name: "Work.S01E01.ass", Size: 8}, {ID: "700", Name: "Work.S01E02.mkv", Size: 123}}
	entries, err := files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run.streaming = true
	if err := s.processMediaBatch(context.Background(), files, &run, entries); err != nil {
		t.Fatal("旧目标缺失不应中断同批其他视频", err)
	}
	if err := s.prepareOrphanCleanup(files, &run); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanup(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if media115PipelineHas(drive, "10", "601") || media115PipelineHas(drive, "10", "700") || !slices.Contains(drive.deleted, "601") {
		t.Fatal("失效历史不应阻止孤立附件清理，其他视频应正常入库", run.Entries, drive.deleted)
	}
	if !slices.ContainsFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == "700" && entry.Status == "done" }) {
		t.Fatal("其他视频未正常完成", run.Entries)
	}
}

func TestMediaAttachment115ReferenceDoesNotCrossFrozenScope(t *testing.T) {
	for _, scenario := range []string{"account", "source-id", "target-id", "source-path", "target-path", "rule", "unrelated-name"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, entries := mediaAttachment115ReferenceFixture(t)
			switch scenario {
			case "account":
				run.Rule.AccountID += "-other"
			case "source-id":
				run.Rule.SourceID += "-other"
			case "target-id":
				run.Rule.TargetID += "-other"
			case "source-path":
				run.Rule.Source += "/other"
			case "target-path":
				run.Rule.Target += "/other"
			case "rule":
				run.Rule.ID += "-other"
			case "unrelated-name":
				entries = []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "999", Name: "Other.S01E01.ass", Size: 9}, ParentID: "10", SourcePath: "Other.S01E01.ass", MediaType: "subtitle"}}}
			}
			if err := s.add115References(context.Background(), files, run, entries); err != nil || len(run.Entries) != 0 || len(drive.readOrder) != 0 {
				t.Fatal("历史引用必须限定同一冻结规则与实际附件关系", run.Entries, drive.readOrder, err)
			}
		})
	}
}
