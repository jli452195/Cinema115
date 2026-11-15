package service

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

func wonderlandYearMetadata() *mediaYearBenchMetadata {
	wrong := tmdb.Item{ID: 48936, Kind: "tv", Title: "Wonderland", OriginalTitle: "Wonderland", Date: "2013-08-21"}
	right := tmdb.Item{ID: 126398, Kind: "tv", Title: "五十公里桃花坞", OriginalTitle: "五十公里桃花坞", Date: "2021-05-23"}
	return &mediaYearBenchMetadata{
		page: tmdb.Page{Items: []tmdb.Item{wrong, right}, TotalPages: 1},
		details: map[int]*tmdb.Detail{
			48936:  {Item: wrong, Seasons: []tmdb.SeasonSummary{{Number: 1, Date: "2013-08-21"}}},
			126398: {Item: right, Seasons: []tmdb.SeasonSummary{{Number: 0, Date: "2021-05-23"}}},
		},
	}
}

func wonderlandBatchEntries(count int) []MediaOrganizeEntry {
	entries := make([]MediaOrganizeEntry, 0, count)
	const parent = "五十公里桃花坞 (2021) {tmdb-126398}/S*******"
	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("Wonderland.2021.S00E%02d.2160p.WEB-DL.60fps.HEVC.DDP 2.0-WF.strm", i)
		source := path.Join(parent, name)
		file := OrganizeFile{ShareFile: p115.ShareFile{ID: source, Name: name}, ParentID: parent, SourcePath: source, MediaType: "video", Season: 1}
		mediaOrganizeFileNumbering(&file)
		entries = append(entries, MediaOrganizeEntry{File: file})
	}
	return entries
}

func TestMediaOrganizerWonderlandPlaceholderBatch(t *testing.T) {
	metadata := wonderlandYearMetadata()
	organizer := &MediaOrganizer{metadata: metadata}
	run := MediaOrganizeRun{Rule: MediaOrganizeRule{Source: "/strm/待整理"}}
	mediaAppendScanEntries(&run, wonderlandBatchEntries(289))
	if len(run.Groups) != 1 || len(run.Entries) != 289 || run.Groups[0].Identity.TMDBID != 126398 {
		t.Fatalf("同目录 289 个特别篇应继承同一作品 ID 并归为一组: groups=%+v entries=%d", run.Groups, len(run.Entries))
	}
	if err := organizer.recognize(context.Background(), &run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Groups[0].TMDBID != 126398 || run.Groups[0].Title != "五十公里桃花坞" || run.Groups[0].MatchedSeason == nil || *run.Groups[0].MatchedSeason != 0 {
		t.Fatalf("不能误选排在前面的 2013 年同名作品: %+v", run.Groups[0])
	}
	if metadata.browseCalls != 0 || metadata.detailCalls != 1 || metadata.seasonCalls != 0 {
		t.Fatalf("289 条文件应只直查一次作品，无搜索或季补查: %+v", metadata)
	}
	for i, entry := range run.Entries {
		if entry.Group != run.Groups[0].ID || entry.File.Season != 0 || entry.File.Episode != i+1 {
			t.Fatalf("批量识别不能修改明确的特殊集坐标: %+v", entry)
		}
	}
}

func TestMediaOrganizerWonderlandLocalSTRM(t *testing.T) {
	for _, directory := range []string{"Season 00", "S*******"} {
		t.Run(directory, func(t *testing.T) {
			if directory == "S*******" && runtime.GOOS == "windows" {
				t.Skip("Windows 文件系统不允许星号目录，真实占位路径由 289 条扫描分组测试覆盖")
			}
			organizer, _, _, rule := newMediaOrganizeTest(t)
			metadata := wonderlandYearMetadata()
			organizer.metadata = metadata
			const body = "http://example.invalid/original.strm?token=unchanged\r\n"
			for _, episode := range []int{169, 170} {
				name := fmt.Sprintf("Wonderland.2021.S00E%d.2160p.WEB-DL.60fps.HEVC.DDP 2.0-WF.strm", episode)
				mediaTestFile(t, rule.Source, path.Join("五十公里桃花坞 (2021) {tmdb-126398}", directory, name), body)
			}
			run, err := organizer.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, organizer, &run, "auto")
			if run.Status != "completed" || len(run.Groups) != 1 || run.Groups[0].TMDBID != 126398 {
				t.Fatalf("本地整理没有完成正确的作品匹配: %+v", run)
			}
			videos := 0
			for _, entry := range run.Entries {
				if entry.File.MediaType != "video" {
					continue
				}
				videos++
				want := fmt.Sprintf("五十公里桃花坞 (2021) {tmdb-126398}/Season 00/五十公里桃花坞.S00E%d.2160p.WEB-DL.HEVC.60fps.DDP.2.0.strm", entry.File.Episode)
				if entry.Status != "done" || !strings.HasSuffix(filepath.ToSlash(entry.Target), want) || entry.File.Season != 0 {
					t.Fatalf("目标作品、季号或重命名错误: %+v", entry)
				}
				data, err := os.ReadFile(filepath.FromSlash(entry.Target))
				if err != nil || string(data) != body {
					t.Fatalf("STRM 正文必须保持原样: body=%q err=%v", data, err)
				}
				if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(entry.File.SourcePath))); !os.IsNotExist(err) {
					t.Fatalf("完成整理后来源文件应移走: %v", err)
				}
			}
			// 作品识别与命名准备各读取一次详情，文件增加不会逐个重查。
			if videos != 2 || metadata.browseCalls != 0 || metadata.detailCalls != 2 || metadata.seasonCalls != 0 {
				t.Fatalf("多个同组文件不得重复识别作品: videos=%d metadata=%+v", videos, metadata)
			}
		})
	}
}
