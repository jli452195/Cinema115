package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/tmdb"
)

func TestMediaOrganizerZeroEpisodeSTRMAndSubtitles(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	metadata.items = []tmdb.Item{{ID: 42, Kind: "tv", Title: "一间房", Date: "2017-01-01"}}
	const stem = "一间房 - S02E00 - 第 0 集"
	const body = "https://example.invalid/original-file\r\n"
	for _, extension := range []string{".strm", ".default.chi.zh-cn.ass", ".zh-tw.ass"} {
		mediaTestFile(t, rule.Source, filepath.Join("Season 2", stem+extension), body)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	count := 0
	for _, entry := range run.Entries {
		if entry.File.MediaType != "cleanup" {
			count++
		}
	}
	if run.Status != "completed" || count != 3 {
		t.Fatal("TMDB 没有分集资料时，明确零集与配套字幕仍应完成整理", run)
	}
	for _, entry := range run.Entries {
		if entry.File.MediaType == "cleanup" {
			continue
		}
		if entry.Status != "done" || !strings.Contains(entry.Target, "Season 02/") || !strings.Contains(entry.Target, ".S02E00") {
			t.Fatal("主文件与字幕应保留原零集坐标", entry)
		}
		content, err := os.ReadFile(filepath.FromSlash(entry.Target))
		if err != nil || string(content) != body {
			t.Fatal("整理不得改写 STRM 或字幕正文", entry, err)
		}
	}
	task, err := s.Task(run.ID, "Season 2/"+stem+".strm")
	if err != nil || !task.EpisodeExplicit || len(task.Entries) == 0 || !task.Entries[0].File.EpisodeExplicit {
		t.Fatal("成功历史与详情应保留明确零集标记", task, err)
	}
}

func TestMediaOrganizerManualZeroEpisodeSurvivesRestartAndRetry(t *testing.T) {
	for _, name := range []string{"Work.strm", "Work.S01E05.strm"} {
		t.Run(name, func(t *testing.T) {
			s, _, store, rule := newMediaOrganizeTest(t)
			mediaTestFile(t, rule.Source, name, "原始内容")
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "scan")
			season, episode := 2, 0
			if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: name, Season: &season, Episode: &episode}); err != nil {
				t.Fatal(err)
			}
			restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: name}); err != nil {
				t.Fatal(err)
			}
			retry, err := restarted.Snapshot(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, restarted, &retry, "retry")
			entry := retry.Entries[0]
			if entry.Status != "done" || !entry.File.EpisodeExplicit || entry.File.Episode != 0 || entry.File.Season != season || !strings.Contains(entry.Target, fmt.Sprintf(".S%02dE00", season)) {
				t.Fatal("人工零集必须持久化，直接重试不能回填原集数", entry)
			}
		})
	}
}
