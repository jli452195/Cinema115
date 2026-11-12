package service

import (
	"context"
	"path"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMediaDescriptionNumberingContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		files    []string
		title    string
		kind     string
		episodes []int
	}{
		{"百集前后", []string{"死神Bleach 099 [原创].strm", "死神Bleach 100 [原创].strm", "死神Bleach 101 [主线].strm", "死神Bleach 203 [主线].strm", "死神Bleach 358 [主线].strm"}, "死神Bleach", "tv", []int{99, 100, 101, 203, 358}},
		{"只有后半段", []string{"Work 100 [原创].strm", "Work 101 [主线].strm"}, "Work", "tv", []int{100, 101}},
		{"明确同作品", []string{"Work.S01E099.strm", "Work 203 [主线].strm"}, "Work", "tv", []int{99, 203}},
		{"季目录", []string{"Season 01/Work 100 [原创].strm"}, "Work", "tv", []int{100}},
		{"繁体与规格", []string{"Work 100v2 【原創】 [1080p].mkv", "Work 101 [主線] [1080p].mkv"}, "Work", "tv", []int{100, 101}},
		{"续作数字", []string{"Work 2 - 100 [主线].strm"}, "Work 2", "tv", []int{100}},
		{"续作数字等于集号", []string{"Work 2.S01E02.strm"}, "Work 2", "tv", []int{2}},
		{"正式片名与集号重复", []string{"The 100.S01E100.strm"}, "The 100", "tv", []int{100}},
		{"正式片名与两位集号重复", []string{"Warehouse 13.S01E13.strm"}, "Warehouse 13", "tv", []int{13}},
		{"破折号重复编号保留歧义", []string{"Season 01/The 100 - 100 [主线].strm"}, "The 100", "tv", []int{0}},
		{"方括号片名与编号", []string{"[真正作品标题][100][主线].strm"}, "真正作品标题", "tv", []int{100}},
		{"描述词也是正式片名", []string{"[原创].S01E100.strm"}, "原创", "tv", []int{100}},
		{"孤立裸数字", []string{"Work 100 [主线].strm"}, "Work 100", "", []int{0}},
		{"数字电影名", []string{"Planet 51 [主线] [1080p].mkv"}, "Planet 51", "", []int{0}},
		{"电影年份片名", []string{"Blade Runner 2049 [1080p].mkv"}, "Blade Runner 2049", "", []int{0}},
		{"同集修订不构成证据", []string{"Work 100v2 [主线].strm", "Work 100v3 [主线].strm"}, "Work 100", "", []int{0, 0}},
		{"未知标签不删除", []string{"Work 100 [未知文字].strm"}, "Work 100 [未知文字", "tv", []int{100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := MediaOrganizeRun{Rule: MediaOrganizeRule{Source: "/inbox"}}
			entries := make([]MediaOrganizeEntry, 0, len(tc.files))
			for _, relative := range tc.files {
				file := OrganizeFile{ShareFile: p115.ShareFile{ID: relative, Name: path.Base(relative)}, SourcePath: relative, ParentID: path.Dir(relative), MediaType: "video", Season: 1}
				mediaOrganizeFileNumbering(&file)
				entries = append(entries, MediaOrganizeEntry{File: file})
			}
			mediaAppendScanEntries(&run, entries)
			for _, group := range run.Groups {
				if group.Identity.Name != tc.title || group.Identity.Kind != tc.kind {
					t.Fatalf("作品身份错误：%+v", run.Groups)
				}
			}
			for i, entry := range run.Entries {
				if entry.File.Episode != tc.episodes[i] || entry.File.Name != path.Base(tc.files[i]) {
					t.Fatalf("集号或原始名称变化：%+v", entry.File)
				}
			}
		})
	}
}

func TestMediaDescriptionDoesNotBorrowOtherWorkOrDirectory(t *testing.T) {
	for _, files := range [][]string{
		{"Other.S01E099.strm", "Work 100 [原创].strm"},
		{"a/Work 100 [主线].strm", "b/Work 101 [主线].strm"},
		{"Work 100 [主线] [MOVIE].strm", "Work 101 [主线] [MOVIE].strm"},
		{"Work 100 [主线] [tmdb-42] [tmdb-43].strm", "Work 101 [主线].strm"},
	} {
		run := MediaOrganizeRun{Rule: MediaOrganizeRule{Source: "/inbox"}}
		entries := make([]MediaOrganizeEntry, 0, len(files))
		for _, relative := range files {
			file := OrganizeFile{ShareFile: p115.ShareFile{ID: relative, Name: path.Base(relative)}, SourcePath: relative, ParentID: path.Dir(relative), MediaType: "video", Season: 1}
			mediaOrganizeFileNumbering(&file)
			entries = append(entries, MediaOrganizeEntry{File: file})
		}
		mediaAppendScanEntries(&run, entries)
		for _, group := range run.Groups {
			if group.Identity.Name == "Work" && group.Identity.Kind == "tv" {
				t.Fatalf("不能跨作品、目录、电影或冲突身份补造剧集证据：%v %+v", files, run.Groups)
			}
		}
	}
}

func TestMediaDescriptionRetryRefreshesUnmatchedIdentity(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	for _, file := range []string{"Work 100 [原创].strm", "Work 101 [主线].strm"} {
		mediaTestFile(t, rule.Source, file, "http://example.invalid/original\r\n")
	}
	metadata.err = context.DeadlineExceeded
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	// 模拟已持久化的旧失败名称，重试不能要求用户先改名或重新放回来源。
	run.Groups[0].Identity.Name = "Work 100 [原创"
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	metadata.err = nil
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "Work 100 [原创].strm"}); err != nil {
		t.Fatal(err)
	}
	run, err = s.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "retry")
	task, err := s.Task(run.ID, "Work 100 [原创].strm")
	if err != nil || task.Status != "done" || task.Episode != 100 || task.Group.TMDBID != 42 {
		t.Fatalf("旧失败未按修正片名完成：%+v %v", task, err)
	}
	other, err := s.Task(run.ID, "Work 101 [主线].strm")
	if err != nil || other.Status == "done" {
		t.Fatalf("单任务重试不得整理同批其他文件：%+v %v", other, err)
	}
}
