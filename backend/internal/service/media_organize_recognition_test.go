package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
)

func TestMediaOrganizerRecognitionQueryFailureKeepsSources(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	rule.Failure = t.TempDir()
	var err error
	rule, err = s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	metadata.err = context.DeadlineExceeded
	for _, name := range []string{"Work.S01E01.mkv", "Work.S01E01.ass"} {
		mediaTestFile(t, rule.Source, name, "原文件")
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if len(run.Groups) != 1 || run.Groups[0].ResultCode != "METADATA_QUERY_FAILED" {
		t.Fatal("查询异常未单独分类", run)
	}
	for _, entry := range run.Entries {
		if entry.File.MediaType == "cleanup" {
			continue
		}
		if entry.Failure != nil {
			t.Fatal("查询异常不应移入失败目录", entry)
		}
		if _, err := os.Stat(filepath.Join(rule.Source, entry.File.Name)); err != nil {
			t.Fatal("查询异常后来源未保留", entry, err)
		}
	}
}

func TestMediaRecognitionLogsPersistAndDetach(t *testing.T) {
	logs := &GenerationLogs{}
	dir := t.TempDir()
	if err := logs.EnablePersistence(dir); err != nil {
		t.Fatal(err)
	}
	details := MediaRecognitionLogDetails{RunID: "demo-run", GroupID: "1", FileID: "demo-file", Status: "failed", ResultCode: "MATCH_NOT_FOUND"}
	logs.appendRecognition("测试整理", "WARNING", "未找到对应作品", details)
	view := logs.Read("ALL", "整理", 0, false)
	if len(view.Entries) != 1 || view.Entries[0].Recognition == nil {
		t.Fatal(view)
	}
	view.Entries[0].Recognition.ResultCode = "外部修改"
	if logs.Read("ALL", "整理", 0, false).Entries[0].Recognition.ResultCode != details.ResultCode {
		t.Fatal("日志上下文未脱离共享数据")
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	restored := &GenerationLogs{}
	if err := restored.EnablePersistence(dir); err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	view = restored.Read("ALL", "整理", 0, false)
	if len(view.Entries) != 1 || view.Entries[0].Recognition == nil || *view.Entries[0].Recognition != details || view.Entries[0].Details != nil {
		t.Fatal("持久化日志丢失识别上下文", view)
	}
}

func TestMediaRecognitionGroupKeyIgnoresProvenance(t *testing.T) {
	left := mediafacts.Identity{Name: "作品", NameSource: "filename", Season: 1}
	right := left
	right.NameSource, right.YearSource = "parent:1", "parent:2"
	if mediaGroupIdentityKey(left) != mediaGroupIdentityKey(right) {
		t.Fatal("字段来源不应拆分相同作品声明")
	}
}

func TestMediaRecognitionPartialSeasonAndRevisions(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	item := tmdb.Item{ID: 42, Kind: "tv", Title: "Show", Date: "2024-01-01"}
	s.metadata = &mediaCollectionMetadata{items: []tmdb.Item{item}, details: map[int]*tmdb.Detail{42: {Item: item}}}
	for _, name := range []string{"Show 10v2 [BD 1080p].mkv", "Show 11 [BD 1080p].mkv", "Show 24 END [BD 1080p].mkv"} {
		mediaTestFile(t, rule.Source, name, "原文件")
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if len(run.Groups) != 1 || len(run.Entries) != 3 || run.Groups[0].TMDBID != 42 {
		t.Fatal("半季包和修订文件应复用同一作品", run)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" || entry.File.Episode != 10 && entry.File.Episode != 11 && entry.File.Episode != 24 {
			t.Fatal("连续编号或完结标记解析失败", entry)
		}
	}
}

func TestMediaOrganizerResultsPersistWithoutProcessSnapshots(t *testing.T) {
	for _, tc := range []struct{ name, title, kind string }{
		{"三体.S01E01.mkv", "三体", "tv"},
		{"Planet 51 [1080p].mkv", "Planet 51", "movie"},
		{"Blade Runner 2049.mkv", "Blade Runner 2049", "movie"},
		{"北境往事 (2026) - S01E13 - 2160p.WEB-DL.H265.DDP 2.0.strm", "北境往事", "tv"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			s, db, dir := newMediaSQLiteTest(t)
			item := tmdb.Item{ID: 318902, Kind: tc.kind, Title: tc.title, Date: "2026-01-01"}
			metadata := &mediaCollectionMetadata{items: []tmdb.Item{item}, details: map[int]*tmdb.Detail{item.ID: {Item: item}}}
			s.metadata, s.Logs = metadata, &GenerationLogs{}
			rule, err := s.SaveRule(MediaOrganizeRule{Name: "结果验收", Storage: "local", Source: t.TempDir(), Target: t.TempDir(), IntervalSeconds: 300})
			if err != nil {
				t.Fatal(err)
			}
			mediaTestFile(t, rule.Source, tc.name, "原文件内容")
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "auto")
			if len(run.Entries) != 1 || run.Entries[0].Status != "done" || run.Groups[0].TMDBID != item.ID || run.Groups[0].Year != "2026" {
				t.Fatal(run)
			}
			if strings.HasPrefix(tc.name, "北境往事") {
				v := run.Entries[0].File.NamingValues
				if v[naming.Source] != "WEB-DL" || v[naming.VideoCodec] != "HEVC" || v[naming.AudioCodec] != "DDP" || v[naming.Channels] != "2.0" || v[naming.Platform] != "" {
					t.Fatal(v)
				}
			}
			data, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{`"candidates"`, `"queries"`, `"stages"`, `"recognitionHistory"`, `"parsed"`, `"input"`, `"nameSource"`, `"yearSource"`, `"seasonSource"`} {
				if strings.Contains(string(data), field) {
					t.Fatalf("不应保存过程字段 %s", field)
				}
			}
			if logs := s.Logs.Read("ALL", "整理", 0, false).Entries; len(logs) != 0 {
				t.Fatal("成功整理不应产生失败日志", logs)
			}
			queries := len(metadata.queries)
			restored, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", metadata, s.templates, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if task, err := restored.Task(run.ID, run.Entries[0].File.ID); err != nil || task.Status != "done" || !task.At.Equal(run.Entries[0].Result.At) {
				t.Fatal("重启后应保留成功任务及原完成时间", task, err)
			}
			index, err := restored.Snapshot(run.ID)
			if err != nil || len(index.Entries) != 1 || !index.Entries[0].Reference || index.Entries[0].Relative != run.Entries[0].Relative || index.Entries[0].Target != run.Entries[0].Target || index.Entries[0].File.NamingValues != nil || len(metadata.queries) != queries {
				t.Fatal("成功后只应持久来源目标映射，查看映射不能重新查询元数据", index, err)
			}
			index.Entries[0].File.Name, index.Groups[0].Title = "外部修改", "外部修改"
			again, err := restored.Snapshot(run.ID)
			if err != nil || again.Entries[0].File.Name == "外部修改" || again.Groups[0].Title == "外部修改" {
				t.Fatal("只读映射快照没有隔离可变字段", again, err)
			}
		})
	}
}

func TestMediaResultCheckpointDoesNotRefreshUnchangedFiles(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	s.Logs = &GenerationLogs{}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Entries = []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "a", Name: "A.S01E01.mkv"}, MediaType: "video"}, Status: "needs_match", Message: "请选择作品"},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "b", Name: "B.S01E01.mkv"}, MediaType: "video"}, Status: "needs_match", Message: "请选择作品"},
	}
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	first := mediaTasks(run)
	logCount := len(s.Logs.Read("ALL", "整理", 0, false).Entries)
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	second := mediaTasks(run)
	if !first[0].At.Equal(second[0].At) || !first[1].At.Equal(second[1].At) || len(s.Logs.Read("ALL", "整理", 0, false).Entries) != logCount {
		t.Fatal("未变化文件被刷新或重复记日志")
	}
	run.Entries[0].Status, run.Entries[0].Message = "conflict", "目标已存在"
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	third := mediaTasks(run)
	if !third[1].At.Equal(first[1].At) || !third[0].At.Equal(run.UpdatedAt) {
		t.Fatalf("单文件变化污染其他任务时间：first=%v,%v third=%v,%v", first[0].At, first[1].At, third[0].At, third[1].At)
	}
	logCount = len(s.Logs.Read("ALL", "整理", 0, false).Entries)
	run.Entries[0].Status = "done"
	store.err = errors.New("测试保存失败")
	if err := s.checkpoint(&run); err == nil {
		t.Fatal("预期保存失败")
	}
	if len(s.Logs.Read("ALL", "整理", 0, false).Entries) != logCount {
		t.Fatal("保存失败却发布成功结果")
	}
	if len(run.recognitionEvents) != 0 {
		t.Fatal("保存失败残留待发布日志")
	}
	store.err = nil
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	if logs := s.Logs.Read("ALL", "整理", 0, false).Entries; len(logs) != 0 {
		t.Fatal("处理中不应输出中间失败或成功日志", logs)
	}
	run.Status = "partial"
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	logs := s.Logs.Read("ALL", "整理", 0, false).Entries
	if len(logs) != 1 || logs[0].Recognition.FileID != "b" || logs[0].Recognition.ResultCode != "MATCH_NOT_FOUND" {
		t.Fatal("结束时只应记录尚未匹配的文件", logs)
	}
	if err := s.checkpoint(&run); err != nil || len(s.Logs.Read("ALL", "整理", 0, false).Entries) != 1 {
		t.Fatal("重复保存不应重复输出最终失败", err)
	}
}

func TestMediaResultTimestampFollowsOnlyChangedEntry(t *testing.T) {
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	previous := MediaOrganizeRun{Status: "running", UpdatedAt: at, Entries: []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "a"}}, Status: "ready", UpdatedAt: at},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "b"}}, Status: "ready", UpdatedAt: at},
	}}
	var current MediaOrganizeRun
	cloneMediaRun(previous, &current)
	current.UpdatedAt, current.Entries[0].Status = at.Add(time.Second), "done"
	mediaCheckpointResults(&current, previous)
	if !current.Entries[0].UpdatedAt.Equal(current.UpdatedAt) || !current.Entries[1].UpdatedAt.Equal(at) {
		t.Fatal("检查点更新了无关文件时间", current.Entries)
	}
}

func TestMediaResultFinishKeepsUnchangedResultTime(t *testing.T) {
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	run := MediaOrganizeRun{Entries: []MediaOrganizeEntry{{
		Status: "needs_match", Message: "请选择作品", Target: "/target",
		Result: &MediaOrganizeResult{Status: "error", Message: "请选择作品", Target: "/target", At: at},
	}}}
	mediaFinishRun(&run)
	if !run.Entries[0].Result.At.Equal(at) {
		t.Fatal("重复汇总刷新了未变化的结果时间", run.Entries[0])
	}
}

func TestMediaResultTaskWaitingPreservesPrimaryFailure(t *testing.T) {
	for _, status := range []string{"needs_match", "invalid", "conflict", "done"} {
		t.Run(status, func(t *testing.T) {
			run := MediaOrganizeRun{Status: "running", Groups: []MediaOrganizeGroup{{ID: "g", Message: "主文件具体原因"}}, Entries: []MediaOrganizeEntry{
				{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "v"}, MediaType: "video"}, Group: "g", Status: status},
				{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "s"}, MediaType: "subtitle"}, Group: "g", VideoID: "v", Status: "error", ResultCode: "WAITING_MAIN_FILE", Target: "/target/subtitle.ass"},
			}}
			mediaFinishRun(&run)
			task := mediaTasks(run)[0]
			if status == "done" {
				if task.Status != "done" {
					t.Fatal("附件等待不应改变主视频成功", task)
				}
			} else if task.Status != "failed" || task.Message != "主文件具体原因" {
				t.Fatal("主文件原因被附件等待覆盖", task)
			}
		})
	}
}

func TestMediaResultUnselectedEmptyTaskKeepsSummary(t *testing.T) {
	run := MediaOrganizeRun{Status: "interrupted", Message: "旧批次提示", Entries: []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "a"}, MediaType: "video"}, Status: "ready"},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "b"}, MediaType: "video"}, Status: "ready"},
	}}
	before, err := mediaRunRow(run)
	if err != nil {
		t.Fatal(err)
	}
	run.TaskFileID, run.Status, run.Message = "a", "queued", "等待重新整理当前任务"
	after, err := mediaRunRow(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Tasks) != 2 || len(after.Tasks) != 1 || after.Tasks[0].FileID != "b" || before.Tasks[1] != after.Tasks[0] {
		t.Fatal("重试当前任务改变了空消息兄弟任务", before, after)
	}
}

func TestMediaResultRenderClearsTransientRecognitionFailure(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.Failure = t.TempDir()
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	run := MediaOrganizeRun{Rule: rule, Groups: []MediaOrganizeGroup{{ID: "g", Kind: "tv", TMDBID: 42, Message: "查询暂未完成", ResultCode: "METADATA_QUERY_FAILED"}}, Entries: []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "v", Name: "Work.strm"}, MediaType: "video"}, Group: "g"},
	}}
	if err := s.render(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if run.Groups[0].ResultCode != "MATCHED" || run.Entries[0].Status != "invalid" || !mediaFailureEntries(&run)["v"] {
		t.Fatal("查询恢复后仍被旧临时错误阻挡", run)
	}
}

func TestMediaResultManualSeasonZeroRetry(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	metadata.items = nil
	mediaTestFile(t, rule.Source, "Work.strm", "原始内容")
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	season, episode := 0, 2
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: run.Entries[0].File.ID, Kind: "tv", TMDBID: 42, Season: &season, Episode: &episode}); err != nil {
		t.Fatal(err)
	}
	run, err = s.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "retry")
	task, err := s.Task(run.ID, "Work.strm")
	if err != nil || task.Status != "done" || task.Season != 0 || task.Episode != 2 || task.Group.TMDBID != 42 || task.Entries[0].File.Name != "Work.strm" {
		t.Fatal(task, err)
	}
}
