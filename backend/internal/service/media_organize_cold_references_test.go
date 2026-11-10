package service

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"slices"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

// 使用隔离 SQLite 验证冷历史，不连接真实网盘，不修改工作区运行数据库。
func persistMediaColdFixture(t *testing.T, s *MediaOrganizer, runs ...MediaOrganizeRun) (*storage.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "cold-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	settings, err := json.Marshal(mediaOrganizeSettings{Rules: s.doc.Rules, Orphans: s.doc.Orphans})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]storage.MediaOrganizeRunRow, 0, len(runs))
	for _, run := range runs {
		row, err := mediaRunRow(run)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := db.SaveMediaOrganize(context.Background(), string(settings), rows, nil); err != nil {
		t.Fatal(err)
	}
	s.db = db
	return db, dir
}

func TestMediaCold115ReferencesPreserveScopeAndTargetIdentity(t *testing.T) {
	for _, scenario := range []string{"confirmed", "changed-account", "changed-target", "changed-file"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, entries := mediaAttachment115ReferenceFixture(t)
			video := drive.dirs["20"][0]
			old := MediaOrganizeRun{ID: "cold-success", Rule: run.Rule, Status: "completed", Groups: []MediaOrganizeGroup{{ID: "work", Kind: "tv", TMDBID: 42, Title: "Work"}}, Entries: []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: video, ParentID: "10", SourcePath: video.Name, MediaType: "video", Season: 1, Episode: 1}, Group: "work", Status: "done", Relative: video.Name, Target: path.Join(run.Rule.Target, video.Name), Result: &MediaOrganizeResult{Status: "done", Action: "moved"}}}}
			persistMediaColdFixture(t, s, old)
			s.doc.Runs = []MediaOrganizeRun{*run}
			switch scenario {
			case "changed-account":
				run.Rule.AccountID += "-other"
			case "changed-target":
				run.Rule.TargetID += "-other"
			case "changed-file":
				drive.dirs["20"][0].ID = "replacement"
			}
			if err := s.add115References(context.Background(), files, run, entries); err != nil {
				t.Fatal(err)
			}
			if (len(run.Entries) == 1 && run.Entries[0].Reference) != (scenario == "confirmed") {
				t.Fatal("冷历史必须保留冻结来源和目标身份核验", run.Entries)
			}
			if len(s.doc.Runs) != 1 || s.doc.Runs[0].ID != run.ID || drive.moves != 0 || drive.renames != 0 || len(drive.deleted) != 0 {
				t.Fatal("只读查询不能常驻冷历史或改动历史文件", s.doc.Runs)
			}
		})
	}
}

func TestMediaColdCD2LateSubtitleAfterRestart(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	first, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &first, "auto")
	db, dir := persistMediaColdFixture(t, s, first)
	restarted, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.doc.Runs) != 0 {
		t.Fatal("完成历史不应在启动后常驻内存", len(restarted.doc.Runs))
	}
	restarted.cd2 = connection
	late := fs.add(path.Join(rule.Source, "Work.S01E01.zh.ass"), false, 8)
	second, err := restarted.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, restarted, &second, "auto")
	if _, exists := fs.files[late.Path]; exists || slices.Contains(fs.deleted, late.Path) {
		t.Fatal("冷历史中的视频应继续接收晚到字幕", second.Entries)
	}
	if !slices.ContainsFunc(second.Entries, func(entry MediaOrganizeEntry) bool { return entry.Reference && entry.File.MediaType == "video" }) || !slices.ContainsFunc(second.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.Name == late.Name && entry.Status == "done" }) {
		t.Fatal("晚到字幕缺少成功结果或已核实视频引用", second.Entries)
	}
}

func TestMediaCold115RootArtworkScopeIncludesNestedVideos(t *testing.T) {
	s, drive, files, run, _ := mediaAttachment115ReferenceFixture(t)
	video := drive.dirs["20"][0]
	old := MediaOrganizeRun{ID: "cold-nested-video", Rule: run.Rule, Status: "completed", Groups: []MediaOrganizeGroup{{ID: "work", Kind: "tv", TMDBID: 42, Title: "Work"}}, Entries: []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: video, ParentID: "nested-source", SourcePath: "video/" + video.Name, MediaType: "video", Season: 1, Episode: 1}, Group: "work", Status: "done", Relative: video.Name, Target: path.Join(run.Rule.Target, video.Name), Result: &MediaOrganizeResult{Status: "done", Action: "moved"}}}}
	persistMediaColdFixture(t, s, old)
	s.doc.Runs = []MediaOrganizeRun{*run}
	// 根目录的 .actors 以点号表示共享范围，必须覆盖其下不同目录中的成功视频。
	entries := []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "root-art", Name: "actor.jpg", Size: 8}, ParentID: "actors-folder", SourcePath: ".actors/actor.jpg", MediaType: "attachment"}, Status: "pending"}}
	run.streaming = true
	if err := s.add115References(context.Background(), files, run, entries); err != nil {
		t.Fatal(err)
	}
	if len(run.Entries) != 1 || !run.Entries[0].Reference || run.Entries[0].File.ID != video.ID || !slices.Equal(drive.readOrder, []string{"20"}) {
		t.Fatal("根共享附件应按点号范围找到并核实冷存储中的嵌套视频", run.Entries, drive.readOrder)
	}
}

func TestMediaColdPending115MovesProtectOnlyFrozenAccount(t *testing.T) {
	s, _, _, current, _ := newMedia115PipelineTest(t)
	old := MediaOrganizeRun{ID: "cold-pending", Rule: current.Rule, Status: "failed", Entries: []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.mkv"}, ParentID: "original-parent", SourcePath: "Work.mkv", MediaType: "video"}, Status: "error", P115Move: &Media115MoveIntent{}},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "subtitle", Name: "Work.ass"}, SourcePath: "Work.ass", MediaType: "subtitle"}, VideoID: "video", Status: "pending"},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "unrelated", Name: "Other.ass"}, SourcePath: "Other.ass", MediaType: "subtitle"}, Status: "pending"},
	}}
	persistMediaColdFixture(t, s, old)
	s.doc.Runs = []MediaOrganizeRun{*current}
	pending, directories, err := s.pending115Tasks(context.Background(), current)
	if err != nil || !pending["video"] || !pending["subtitle"] || pending["unrelated"] || !directories["original-parent"] {
		t.Fatal("冷存储中的待核实移动应保护对应主文件及附件", pending, directories, err)
	}
	current.Rule.AccountID += "-other"
	pending, directories, err = s.pending115Tasks(context.Background(), current)
	if err != nil || len(pending) != 0 || len(directories) != 0 {
		t.Fatal("未核实移动不能跨账号保护目录", pending, directories, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.pending115Tasks(ctx, current); !errors.Is(err, context.Canceled) {
		t.Fatal("读取冷证据被取消时必须中止当前流程", err)
	}
}
