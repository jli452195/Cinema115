package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMediaOrganizerTaskRetryOnlyFailedCompanions(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for _, name := range []string{"Work.S01E01.strm", "Work.S01E01.ass", "Work.S01E02.mkv"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	var subtitleTarget string
	for _, e := range run.Entries {
		if e.File.MediaType == "subtitle" {
			subtitleTarget = e.Target
			if err := os.MkdirAll(e.Target, 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	mediaTestProcess(t, s, &run, "execute")
	task, err := s.Task(run.ID, "Work.S01E01.strm")
	if err != nil || task.Status != "done" || !task.CanRetry || len(task.Entries) != 2 || mediaTestState(s, 1).History.Total != 2 {
		t.Fatal("主文件完成后任务应保持成功，并允许单独重试失败附件", task, err)
	}
	other, _ := s.Task(run.ID, "Work.S01E02.mkv")
	inProgress := run
	inProgress.Status = "running"
	for _, projected := range mediaTasks(inProgress) {
		if projected.FileID == other.FileID && projected.Status != "done" {
			t.Fatal("a completed task must stay completed while its batch is running", projected)
		}
	}
	videoResult := task.Entries[0].Result
	s.metadata = nil // Failed companions use the saved plan, without querying TMDB.
	if err := os.Remove(subtitleTarget); err != nil {
		t.Fatal(err)
	}
	queued, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: task.FileID})
	if err != nil {
		t.Fatal(err)
	}
	queued.Entries[0].Result.Message = "mutated"
	snapshot, _ := s.Snapshot(run.ID)
	mediaTestProcess(t, s, &snapshot, "retry")
	finished, _ := s.Task(run.ID, task.FileID)
	unchanged, _ := s.Task(run.ID, other.FileID)
	if finished.Status != "done" || finished.CanRetry || !finished.At.Equal(task.At) || !reflect.DeepEqual(videoResult, finished.Entries[0].Result) || !reflect.DeepEqual(other, unchanged) {
		t.Fatal("retry moved completed primary, changed sibling, or lost attachment", finished, unchanged)
	}
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: task.FileID}); err == nil {
		t.Fatal("completed task should not queue again")
	}
}

func TestMediaOrganizerTaskCorrectionReplansUnwrittenAttachments(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for _, name := range []string{"Work.S01E01.strm", "Work.S01E01.ass", "Work.S01E01(1).ass"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" {
			mediaTestFile(t, rule.Target, entry.Relative, "已存在的独立视频")
		}
	}
	mediaTestProcess(t, s, &run, "execute")
	retained := 0
	for _, entry := range run.Entries {
		if entry.File.MediaType == "subtitle" {
			retained++
			data, err := os.ReadFile(filepath.Join(rule.Source, entry.File.SourcePath))
			if err != nil || string(data) != entry.File.Name || entry.AttachmentKeep != nil || entry.ResultCode != "WAITING_MAIN_FILE" {
				t.Fatal("主视频失败后附件保留来源，结束本次选择", entry, err)
			}
		}
	}
	if retained != 2 {
		t.Fatal("主视频冲突不能丢失附件", run)
	}
	episode := 2
	queued, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "Work.S01E01.strm", Episode: &episode})
	if err != nil {
		t.Fatal("未写入的附件计划不应阻止季集修正", err)
	}
	for _, entry := range queued.Entries {
		if entry.AttachmentKeep != nil {
			t.Fatal("人工修正未清除旧附件目标计划", entry)
		}
	}
	snapshot, err := s.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &snapshot, "retry")
	finished, err := s.Task(run.ID, queued.FileID)
	if err != nil || finished.Status != "done" || finished.Episode != episode {
		t.Fatal("附件未按修正后的主视频重新规划", finished, err)
	}
}

func TestMediaOrganizerTaskCorrectionDropsOldAttachmentChoice(t *testing.T) {
	for _, tc := range []struct {
		phase string
		saved bool
	}{{"recycling", false}, {"selected", true}, {"unknown", false}} {
		t.Run(fmt.Sprintf("%s_%t", tc.phase, tc.saved), func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			mediaTestFile(t, rule.Source, "Work.S01E01.strm", "来源")
			mediaTestFile(t, rule.Source, "Work.S01E01.ass", "字幕")
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "scan")
			run.Entries[1].AttachmentKeep = &MediaAttachmentKeepIntent{Phase: tc.phase}
			if tc.saved {
				run.Entries[1].AttachmentKeep.Source = &MediaAttachmentFileSnapshot{ID: "已保存来源"}
			}
			if err := s.checkpoint(&run); err != nil {
				t.Fatal(err)
			}
			episode := 2
			queued, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "Work.S01E01.strm", Episode: &episode})
			if err != nil || queued.Episode != episode {
				t.Fatal("旧附件选择不能阻止主文件修正", queued, err)
			}
			for _, entry := range queued.Entries {
				if entry.AttachmentKeep != nil {
					t.Fatal("下次按当前事实重算附件", entry)
				}
			}
		})
	}
}

func TestMediaOrganizerSeparateVersionsAndSharedArtwork(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for _, name := range []string{"a/Work.S01E01.mkv", "b/Work.S01E01.mkv", "poster.jpg"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	state := mediaTestState(s, 1)
	if state.History.Total != 2 {
		t.Fatal("different file versions merged", state)
	}
	attachments := 0
	for _, item := range state.History.Items {
		attachments += item.AttachmentCount
	}
	if attachments != 1 {
		t.Fatal("shared artwork duplicated or lost", state)
	}
	for _, id := range []string{"a/Work.S01E01.mkv", "b/Work.S01E01.mkv"} {
		task, err := s.Task(run.ID, id)
		if err != nil || task.FileID != id || task.Entries[0].File.ID != id {
			t.Fatal("task identity lost", task, err)
		}
	}
}

func TestMediaOrganizerTaskCorrectionAndRestartScope(t *testing.T) {
	s, metadata, store, rule := newMediaOrganizeTest(t)
	metadata.items = nil
	rule.Failure = t.TempDir()
	rule, _ = s.SaveRule(rule)
	for _, name := range []string{"Unknown.S01E01.strm", "Unknown.S01E01.ass", "Unknown.S01E02.strm"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	other, _ := s.Task(run.ID, "Unknown.S01E02.strm")
	_, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "Unknown.S01E01.strm", Kind: "tv", TMDBID: 42})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := restarted.Snapshot(run.ID)
	if snapshot.Status != "interrupted" || snapshot.TaskFileID != "Unknown.S01E01.strm" {
		t.Fatal("restart lost task scope", snapshot)
	}
	_, err = restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: snapshot.TaskFileID})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ = restarted.Snapshot(run.ID)
	mediaTestProcess(t, restarted, &snapshot, "retry")
	finished, _ := restarted.Task(run.ID, "Unknown.S01E01.strm")
	unchanged, _ := restarted.Task(run.ID, other.FileID)
	want, marshalErr := json.Marshal(other)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	got, marshalErr := json.Marshal(unchanged)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if finished.Status != "done" || string(want) != string(got) {
		t.Fatal("single correction changed another task", finished, unchanged)
	}
	if _, err := os.Stat(filepath.Join(rule.Failure, other.FileID)); err != nil {
		t.Fatal("other failed task was moved", err)
	}
}

func TestMediaOrganizerTaskRetryNumberingPreservesCorrections(t *testing.T) {
	correctedEpisode, emptyEpisode := 13, 0
	for _, tc := range []struct {
		name    string
		file    string
		season  int
		episode int
		input   *int
		want    int
		wantEnd int
	}{
		{name: "missing", file: "Work.S02E07 - 7³ACG.mkv", season: 2, want: 7},
		{name: "missing-aac-channels", file: "花朵盛开 (2026) - S01E26 - 1080p.Hami Video.WEB-DL.H264.AAC 2.0.mkv", season: 1, want: 26},
		{name: "missing-ddp-channels", file: "极恶老大 (2020) - S03E07 - 1080p.Amazon.WEB-DL.H264.DDP 5.1.mkv", season: 3, want: 7},
		{name: "missing-420p", file: "未确认进行式.2014.S01E08.420P.mkv", season: 1, want: 8},
		{name: "missing-420p-nil-tags", file: "Work.S01E08.420P.WEB-DL.H264.AAC 2.0.mkv", season: 1, want: 8},
		{name: "saved-420p-tags", file: "Work.S01E08.420P.mkv", season: 1, episode: 8, want: 8},
		{name: "saved-season", file: "Work.S02E07 - 7³ACG.mkv", season: 9, want: 7},
		{name: "saved-episode", file: "Work.S02E07 - 7³ACG.mkv", season: 2, episode: 3, want: 3},
		{name: "manual-episode", file: "Work.S02E07 - 7³ACG.mkv", season: 2, input: &correctedEpisode, want: 13},
		{name: "explicit-empty", file: "Work.S02E07 - 7³ACG.mkv", season: 2, input: &emptyEpisode},
		{name: "real-range", file: "Work.S02E04-05 - 7³ACG.mkv", season: 2, want: 4, wantEnd: 5},
		{name: "invalid-range", file: "Work.S02E04-03.mkv", season: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			mediaTestFile(t, rule.Source, tc.file, "video")
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "scan")
			run.Entries[0].File.Season, run.Entries[0].File.Episode, run.Entries[0].File.EndEpisode = tc.season, tc.episode, 0
			if tc.name == "missing-420p" {
				// 模拟旧解析器保存的空分辨率，并保留目录继承的字幕信息。
				run.Entries[0].File.NamingValues["resolution"] = ""
				run.Entries[0].File.NamingValues["languages"] = "简体"
			}
			if tc.name == "missing-420p-nil-tags" {
				run.Entries[0].File.NamingValues = nil
			}
			if tc.name == "saved-420p-tags" {
				run.Entries[0].File.NamingValues["resolution"] = "720p"
			}
			if err := s.checkpoint(&run); err != nil {
				t.Fatal(err)
			}
			queued, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: tc.file, Episode: tc.input})
			if err != nil || len(queued.Entries) != 1 {
				t.Fatal(queued, err)
			}
			file := queued.Entries[0].File
			if file.Season != tc.season || file.Episode != tc.want || file.EndEpisode != tc.wantEnd {
				t.Fatal("缺失集数可恢复，已有值、手动修正和歧义校验必须保留", file)
			}
			if tc.name == "missing-420p" && (file.NamingValues["resolution"] != "420p" || file.NamingValues["languages"] != "简体") {
				t.Fatal("旧任务应恢复分辨率并保留继承的字幕标签", file)
			}
			if tc.name == "missing-420p-nil-tags" && (file.NamingValues["resolution"] != "420p" || file.NamingValues["source"] != "WEB-DL" || file.NamingValues["videoCodec"] != "H264" || file.NamingValues["audioCodec"] != "AAC" || file.NamingValues["channels"] != "2.0") {
				t.Fatal("没有命名标签的旧任务应完整解析文件名中的规格", file)
			}
			if tc.name == "saved-420p-tags" && file.NamingValues["resolution"] != "720p" {
				t.Fatal("重试不能覆盖已保存的非空分辨率", file)
			}
		})
	}
}

// A small real-filesystem run: classification, STRM content, video, artwork,
// cleanup, duplicate submission and a second scan use the application's worker.
func TestMediaOrganizerGrayRun(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	settings, _, _ := classificationTestStore(t)
	s.classification = settings
	rule.Classify = true
	var err error
	if rule, err = s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	const strm = "http://example.invalid/original\r\n"
	mediaTestFile(t, rule.Source, "show/Work.S01E01.strm", strm)
	for _, name := range []string{"show/Work.S01E01.ass", "show/Work.S01E02.mkv", "show/poster.jpg", "Other.Movie.2023-poster.jpg", "ads.txt"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Start(rule.ID)
	if err != nil || duplicate.ID != run.ID {
		t.Fatal("duplicate submission", duplicate, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait := func(id string) MediaOrganizeRun {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			r, _ := s.Snapshot(id)
			if !mediaRunActive(r.Status) && !s.State().Active {
				return r
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("worker did not complete")
		return MediaOrganizeRun{}
	}
	run = wait(run.ID)
	state := mediaTestState(s, 1)
	if state.History.Total != 2 || state.History.Counts["done"] != 2 || len(state.Cleanup) != 0 || state.Rules[0].RealtimeEnabled {
		t.Fatal("incorrect task/cleanup projection or monitoring toggled", state)
	}
	if task, err := s.Task(run.ID, "show/Work.S01E01.strm"); err != nil || task.Status != "done" || task.At.IsZero() {
		t.Fatal("本轮结束后成功记录应继续可查询", task, err)
	}
	var target string
	for _, entry := range run.Entries {
		if entry.File.ID == "show/Work.S01E01.strm" {
			target = entry.Target
			if !entry.Reference || entry.Result == nil || entry.Result.Status != "done" || entry.Result.At.IsZero() {
				t.Fatal("成功只保留精简完成结果与只读映射", entry)
			}
		}
	}
	if target == "" || len(run.Groups) != 1 || run.Groups[0].Category != "剧集/其他剧集" {
		t.Fatal("分类结果及目标映射必须保留", run)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != strm {
		t.Fatal("STRM changed", err)
	}
	for _, name := range []string{"Other.Movie.2023-poster.jpg", "ads.txt", "show"} {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cleanup failed", name, err)
		}
	}
	next, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait(next.ID)
	if state := mediaTestState(s, 1); state.History.Total != 2 || state.History.Counts["done"] != 2 {
		t.Fatal("空扫描不能清除或重复生成成功历史", state)
	}
	t.Log("gray run: YAML classification, 2 media tasks, matched subtitles/artwork, orphan cleanup, unchanged STRM, duplicate submission and empty rescan passed")
}
