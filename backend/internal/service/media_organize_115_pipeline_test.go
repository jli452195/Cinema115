package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

type media115PipelineDrive struct {
	*media115ExecutionDrive
	listed          []string
	beforeDirectory func(string) error
	directoryID     map[string]string
	partialError    error
	paginated       bool
}

func (d *media115PipelineDrive) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	id, err := d.media115ExecutionDrive.CreateDirectory(ctx, parent, name, cookie)
	if err == nil {
		d.paths[id] = path.Join(d.paths[parent], name)
	}
	return id, err
}

func (d *media115PipelineDrive) ReadDirectoryFiles(ctx context.Context, id, cookie string) (p115.DriveDirectory, []p115.ShareFile, error) {
	d.listed = append(d.listed, id)
	if d.beforeDirectory != nil {
		if err := d.beforeDirectory(id); err != nil {
			return p115.DriveDirectory{}, nil, err
		}
	}
	var directory p115.DriveDirectory
	var files []p115.ShareFile
	var err error
	if d.paginated {
		directory, err = d.ReadDirectory(ctx, id, cookie)
		if err == nil {
			files, err = listOrganizeFiles(ctx, d, id, cookie)
		}
	} else {
		directory, files, err = d.media115ExecutionDrive.ReadDirectoryFiles(ctx, id, cookie)
	}
	if alternate, ok := d.directoryID[id]; ok {
		directory.CID = alternate
	}
	if d.partialError != nil && id == "10" {
		return directory, files, d.partialError
	}
	return directory, files, err
}

type media115PipelineMetadata struct {
	mediaRecognitionFake
	details int
}

func (m *media115PipelineMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	m.details++
	return m.mediaRecognitionFake.Details(ctx, kind, id)
}

func newMedia115PipelineTest(t *testing.T) (*MediaOrganizer, *media115PipelineDrive, *mediaOrganizeFiles, *MediaOrganizeRun, *media115PipelineMetadata) {
	t.Helper()
	s, base, files, run := newMedia115ExecutionTest(t)
	drive := &media115PipelineDrive{media115ExecutionDrive: base}
	metadata := &media115PipelineMetadata{mediaRecognitionFake: mediaRecognitionFake{items: []tmdb.Item{{ID: 42, Kind: "tv", Title: "作品", OriginalTitle: "Work", Date: "2024-01-01"}}}}
	s.drive, files.drive, s.metadata = drive, drive, metadata
	return s, drive, files, run, metadata
}

func media115PipelineHas(d *media115PipelineDrive, parent, id string) bool {
	return slices.ContainsFunc(d.dirs[parent], func(file p115.ShareFile) bool { return file.ID == id })
}

func TestMedia115PipelineMovesCompleteDirectoryBeforeReadingChildren(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	previousScan := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	s.doc.Rules[0].LastScan = previousScan
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, {ID: "502", Name: "Work.S01E01.zh.ass", Size: 8}, {ID: "30", Name: "later", Directory: true}}
	drive.paths["30"] = "/inbox/later"
	drive.dirs["30"] = []p115.ShareFile{{ID: "503", Name: "Work.S01E02.mkv", Size: 123}}
	checked := false
	drive.beforeDirectory = func(id string) error {
		if id == "30" {
			checked = true
			if media115PipelineHas(drive, "10", "501") || media115PipelineHas(drive, "10", "502") {
				t.Fatal("读取子目录前，同目录的视频与字幕应已整理")
			}
		}
		return nil
	}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if !checked || run.Status != "completed" || len(run.Entries) != 3 || files.cloudEmpty == nil || !files.cloudEmpty.complete {
		t.Fatal("逐目录流水线结果不完整", checked, run.Status, run.Entries)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" || entry.Relative == "" {
			t.Fatal("完整目录中的媒体未完成整理", entry)
		}
	}
	if !s.doc.Rules[0].LastScan.After(previousScan) {
		t.Fatal("完整扫描后应更新最近扫描时间")
	}
}

func TestMedia115PipelineRejectsIncompleteOrChangedDirectoryBeforeMoving(t *testing.T) {
	for _, scenario := range []string{"partial-page", "changed-id", "changed-path", "target-child", "failure-child"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, _ := newMedia115PipelineTest(t)
			previousScan := s.doc.Rules[0].LastScan
			drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}}
			switch scenario {
			case "partial-page":
				drive.partialError = errors.New("模拟最后一页失败")
			case "changed-id":
				drive.directoryID = map[string]string{"10": "999"}
			case "changed-path":
				drive.paths["10"] = "/outside/inbox"
			case "target-child":
				drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: run.Rule.TargetID, Name: "library", Directory: true})
			case "failure-child":
				files.rule.FailureID, run.Rule.FailureID = "40", "40"
				drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: "40", Name: "failed", Directory: true})
			}
			if err := s.process115Auto(context.Background(), files, run); err == nil {
				t.Fatal("不完整或身份变化的目录应停止整理")
			}
			if drive.moves != 0 || drive.renames != 0 || len(drive.deleted) != 0 || !media115PipelineHas(drive, "10", "501") {
				t.Fatal("当前目录核验成功前不允许写入", drive.moves, drive.renames, drive.deleted)
			}
			if files.cloudEmpty != nil && files.cloudEmpty.complete || run.streaming || files.shallow115 || !s.doc.Rules[0].LastScan.Equal(previousScan) {
				t.Fatal("失败扫描不能留下完整证明或更新最近成功扫描时间")
			}
		})
	}
}

func TestMedia115PipelineLaterFailureKeepsEarlierMovesWithoutCleanup(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, {ID: "502", Name: "advertisement.zip", Size: 8}, {ID: "503", Name: "poster.png", Size: 8}, {ID: "30", Name: "broken", Directory: true}, {ID: "31", Name: "empty", Directory: true}}
	drive.paths["30"], drive.paths["31"] = "/inbox/broken", "/inbox/empty"
	drive.dirs["30"], drive.dirs["31"] = []p115.ShareFile{}, []p115.ShareFile{}
	drive.beforeDirectory = func(id string) error {
		if id == "30" {
			return errors.New("模拟后续目录读取失败")
		}
		return nil
	}
	if err := s.process115Auto(context.Background(), files, run); err == nil {
		t.Fatal("后续目录失败应返回错误")
	}
	if err := s.cleanupEmptyDirectories(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if media115PipelineHas(drive, "10", "501") || !slices.ContainsFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == "501" && entry.Status == "done" }) {
		t.Fatal("后续扫描失败不能丢弃之前已完成的整理")
	}
	if len(drive.deleted) != 0 || !media115PipelineHas(drive, "10", "502") || !media115PipelineHas(drive, "10", "503") || files.cloudEmpty.complete || len(files.directories) != 0 {
		t.Fatal("不完整扫描不得处理共享附件、孤立文件或空目录", drive.deleted)
	}
}

func TestMedia115PipelineSharedAttachmentsWaitAndUseEarlierVideo(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, {ID: "502", Name: "poster.png", Size: 8}, {ID: "503", Name: "tvshow.nfo", Size: 8}, {ID: "30", Name: ".actors", Directory: true}}
	drive.paths["30"] = "/inbox/.actors"
	drive.dirs["30"] = []p115.ShareFile{{ID: "504", Name: "Actor.jpg", Size: 8}}
	checked := false
	drive.beforeDirectory = func(id string) error {
		if id == "30" && !checked {
			checked = true
			if media115PipelineHas(drive, "10", "501") || !media115PipelineHas(drive, "10", "502") || !media115PipelineHas(drive, "10", "503") {
				t.Fatal("视频应立即移动，共享附件应等待全部目录读取")
			}
		}
		return nil
	}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" || len(run.Entries) != 4 || len(drive.deleted) != 0 {
		t.Fatal("共享附件未完整跟随本轮已移动的视频", run.Entries, drive.deleted)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" || entry.File.MediaType == "attachment" && entry.VideoID != "501" {
			t.Fatal("共享附件缺少已完成视频的归属", entry)
		}
		if entry.File.ID == "504" && !strings.Contains(entry.Relative, "/.actors/") {
			t.Fatal("演员图片应保留附件目录", entry.Relative)
		}
	}
}

func TestMedia115PipelineTemporaryCompanionsHoldVideo(t *testing.T) {
	for _, scenario := range []string{"subtitle", "video"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, _ := newMedia115PipelineTest(t)
			video, subtitle := "Work.S01E01.mkv", "Work.S01E01.zh.ass"
			if scenario == "subtitle" {
				subtitle += ".part"
			} else {
				video += ".part"
			}
			drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: video, Size: 123}, {ID: "502", Name: subtitle, Size: 8}, {ID: "503", Name: "tvshow.nfo", Size: 8}}
			if err := s.process115Auto(context.Background(), files, run); err != nil {
				t.Fatal(err)
			}
			if drive.moves != 0 || drive.renames != 0 || len(drive.deleted) != 0 || !files.settlingVideos["10"] || len(files.localPending) != 1 {
				t.Fatal("临时视频或字幕应保护同批视频及共享附件", run.Entries, drive.deleted)
			}
		})
	}
}

func TestMedia115PipelineWaitsForFinalPageContainingSubtitle(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	drive.paginated, drive.pageSize = true, 100
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}}
	for i := range 99 {
		drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: fmt.Sprint(600 + i), Name: fmt.Sprintf("keep-%d.iso", i), Size: 123})
	}
	drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: "502", Name: "Work.S01E01.zh.ass", Size: 8})
	finalPageRead := false
	drive.beforeRead = func(id string, offset int) error {
		if id == "10" && offset == 100 && !finalPageRead {
			if drive.moves != 0 || drive.renames != 0 {
				t.Fatal("读到最后一页前不得修改当前目录")
			}
			finalPageRead = true
		}
		return nil
	}
	drive.beforeRename = func() {
		if !finalPageRead {
			t.Fatal("最后一页字幕未纳入计划前不得改名")
		}
	}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if !finalPageRead || run.Status != "completed" || len(run.Entries) != 2 || len(drive.dirs["10"]) != 99 || len(drive.deleted) != 0 {
		t.Fatal("末页字幕应与视频同时整理，其他保留文件不受影响", run.Entries, drive.deleted)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" || entry.File.MediaType == "subtitle" && entry.VideoID != "501" {
			t.Fatal("最后一页字幕未跟随视频", entry)
		}
	}
}

func TestMedia115PipelineSharedPosterIncludesLaterDifferentWork(t *testing.T) {
	s, drive, files, run, metadata := newMedia115PipelineTest(t)
	metadata.items = append(metadata.items, tmdb.Item{ID: 43, Kind: "tv", Title: "Other", OriginalTitle: "Other", Date: "2024-01-01"})
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, {ID: "502", Name: "poster.png", Size: 8}, {ID: "30", Name: "later", Directory: true}}
	drive.paths["30"] = "/inbox/later"
	drive.dirs["30"] = []p115.ShareFile{{ID: "503", Name: "Other.S01E02.mkv", Size: 123}}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if !media115PipelineHas(drive, "10", "502") || len(drive.deleted) != 0 || len(run.Entries) != 3 {
		t.Fatal("来源含不同作品时共享海报必须保留", run.Entries, drive.deleted)
	}
	for _, entry := range run.Entries {
		if entry.File.ID == "502" {
			if entry.Status != "retained" || entry.VideoID != "" || entry.Target != "" || entry.Relative != "" {
				t.Fatal("共享海报不能提前归属第一个视频", entry)
			}
		} else if entry.Status != "done" {
			t.Fatal("共享海报归属不明不应阻止两个作品整理", entry)
		}
	}
}

func TestMedia115PipelineReusesRecognitionAndNamingWithinGroup(t *testing.T) {
	s, drive, files, run, metadata := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, {ID: "30", Name: "Season 01", Directory: true}}
	drive.paths["30"] = "/inbox/Season 01"
	drive.dirs["30"] = []p115.ShareFile{{ID: "502", Name: "Work.S01E02.mkv", Size: 123}, {ID: "503", Name: "tvshow.nfo", Size: 8}}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if len(run.Groups) != 1 || metadata.browseCalls != 1 || metadata.details != 2 || run.Status != "completed" {
		t.Fatal("同作品同季的后续目录应复用识别与命名结果", run.Groups, metadata.browseCalls, metadata.details, len(files.naming), run.Entries)
	}
}

func TestMedia115PipelineOrphanGraceSurvivesRuns(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	run.Rule.AttachmentGraceSeconds = 60
	files.rule.AttachmentGraceSeconds = 60
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "missing.zh.ass", Size: 8}}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if len(drive.deleted) != 0 || len(run.Entries) != 1 || run.Entries[0].Status != "retained" {
		t.Fatal("首次发现的孤立字幕应进入宽限", run.Entries, drive.deleted)
	}
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	second, err := s.Start(run.Rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	second.Rule.AttachmentGraceSeconds = 60
	nextFiles, err := s.files(context.Background(), second.Rule)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.process115Auto(context.Background(), nextFiles, &second); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(drive.deleted, []string{"501"}) || len(drive.dirs["10"]) != 0 {
		t.Fatal("超过自定义宽限且仍无视频时应清理字幕", second.Entries, drive.deleted)
	}
}

func TestMedia115PipelineProtectsEarlierUnconfirmedTaskWithoutBlockingNewVideo(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "已改名.S01E01.mkv", Size: 123}, {ID: "502", Name: "Work.S01E01.zh.ass", Size: 8}, {ID: "503", Name: "Work.S01E02.mkv", Size: 123}}
	old := MediaOrganizeRun{ID: "old-unconfirmed", Rule: run.Rule, Status: "partial", Entries: []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, ParentID: "10", SourcePath: "Work.S01E01.mkv", MediaType: "video"}, Status: "error", P115Move: &Media115MoveIntent{FileID: "501", Phase: "rename"}},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "502", Name: "Work.S01E01.zh.ass", Size: 8}, ParentID: "10", SourcePath: "Work.S01E01.zh.ass", MediaType: "subtitle"}, VideoID: "501", Status: "ready"},
	}}
	s.doc.Runs = append(s.doc.Runs, old)
	state := mediaOrphanBinding(run.Rule)
	state.Files = map[string]mediaOrphan{"502": {Since: time.Now().Add(-24 * time.Hour), FileID: "502", Size: 8}}
	s.doc.Orphans = map[string]mediaOrphanState{run.Rule.ID: state}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if !media115PipelineHas(drive, "10", "501") || !media115PipelineHas(drive, "10", "502") || len(drive.deleted) != 0 {
		t.Fatal("旧未确认任务的来源和过期字幕不能被新扫描改写或删除", drive.deleted)
	}
	if len(run.Entries) != 1 || run.Entries[0].File.ID != "503" || run.Entries[0].Status != "done" || !files.localPending["501"] || !files.localPending["502"] {
		t.Fatal("保护旧任务不应阻止新视频立即整理", run.Entries, files.localPending)
	}
	for _, batch := range drive.renameBatches {
		for _, entry := range batch {
			if entry.ID == "501" || entry.ID == "502" {
				t.Fatal("旧任务不应参与新的改名写入", batch)
			}
		}
	}
}

func TestMedia115PipelineUnconfirmedMovedVideoProtectsLateSubtitleInOriginalDirectory(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "504", Name: "Work.S01E01.zh.ass", Size: 8}, {ID: "503", Name: "Work.S01E02.mkv", Size: 123}}
	drive.dirs["20"] = []p115.ShareFile{{ID: "501", Name: "已移走.S01E01.mkv", Size: 123}}
	old := MediaOrganizeRun{ID: "old-moved-unconfirmed", Rule: run.Rule, Status: "partial", Entries: []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, ParentID: "10", SourcePath: "Work.S01E01.mkv", MediaType: "video"}, Status: "error", P115Move: &Media115MoveIntent{FileID: "501", Phase: "move"}},
	}}
	s.doc.Runs = append(s.doc.Runs, old)
	state := mediaOrphanBinding(run.Rule)
	state.Files = map[string]mediaOrphan{"504": {Since: time.Now().Add(-24 * time.Hour), FileID: "504", Size: 8}}
	s.doc.Orphans = map[string]mediaOrphanState{run.Rule.ID: state}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if !media115PipelineHas(drive, "10", "504") || slices.Contains(drive.deleted, "504") || !files.settlingVideos["10"] {
		t.Fatal("已移走但尚未核实的视频，其原目录晚到字幕仍需保留", run.Entries, drive.deleted)
	}
	if media115PipelineHas(drive, "10", "503") || !slices.ContainsFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == "503" && entry.Status == "done" }) {
		t.Fatal("旧任务原目录的附件保护不能阻止新视频整理", run.Entries)
	}
}

func TestMedia115PipelineUnconfirmedProtectionSurvivesTargetAndRuleChanges(t *testing.T) {
	for _, scenario := range []string{"new-target", "overlapping-rule", "different-account"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, _ := newMedia115PipelineTest(t)
			drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}}
			old := MediaOrganizeRun{ID: "old-unconfirmed", Rule: run.Rule, Status: "partial", Entries: []MediaOrganizeEntry{
				{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, ParentID: "10", SourcePath: "Work.S01E01.mkv", MediaType: "video"}, Status: "error", P115Move: &Media115MoveIntent{FileID: "501", Phase: "rename"}},
			}}
			switch scenario {
			case "new-target":
				run.Rule.TargetID, run.Rule.Target = "21", "/new-library"
				files.rule, s.doc.Rules[0] = run.Rule, run.Rule
				drive.paths["21"], drive.dirs["21"] = run.Rule.Target, []p115.ShareFile{}
			case "overlapping-rule":
				old.Rule.ID, old.Rule.SourceID, old.Rule.Source = "other-rule", "99", "/higher-source"
			case "different-account":
				old.Rule.AccountID = "another-account"
			}
			s.doc.Runs = append(s.doc.Runs, old)
			if err := s.process115Auto(context.Background(), files, run); err != nil {
				t.Fatal(err)
			}
			if scenario == "different-account" {
				if media115PipelineHas(drive, "10", "501") || len(run.Entries) != 1 || run.Entries[0].Status != "done" || files.pending115["501"] {
					t.Fatal("不同账号的文件标识不能传播保护", run.Entries, files.pending115)
				}
			} else if !media115PipelineHas(drive, "10", "501") || drive.moves != 0 || drive.renames != 0 || !files.pending115["501"] || !files.settlingVideos["10"] {
				t.Fatal("同账号目标或规则变化不能解除旧未确认写入保护", run.Entries, files.pending115)
			}
		})
	}
}

func TestMedia115PipelineRejectsDirectoryCycleAndLimits(t *testing.T) {
	for _, scenario := range []string{"cycle", "depth", "directories", "files"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, _ := newMedia115PipelineTest(t)
			switch scenario {
			case "cycle":
				drive.dirs["10"] = []p115.ShareFile{{ID: "10", Name: "cycle", Directory: true}}
			case "depth":
				parent, prefix := "10", "/inbox"
				for i := range 13 {
					id := fmt.Sprint(500 + i)
					name := fmt.Sprintf("dir-%d", i)
					drive.dirs[parent] = []p115.ShareFile{{ID: id, Name: name, Directory: true}}
					prefix = path.Join(prefix, name)
					drive.paths[id], drive.dirs[id] = prefix, []p115.ShareFile{}
					parent = id
				}
			case "directories":
				for i := range 300 {
					id, name := fmt.Sprint(500+i), fmt.Sprintf("dir-%d", i)
					drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: id, Name: name, Directory: true})
					drive.paths[id], drive.dirs[id] = path.Join("/inbox", name), []p115.ShareFile{}
				}
			case "files":
				for i := range 5001 {
					drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: fmt.Sprint(500 + i), Name: "keep.iso", Size: 123})
				}
			}
			if err := s.process115Auto(context.Background(), files, run); err == nil {
				t.Fatal("超出扫描范围应停止")
			}
			if len(drive.deleted) != 0 || files.cloudEmpty == nil || files.cloudEmpty.complete || len(files.directories) != 0 {
				t.Fatal("扫描限制失败不能给出删除证明", drive.deleted)
			}
		})
	}
}
