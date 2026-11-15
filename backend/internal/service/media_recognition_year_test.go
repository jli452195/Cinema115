package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

// 固定季资料和请求计数，同时验证作品准确性与补查开销。
type mediaYearMetadata struct {
	*mediaDecisionMetadata
	seasons      map[mediaRecognitionSeasonKey]*tmdb.SeasonDetail
	seasonErrors map[mediaRecognitionSeasonKey]error
	seasonCalls  map[mediaRecognitionSeasonKey]int
}

func newMediaYearMetadata(items ...tmdb.Item) *mediaYearMetadata {
	return &mediaYearMetadata{
		mediaDecisionMetadata: newMediaDecisionMetadata(items...),
		seasons:               make(map[mediaRecognitionSeasonKey]*tmdb.SeasonDetail),
		seasonErrors:          make(map[mediaRecognitionSeasonKey]error),
		seasonCalls:           make(map[mediaRecognitionSeasonKey]int),
	}
}

func (f *mediaYearMetadata) Season(ctx context.Context, id, number int) (*tmdb.SeasonDetail, error) {
	key := mediaRecognitionSeasonKey{id: id, season: number}
	f.seasonCalls[key]++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.seasons[key], f.seasonErrors[key]
}

func TestMediaRecognitionExplicitSeasonYearEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, file, parent string
		summaries          []tmdb.SeasonSummary
		season             *tmdb.SeasonDetail
		seasonError        error
		wantCode           string
		wantCalls          int
		wantSeason         int
		wantMatch          bool
	}{
		{name: "首播年份无需季资料", file: "Show.2013.S00E9999.mkv", wantCode: "TITLE_EXACT", wantMatch: true},
		{name: "无年份缺季资料待确认且不补查日期", file: "Show.S00E169.mkv", wantCode: "SEASON_METADATA_INSUFFICIENT"},
		{name: "明确ID不按日期换作品", file: "Show.2021.{tmdb-42}.S00E169.mkv", wantCode: "TMDB_ID", wantMatch: true},
		{name: "对应季年份直接通过", file: "Show.2021.S02E99.mkv", summaries: []tmdb.SeasonSummary{{Number: 2, Date: "2021-12-01", EpisodeCount: 1}}, wantCode: "TITLE_EXACT", wantSeason: 2, wantMatch: true},
		{name: "特别篇跨年", file: "Show.2021.S00E169.mkv", summaries: []tmdb.SeasonSummary{{Number: 0, Date: "2014-01-01"}}, season: &tmdb.SeasonDetail{Number: 0, Episodes: []tmdb.Episode{{Number: 1, Date: "2014-01-01"}, {Number: 2, Date: "2021-01-01"}}}, wantCode: "TITLE_EXACT", wantCalls: 1, wantMatch: true},
		{name: "普通季跨年", file: "Show.2021.S02E99.mkv", summaries: []tmdb.SeasonSummary{{Number: 2, Date: "2020-12-01"}}, season: &tmdb.SeasonDetail{Number: 2, Episodes: []tmdb.Episode{{Date: "2020-12-01"}, {Date: "2021-01-01"}}}, wantCode: "TITLE_EXACT", wantCalls: 1, wantSeason: 2, wantMatch: true},
		{name: "第一季跨年", file: "Show.2014.S01E99.mkv", summaries: []tmdb.SeasonSummary{{Number: 1, Date: "2013-12-01"}}, season: &tmdb.SeasonDetail{Number: 1, Episodes: []tmdb.Episode{{Date: "2014-01-01"}}}, wantCode: "TITLE_EXACT", wantCalls: 1, wantSeason: 1, wantMatch: true},
		{name: "缺少特别季拒绝撞名", file: "Show.2021.S00E169.mkv", summaries: []tmdb.SeasonSummary{{Number: 1, Date: "2021-01-01"}}, wantCode: "SEASON_METADATA_INSUFFICIENT"},
		{name: "其他季年份不替代当前季", file: "Show.2021.S02E169.mkv", summaries: []tmdb.SeasonSummary{{Number: 2, Date: "2014-01-01"}, {Number: 3, Date: "2021-01-01"}}, season: &tmdb.SeasonDetail{Number: 2, Episodes: []tmdb.Episode{{Date: "2014-01-01"}}}, wantCode: "YEAR_MISMATCH", wantCalls: 1},
		{name: "空季资料", file: "Show.2021.S00E169.mkv", summaries: []tmdb.SeasonSummary{{Number: 0}}, wantCode: "SEASON_METADATA_INSUFFICIENT", wantCalls: 1},
		{name: "404资料不足", file: "Show.2021.S00E169.mkv", summaries: []tmdb.SeasonSummary{{Number: 0}}, seasonError: tmdb.ErrNotFound, wantCode: "SEASON_METADATA_INSUFFICIENT", wantCalls: 1},
		{name: "返回季号不符", file: "Show.2021.S00E169.mkv", summaries: []tmdb.SeasonSummary{{Number: 0}}, season: &tmdb.SeasonDetail{Number: 1, Episodes: []tmdb.Episode{{Date: "2021-01-01"}}}, wantCode: "SEASON_METADATA_INSUFFICIENT", wantCalls: 1},
		{name: "空日期不作为证据", file: "Show.2021.S00E169.mkv", summaries: []tmdb.SeasonSummary{{Number: 0}}, season: &tmdb.SeasonDetail{Number: 0, Episodes: []tmdb.Episode{{Date: "2014-01-01"}, {Date: ""}}}, wantCode: "SEASON_METADATA_INSUFFICIENT", wantCalls: 1},
		{name: "文件年份覆盖目录年份", file: "Show.2021.S00E169.mkv", parent: "Show (2013)", wantCode: "SEASON_METADATA_INSUFFICIENT"},
		{name: "作品目录年份仍需核实", file: "Show.S00E169.mkv", parent: "Show (2021)", wantCode: "SEASON_METADATA_INSUFFICIENT"},
		{name: "跨语言明确目录年份不丢失", file: "Show.S00E169.mkv", parent: "中文剧名 (2021)", wantCode: "SEASON_METADATA_INSUFFICIENT"},
		{name: "无关目录裸年份保持弱提示但明确季必须存在", file: "Show.S02E169.mkv", parent: "Unrelated Show 2021", summaries: []tmdb.SeasonSummary{{Number: 2}}, wantCode: "TITLE_EXACT", wantSeason: 2, wantMatch: true},
		{name: "已核实副标题不能使不存在的明确季通过", file: "Show.S02E169.mkv", parent: "Show Later Chapter (2021)", summaries: []tmdb.SeasonSummary{{Number: 3, Name: "第 3 季 Later Chapter", Date: "2021-01-01"}}, wantCode: "SEASON_MISMATCH", wantSeason: 2},
		{name: "地区版前缀不能冒充季副标题", file: "Show.S01E169.mkv", parent: "Show Australia (2021)", wantCode: "SEASON_METADATA_INSUFFICIENT", wantSeason: 1},
		{name: "第一季仍核实相关目录裸年份", file: "Show.S01E169.mkv", parent: "Show Australia 2021", wantCode: "SEASON_METADATA_INSUFFICIENT", wantSeason: 1},
		{name: "未核实副标题不能清空强年份", file: "Show.S02E169.mkv", parent: "Show Later Chapter (2021)", wantCode: "SEASON_METADATA_INSUFFICIENT", wantSeason: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMediaYearMetadata(tmdb.Item{ID: 42, Kind: "tv", Title: "Show", Date: "2013-01-01"})
			f.details["tv:42"].Seasons = tc.summaries
			identity := mediafacts.ParseIdentity(tc.file, tc.parent)
			key := mediaRecognitionSeasonKey{id: 42, season: identity.Season}
			f.seasons[key], f.seasonErrors[key] = tc.season, tc.seasonError
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, identity)
			if err != nil || (match != nil) != tc.wantMatch || decision.ResultCode != tc.wantCode || f.seasonCalls[key] != tc.wantCalls {
				t.Fatalf("match=%+v decision=%+v err=%v calls=%v identity=%+v", match, decision, err, f.seasonCalls, identity)
			}
			if match != nil && (match.Detail.ID != 42 || match.Season != tc.wantSeason) {
				t.Fatalf("年份校验不能改变作品或明确季号: %+v", match)
			}
		})
	}
}

func TestMediaRecognitionSeasonYearErrorsAndReuse(t *testing.T) {
	for _, queryError := range []error{nil, tmdb.ErrNotFound, context.DeadlineExceeded, context.Canceled, errors.New("读取失败")} {
		t.Run(fmt.Sprint(queryError), func(t *testing.T) {
			f := newMediaYearMetadata(tmdb.Item{ID: 42, Kind: "tv", Title: "Show", Date: "2013-01-01"})
			f.details["tv:42"].Seasons = []tmdb.SeasonSummary{{Number: 0}}
			key := mediaRecognitionSeasonKey{id: 42, season: 0}
			f.seasonErrors[key] = queryError
			identity := mediafacts.ParseIdentity("Show.2021.S00E169.mkv", "Different Work (2021)")
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, identity)
			if match != nil || f.seasonCalls[key] != 1 {
				t.Fatalf("拒绝候选应复用季资料，不跨片名或匹配层级重复查询: match=%+v calls=%v", match, f.seasonCalls)
			}
			if queryError != nil && !errors.Is(queryError, tmdb.ErrNotFound) {
				if !errors.Is(err, queryError) || decision.ResultCode != "METADATA_QUERY_FAILED" {
					t.Fatalf("查询异常不能归类为无结果: decision=%+v err=%v", decision, err)
				}
			} else if err != nil || decision.ResultCode != "SEASON_METADATA_INSUFFICIENT" {
				t.Fatalf("未取得日期不能匹配: decision=%+v err=%v", decision, err)
			}
		})
	}
}

func TestMediaRecognitionSeasonYearBudget(t *testing.T) {
	for _, withValid := range []bool{false, true} {
		t.Run(fmt.Sprint(withValid), func(t *testing.T) {
			items := make([]tmdb.Item, 8)
			for i := range items {
				items[i] = tmdb.Item{ID: i + 1, Kind: "tv", Title: "Show", Date: "2013-01-01"}
			}
			if withValid {
				items = append(items, tmdb.Item{ID: 99, Kind: "tv", Title: "Show", Date: "2021-01-01"})
			}
			f := newMediaYearMetadata(items...)
			for _, item := range items[:8] {
				f.details[fmt.Sprintf("tv:%d", item.ID)].Seasons = []tmdb.SeasonSummary{{Number: 0, Date: "2014-01-01"}}
				f.seasons[mediaRecognitionSeasonKey{id: item.ID, season: 0}] = &tmdb.SeasonDetail{Number: 0, Episodes: []tmdb.Episode{{Date: "2014-01-01"}}}
			}
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("Show.2021.S00E169.mkv"))
			calls := 0
			for _, count := range f.seasonCalls {
				calls += count
			}
			if err != nil || (match != nil) != withValid || calls != mediaRecognitionSeasonLimit {
				t.Fatalf("补查必须有界且不阻断无需补查的候选: match=%+v decision=%+v err=%v calls=%d", match, decision, err, calls)
			}
			if withValid && match.Detail.ID != 99 || !withValid && decision.ResultCode != "SEASON_METADATA_INSUFFICIENT" {
				t.Fatalf("查询额度不能变成自动放宽或虚假年份冲突: match=%+v decision=%+v", match, decision)
			}
		})
	}
}

func TestMediaRecognitionYearRejectsWrongFirstCandidate(t *testing.T) {
	wrong := tmdb.Item{ID: 48936, Kind: "tv", Title: "Wonderland", Date: "2013-08-21"}
	right := tmdb.Item{ID: 126398, Kind: "tv", Title: "五十公里桃花坞", OriginalTitle: "Wonderland", Date: "2021-05-23"}
	f := newMediaYearMetadata(wrong, right)
	match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("Wonderland.2021.S00E169.strm"))
	if err != nil || match == nil || match.Detail.ID != right.ID || match.Season != 0 || f.detailCalls != 2 || len(f.seasonCalls) != 0 {
		t.Fatalf("应跳过年份不符的首候选，继续查找正确作品: match=%+v decision=%+v err=%v", match, decision, err)
	}
}

func TestMediaRecognitionSeasonYearBatchReuse(t *testing.T) {
	f := newMediaYearMetadata(tmdb.Item{ID: 42, Kind: "tv", Title: "Wonderland", Date: "2013-01-01"})
	f.details["tv:42"].Seasons = []tmdb.SeasonSummary{{Number: 0, Date: "2021-01-01"}}
	key := mediaRecognitionSeasonKey{id: 42, season: 0}
	f.seasons[key] = &tmdb.SeasonDetail{Number: 0, Episodes: []tmdb.Episode{{Number: 1, Date: "2022-01-01"}}}
	entries := wonderlandBatchEntries(289)
	for i := range entries {
		file := &entries[i].File
		file.Name = strings.ReplaceAll(file.Name, "Wonderland.2021.", "Wonderland.2022.")
		file.SourcePath = "Show/S*******/" + file.Name
		mediaOrganizeFileNumbering(file)
	}
	run := MediaOrganizeRun{Rule: MediaOrganizeRule{Source: "/strm/待整理"}}
	mediaAppendScanEntries(&run, entries)
	s := &MediaOrganizer{metadata: f}
	if err := s.recognize(context.Background(), &run, nil); err != nil {
		t.Fatal(err)
	}
	if len(run.Groups) != 1 || run.Groups[0].TMDBID != 42 || len(f.queries) != 1 || f.detailCalls != 1 || f.seasonCalls[key] != 1 {
		t.Fatalf("无ID跨年特别篇也应整组复用一次季核验: groups=%+v queries=%d details=%d seasons=%v", run.Groups, len(f.queries), f.detailCalls, f.seasonCalls)
	}
	for i, entry := range run.Entries {
		if entry.File.Season != 0 || entry.File.Episode != i+1 {
			t.Fatalf("季日期证据不能充当集数上限或改写编号: %+v", entry)
		}
	}
}

func TestMediaRecognitionAlternativeYearDoesNotInheritParentDeclaration(t *testing.T) {
	f := newMediaYearMetadata(tmdb.Item{ID: 42, Kind: "movie", Title: "Known Film", Date: "2020-01-01"})
	identity := mediafacts.ParseIdentity("Known Film 2020.mkv", "Unrelated Work (2021)")
	match, decision, err := recognizeMediaWithDecision(context.Background(), f, identity)
	if err != nil || match == nil || match.Detail.ID != 42 || len(f.queries) != 2 {
		t.Fatalf("备用文件年份不应继续使用目录的年份来源或强声明: match=%+v decision=%+v err=%v identity=%+v", match, decision, err, identity)
	}
}

func TestMediaRecognitionSeasonYearWithoutSeasonReader(t *testing.T) {
	f := &mediaRecognitionFake{items: []tmdb.Item{{ID: 42, Kind: "tv", Title: "Work", Date: "2013-01-01"}}}
	match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("Work.2021.S00E169.mkv"))
	if err != nil || match != nil || decision.ResultCode != "SEASON_METADATA_INSUFFICIENT" {
		t.Fatalf("不支持季资料的来源也不能放行年份冲突: match=%+v decision=%+v err=%v", match, decision, err)
	}
}

type mediaYearNumberingKey struct{}

// 模拟详情返回后管理员切换编排，后续季日期仍须使用固定的旧选择。
type mediaYearPinnedMetadata struct {
	*mediaYearMetadata
	revision, pins, seasonRevision int
}

func (f *mediaYearPinnedMetadata) PinNumbering(ctx context.Context, _ int) (context.Context, error) {
	f.pins++
	return context.WithValue(ctx, mediaYearNumberingKey{}, f.revision), nil
}

func (f *mediaYearPinnedMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	result, err := f.mediaYearMetadata.Details(ctx, kind, id)
	f.revision++
	return result, err
}

func (f *mediaYearPinnedMetadata) Season(ctx context.Context, _ int, number int) (*tmdb.SeasonDetail, error) {
	f.seasonRevision, _ = ctx.Value(mediaYearNumberingKey{}).(int)
	date := "2022-01-01"
	if f.seasonRevision == 1 {
		date = "2021-01-01"
	}
	return &tmdb.SeasonDetail{Number: number, Episodes: []tmdb.Episode{{Date: date}}}, nil
}

func TestMediaRecognitionSeasonYearPinsNumbering(t *testing.T) {
	f := &mediaYearPinnedMetadata{mediaYearMetadata: newMediaYearMetadata(tmdb.Item{ID: 42, Kind: "tv", Title: "Show", Date: "2013-01-01"}), revision: 1}
	f.details["tv:42"].Seasons = []tmdb.SeasonSummary{{Number: 0, Date: "2014-01-01"}}
	match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("Show.2021.S00E169.mkv"))
	if err != nil || match == nil || f.pins != 1 || f.seasonRevision != 1 || f.revision != 2 {
		t.Fatalf("详情和季日期必须来自同一编排: match=%+v decision=%+v err=%v pins=%d seasonRevision=%d", match, decision, err, f.pins, f.seasonRevision)
	}
}
