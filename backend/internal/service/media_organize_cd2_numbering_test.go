package service

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/tmdb"
)

func mediaVinlandMetadata() *mediaCollectionMetadata {
	item := tmdb.Item{ID: 88803, Kind: "tv", Title: "冰海战记", OriginalTitle: "Vinland Saga", Date: "2019-07-08"}
	return &mediaCollectionMetadata{
		items: []tmdb.Item{item},
		details: map[int]*tmdb.Detail{88803: {
			Item: item, OriginalLanguage: "ja",
			Seasons: []tmdb.SeasonSummary{{Number: 1, Date: "2019-07-08"}, {Number: 2, Date: "2023-01-10"}},
		}},
	}
}

func TestMediaOrganizerCD2DigitLeadingPublisherNumbering(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	s.metadata = mediaVinlandMetadata()
	fs := connection.filesystem
	directory := "冰海战记 (2019) {tmdb-88803}/Season 02"
	fs.add(path.Join(rule.Source, path.Dir(directory)), true, 0)
	fs.add(path.Join(rule.Source, directory), true, 0)
	expected := make(map[string]int, 48)
	for episode := 1; episode <= 24; episode++ {
		stem := fmt.Sprintf("冰海战记 - S02E%02d - 7³ACG", episode)
		for _, extension := range []string{".mkv", ".zh.ass"} {
			file := fs.add(path.Join(rule.Source, directory, stem+extension), false, 123)
			expected[file.Path] = episode
		}
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" || len(mediaTasks(run)) != 24 {
		t.Fatal("数字开头发布组不能使整季误判为合并集或无效集数", run.Status, run.Entries)
	}
	checked := 0
	for _, entry := range run.Entries {
		episode, ok := expected[entry.File.ID]
		if !ok {
			continue
		}
		checked++
		if entry.Status != "done" || !strings.Contains(path.Base(entry.Target), fmt.Sprintf(".S02E%02d.", episode)) || path.Base(path.Dir(entry.Target)) != "Season 02" {
			t.Fatal("视频及字幕必须按单集整理到第二季", entry)
		}
		if entry.File.MediaType == "video" && (entry.File.Season != 2 || entry.File.Episode != episode || entry.File.EndEpisode != 0) {
			t.Fatal("发布组数字污染了主文件的季集", entry.File)
		}
		if _, exists := fs.files[entry.File.ID]; exists {
			t.Fatal("来源视频或字幕未移动", entry.File.ID)
		}
		if target, exists := fs.files[entry.Target]; !exists || target.Size != entry.File.Size {
			t.Fatal("目标视频或字幕缺失", entry.Target)
		}
	}
	if checked != len(expected) {
		t.Fatal("遗漏媒体或字幕", checked, len(expected))
	}
}

func TestMediaOrganizerCD2RetryRepairsMissingPublisherNumbering(t *testing.T) {
	s, _, store, connection, rule := newMediaCD2Organizer(t)
	s.metadata = mediaVinlandMetadata()
	rule.Failure = "/其他网盘/failed"
	var err error
	rule, err = s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	fs := connection.filesystem
	for _, name := range []string{"冰海战记 - S02E07 - 7³ACG.mkv", "冰海战记 - S02E07 - 7³ACG.zh.ass", "冰海战记 - S02E08 - 7³ACG.mkv"} {
		fs.add(path.Join(rule.Source, name), false, 123)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	fileID := path.Join(rule.Source, "冰海战记 - S02E07 - 7³ACG.mkv")
	index := slices.IndexFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == fileID })
	if index < 0 {
		t.Fatal("扫描缺少第七集")
	}
	// 模拟旧解析器保存的缺集数记录，并经过实际失败目录转移流程。
	run.Entries[index].File.Episode = 0
	run.Entries[index].File.EpisodeExplicit = false
	mediaTestProcess(t, s, &run, "retry")
	failed, err := s.Task(run.ID, fileID)
	if err != nil || failed.Status == "done" || failed.Entries[0].Failure == nil || failed.Entries[0].Failure.State != "stored" {
		t.Fatal("没有复现已移入失败目录的历史任务", failed, err)
	}
	siblingID := path.Join(rule.Source, "冰海战记 - S02E08 - 7³ACG.mkv")
	sibling, err := s.Task(run.ID, siblingID)
	if err != nil || sibling.Status != "done" {
		t.Fatal("其他集应已完成", sibling, err)
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	beforeRetry, err := restarted.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	siblingIndex := slices.IndexFunc(beforeRetry.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == siblingID })
	if siblingIndex < 0 {
		t.Fatal("重启后缺少其他已完成集的映射", beforeRetry.Entries)
	}
	siblingReference := beforeRetry.Entries[siblingIndex]
	if !siblingReference.Reference || siblingReference.Status != "done" || siblingReference.Result == nil || siblingReference.Result.Status != "done" || !siblingReference.Result.At.Equal(sibling.At) || siblingReference.Target != sibling.Target || siblingReference.File.Season != 2 || siblingReference.File.Episode != 8 {
		t.Fatal("其他已完成集应保留原完成结果、时间及正确目标映射", siblingReference)
	}
	queued, err := restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: fileID})
	if err != nil || queued.Entries[0].File.Episode != 7 || queued.Entries[0].File.Season != 2 {
		t.Fatal("普通重试应从原文件名恢复缺失集数", queued, err)
	}
	snapshot, err := restarted.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, restarted, &snapshot, "retry")
	finished, err := restarted.Task(run.ID, fileID)
	if err != nil || finished.Status != "done" || len(finished.Entries) != 2 {
		t.Fatal("重试应恢复失败目录中的视频和字幕", finished, err)
	}
	for _, entry := range finished.Entries {
		if entry.Status != "done" || entry.Failure != nil || !strings.Contains(entry.Target, ".S02E07.") {
			t.Fatal("第七集及字幕重试结果错误", entry)
		}
		if _, exists := fs.files[entry.Target]; !exists {
			t.Fatal("重试后目标文件缺失", entry.Target)
		}
	}
	afterRetry, err := restarted.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	siblingIndex = slices.IndexFunc(afterRetry.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == siblingID })
	if siblingIndex < 0 {
		t.Fatal("重试丢失其他已完成集的映射", afterRetry.Entries)
	}
	unchanged := afterRetry.Entries[siblingIndex]
	want, err := json.Marshal(siblingReference)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(unchanged)
	if err != nil || string(got) != string(want) {
		t.Fatal("单任务重试改变了其他集的映射", siblingReference, unchanged, err)
	}
	if target, exists := fs.files[unchanged.Target]; !exists || target.Size != unchanged.File.Size {
		t.Fatal("单任务重试影响了其他集的目标文件", unchanged)
	}
}
