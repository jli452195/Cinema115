package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/recognition"
)

type mediaRegressionCase struct {
	category, source, work, filename, videoSource string
}

// 成功样本表只保留输入和期望，元数据固定在测试中，避免网络波动改变回归结论。
func mediaRegressionCases(tb testing.TB) []mediaRegressionCase {
	tb.Helper()
	file, err := os.Open("testdata/media_organize_regression.csv")
	if err != nil {
		tb.Fatal(err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = 5
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 2 {
		tb.Fatalf("读取整理回归样本表: rows=%d err=%v", len(rows), err)
	}
	if strings.Join(rows[0], ",") != "类别,来源相对路径,预期作品,预期文件名,字幕对应视频" {
		tb.Fatal("整理回归样本表字段不符")
	}
	cases := make([]mediaRegressionCase, 0, len(rows)-1)
	seen := make(map[string]bool, len(rows)-1)
	for _, row := range rows[1:] {
		if row[0] == "" || row[1] == "" || row[2] == "" || row[3] == "" || seen[row[1]] {
			tb.Fatalf("整理回归样本为空或重复: %q", row)
		}
		seen[row[1]] = true
		cases = append(cases, mediaRegressionCase{row[0], row[1], row[2], row[3], row[4]})
	}
	return cases
}

type mediaRegressionWork struct {
	detail  tmdb.Detail
	aliases []string
	queries []string
}

type mediaRegressionMetadata struct {
	works                                []mediaRegressionWork
	browseCalls, detailCalls, aliasCalls int
}

func newMediaRegressionMetadata() *mediaRegressionMetadata {
	tv := func(id int, title, original, date string, seasons ...int) tmdb.Detail {
		out := tmdb.Detail{Item: tmdb.Item{ID: id, Kind: "tv", Title: title, OriginalTitle: original, Date: date}, Seasons: make([]tmdb.SeasonSummary, 0, len(seasons))}
		for _, number := range seasons {
			out.Seasons = append(out.Seasons, tmdb.SeasonSummary{Number: number, Date: date})
		}
		return out
	}
	animation := tv(69292, "人渣的本愿", "クズの本懐", "2017-01-13", 1)
	animation.Genres = []tmdb.Genre{{ID: 16}}
	liveAction := tv(69812, "人渣的本愿真人版", "クズの本懐", "2017-01-18", 1)
	liveAction.Genres = []tmdb.Genre{{ID: 18}}
	return &mediaRegressionMetadata{works: []mediaRegressionWork{
		{detail: tv(48936, "Wonderland", "Wonderland", "2013-08-21", 1, 2, 3)},
		{detail: tv(126398, "五十公里桃花坞", "五十公里桃花坞", "2021-05-23", 0, 1, 2, 3, 4, 5), aliases: []string{"Wonderland"}},
		{detail: tv(289273, "余烬行者", "余烬行者", "2025-06-12", 1)},
		{detail: tv(222608, "仙武传", "仙武传", "2023-03-19", 0, 1)},
		{detail: tv(62104, "七大罪", "七つの大罪", "2014-10-05", 0, 1, 2, 3, 4), aliases: []string{"Nanatsu no Taizai", "The Seven Deadly Sins"}},
		{detail: tmdb.Detail{Item: tmdb.Item{ID: 157336, Kind: "movie", Title: "星际穿越", OriginalTitle: "Interstellar", Date: "2014-11-05"}}},
		{detail: tv(154770, "你好，星期六", "你好，星期六", "2022-01-01", 0, 1, 2, 3, 4, 5), aliases: []string{"Hello Saturday"}, queries: []string{"你好，星期六 Hello Saturday"}},
		{detail: tv(129804, "你的名字我的姓氏", "你的名字我的姓氏", "2021-07-27", 1), aliases: []string{"Once Given, Never Forgotten"}, queries: []string{"你的名字我的姓氏 Once Given Never Forgotten"}},
		{detail: tv(204541, "三体", "三体", "2023-01-15", 1)},
		{detail: tv(196944, "我的幸福婚姻", "わたしの幸せな結婚", "2023-07-05", 0), aliases: []string{"My Happy Marriage"}},
		{detail: animation, aliases: []string{"Kuzu no Honkai"}},
		{detail: liveAction, aliases: []string{"Kuzu no Honkai"}},
	}}
}

func (f *mediaRegressionMetadata) Browse(ctx context.Context, options tmdb.Options) (*tmdb.Page, error) {
	f.browseCalls++
	items := make([]tmdb.Item, 0, 2)
	for _, work := range f.works {
		if options.Kind != "multi" && options.Kind != work.detail.Kind {
			continue
		}
		names := append([]string{work.detail.Title, work.detail.OriginalTitle}, work.aliases...)
		names = append(names, work.queries...)
		for _, name := range names {
			if mediafacts.NameKey(options.Query) == mediafacts.NameKey(name) {
				items = append(items, work.detail.Item)
				break
			}
		}
	}
	return &tmdb.Page{Items: items, Page: options.Page, TotalPages: 1}, ctx.Err()
}

func (f *mediaRegressionMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	f.detailCalls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i := range f.works {
		if f.works[i].detail.ID == id && f.works[i].detail.Kind == kind {
			return &f.works[i].detail, nil
		}
	}
	return nil, tmdb.ErrNotFound
}

func (f *mediaRegressionMetadata) AlternativeNames(ctx context.Context, kind string, id int) ([]string, error) {
	f.aliasCalls++
	for _, work := range f.works {
		if work.detail.ID == id && work.detail.Kind == kind {
			return work.aliases, ctx.Err()
		}
	}
	return nil, tmdb.ErrNotFound
}

func mediaRegressionRun(source string) MediaOrganizeRun {
	file := OrganizeFile{ShareFile: p115.ShareFile{ID: source, Name: path.Base(source)}, ParentID: path.Dir(source), SourcePath: source, MediaType: "video", Season: 1}
	mediaOrganizeFileNumbering(&file)
	run := MediaOrganizeRun{Rule: MediaOrganizeRule{Source: "/strm/待整理"}}
	mediaAppendScanEntries(&run, []MediaOrganizeEntry{{File: file}})
	return run
}

func TestMediaOrganizeRegressionCorpus(t *testing.T) {
	for _, tc := range mediaRegressionCases(t) {
		t.Run(tc.category+"/"+path.Base(tc.source), func(t *testing.T) {
			metadata := newMediaRegressionMetadata()
			videoSource := tc.source
			if tc.videoSource != "" {
				videoSource = tc.videoSource
			}
			run := mediaRegressionRun(videoSource)
			// 自动整理会持久化分组，恢复后必须保留相同的标题、年份和季集声明。
			body, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &run); err != nil {
				t.Fatal(err)
			}
			organizer := &MediaOrganizer{metadata: metadata}
			ctx := pinNamingRecognition(context.Background(), recognition.Snapshot{})
			if err := organizer.recognize(ctx, &run, nil); err != nil {
				t.Fatal(err)
			}
			if len(run.Groups) != 1 || len(run.Entries) != 1 {
				t.Fatalf("单文件样本分组错误: %+v", run)
			}
			group, file := run.Groups[0], run.Entries[0].File
			if group.Kind+":"+strconv.Itoa(group.TMDBID) != tc.work {
				t.Fatalf("作品识别回归: got=%+v want=%s", group, tc.work)
			}
			template, err := naming.Default(naming.Kind(group.Kind))
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := (MediaNaming{Templates: &mediaNamingTemplates{record: naming.Snapshot{Template: template}}, Metadata: metadata}).prepare(ctx, group.Kind, group.TMDBID, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := prepared.render(MediaNamingFile{OrganizeSelection: OrganizeSelection{ID: file.ID, Season: file.Season, Episode: file.Episode}, Name: file.Name})
			if err == nil && tc.videoSource != "" {
				sub := mediaSubtitleTestFile(tc.source, path.Dir(tc.source), path.Base(tc.source), "subtitle")
				matches := mediaOrganizeSubtitles([]OrganizeFile{file, sub})
				if len(matches) != 1 || matches[0].VideoID != file.ID {
					t.Fatalf("字幕没有关联预期视频: %+v", matches)
				}
				result, err = naming.Companion(result, matches[0].Tail, sub.Name)
			}
			if err != nil || result.Name != tc.filename {
				t.Fatalf("季集或命名回归: got=%q want=%q err=%v", result.Name, tc.filename, err)
			}
		})
	}
}

func TestMediaOrganizeRegressionLocalSTRM(t *testing.T) {
	for _, tc := range mediaRegressionCases(t) {
		if tc.category != "双语综艺" && tc.category != "括号双语剧" && tc.category != "无年份唯一" {
			continue
		}
		t.Run(tc.category, func(t *testing.T) {
			organizer, _, _, rule := newMediaOrganizeTest(t)
			organizer.metadata = newMediaRegressionMetadata()
			const body = "http://example.invalid/original-media?token=unchanged\r\n"
			mediaTestFile(t, rule.Source, tc.source, body)
			run, err := organizer.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			run.Recognition = recognition.Snapshot{}
			mediaTestProcess(t, organizer, &run, "auto")
			videos := 0
			for _, entry := range run.Entries {
				if entry.File.MediaType != "video" {
					continue
				}
				videos++
				if entry.Status != "done" || filepath.Base(entry.Target) != tc.filename {
					t.Fatalf("临时目录实际整理未生成预期文件: %+v", entry)
				}
				data, err := os.ReadFile(filepath.FromSlash(entry.Target))
				if err != nil || string(data) != body {
					t.Fatalf("整理后 STRM 正文不能改变: body=%q err=%v", data, err)
				}
			}
			if videos != 1 || run.Status != "completed" {
				t.Fatalf("临时目录整理未完成: videos=%d status=%s", videos, run.Status)
			}
		})
	}
}

func TestMediaOrganizeNamePreviewUsesFirstEligible(t *testing.T) {
	for _, tc := range []struct {
		name        string
		label       string
		liveFirst   bool
		wantID      int
		wantLookups int
	}{
		{name: "动画排首位", wantID: 69292, wantLookups: 1},
		{name: "真人排首位", liveFirst: true, wantID: 69812, wantLookups: 1},
		{name: "明确动画标签筛除首位真人剧", label: " [动画]", liveFirst: true, wantID: 69292, wantLookups: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := newMediaRegressionMetadata()
			if tc.liveFirst {
				slices.Reverse(metadata.works)
			}
			template, err := naming.Default(naming.Kind("tv"))
			if err != nil {
				t.Fatal(err)
			}
			organizer := &MediaOrganizer{
				metadata:  metadata,
				templates: &mediaNamingTemplates{record: naming.Snapshot{Template: template}},
			}
			ctx := pinNamingRecognition(context.Background(), recognition.Snapshot{})
			input := "[JYFanSub]Kuzu no Honkai" + tc.label + " - 01 (BDrip 1920x1080 HEVC 2FLAC).mkv"
			preview, err := organizer.PreviewName(ctx, input)
			if err != nil || preview.Work == nil || preview.Work.ID != tc.wantID || preview.RelativePath == "" {
				t.Fatalf("名称预览没有生成预期作品和路径: preview=%+v err=%v", preview, err)
			}
			if metadata.browseCalls != 1 || metadata.aliasCalls != tc.wantLookups || metadata.detailCalls != tc.wantLookups {
				t.Fatalf("候选查找次数不符: search=%d aliases=%d details=%d", metadata.browseCalls, metadata.aliasCalls, metadata.detailCalls)
			}
		})
	}
}

func TestMediaOrganizeNamePreviewNormalizesSubtitleTags(t *testing.T) {
	template, err := naming.Default(naming.TV)
	if err != nil {
		t.Fatal(err)
	}
	organizer := &MediaOrganizer{
		metadata:  newMediaRegressionMetadata(),
		templates: &mediaNamingTemplates{record: naming.Snapshot{Template: template}},
	}
	ctx := pinNamingRecognition(context.Background(), recognition.Snapshot{})
	const want = "七大罪.S01E01.1080p.WEB-DL.[zh-CN+zh-TW].softsub.mkv"
	for _, input := range []string{"Nanatsu.no.Taizai.2014.S01E01.1080p.WEB-DL.[简繁内封].mkv", want} {
		preview, err := organizer.PreviewName(ctx, input)
		if err != nil || preview.Work == nil || preview.Work.ID != 62104 || path.Base(preview.RelativePath) != want ||
			preview.Specifications[naming.Languages] != "zh-CN+zh-TW" || preview.Specifications[naming.SubtitleMode] != "softsub" {
			t.Fatalf("%q 的字幕规格或命名预览有误: %+v, %v", input, preview, err)
		}
	}
}

func BenchmarkMediaRecognitionBilingualBatch(b *testing.B) {
	for _, tc := range mediaRegressionCases(b) {
		if tc.category != "双语综艺" && tc.category != "括号双语剧" {
			continue
		}
		b.Run(tc.category, func(b *testing.B) {
			entries := make([]MediaOrganizeEntry, 0, 40)
			seed := mediaRegressionRun(tc.source).Entries[0].File
			coordinate := fmt.Sprintf("S%02dE%02d", seed.Season, seed.Episode)
			for episode := 1; episode <= 40; episode++ {
				source := strings.Replace(tc.source, coordinate, fmt.Sprintf("S%02dE%02d", seed.Season, episode), 1)
				file := OrganizeFile{ShareFile: p115.ShareFile{ID: source, Name: path.Base(source)}, ParentID: path.Dir(source), SourcePath: source, MediaType: "video", Season: 1}
				mediaOrganizeFileNumbering(&file)
				entries = append(entries, MediaOrganizeEntry{File: file})
			}
			metadata := newMediaRegressionMetadata()
			organizer := &MediaOrganizer{metadata: metadata}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				run := MediaOrganizeRun{Rule: MediaOrganizeRule{Source: "/strm/待整理"}}
				mediaAppendScanEntries(&run, entries)
				if err := organizer.recognize(context.Background(), &run, nil); err != nil {
					b.Fatal(err)
				}
				if len(run.Groups) != 1 || run.Groups[0].Kind+":"+strconv.Itoa(run.Groups[0].TMDBID) != tc.work {
					b.Fatalf("同一双语目录的文件未复用正确作品: %+v", run.Groups)
				}
			}
			b.ReportMetric(float64(metadata.browseCalls)/float64(b.N), "search/batch")
			b.ReportMetric(float64(metadata.detailCalls)/float64(b.N), "detail/batch")
		})
	}
}
