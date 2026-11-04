package service

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

// 固定元数据样本只验证匹配策略，不访问远程服务或实际文件。
type mediaDecisionMetadata struct {
	items       []tmdb.Item
	details     map[string]*tmdb.Detail
	aliases     map[string][]string
	detailError map[string]error
	browseError error
	aliasError  error
	queries     []tmdb.Options
	detailCalls int
	aliasCalls  int
	pages       map[int]*tmdb.Page
}

func newMediaDecisionMetadata(items ...tmdb.Item) *mediaDecisionMetadata {
	f := &mediaDecisionMetadata{items: items, details: make(map[string]*tmdb.Detail, len(items)), aliases: make(map[string][]string), detailError: make(map[string]error)}
	for _, item := range items {
		f.details[item.Kind+":"+strconv.Itoa(item.ID)] = &tmdb.Detail{Item: item}
	}
	return f
}

func (f *mediaDecisionMetadata) Browse(ctx context.Context, options tmdb.Options) (*tmdb.Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.queries = append(f.queries, options)
	if f.browseError != nil {
		return nil, f.browseError
	}
	if f.pages != nil {
		if page := f.pages[options.Page]; page != nil {
			return page, nil
		}
	}
	return &tmdb.Page{Items: f.items, Page: options.Page, TotalPages: 1}, nil
}

func (f *mediaDecisionMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	f.detailCalls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := kind + ":" + strconv.Itoa(id)
	if err := f.detailError[key]; err != nil {
		return nil, err
	}
	if detail := f.details[key]; detail != nil {
		return detail, nil
	}
	return nil, tmdb.ErrNotFound
}

func (f *mediaDecisionMetadata) AlternativeNames(ctx context.Context, kind string, id int) ([]string, error) {
	f.aliasCalls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.aliases[kind+":"+strconv.Itoa(id)], f.aliasError
}

func (f *mediaDecisionMetadata) Season(context.Context, int, int) (*tmdb.SeasonDetail, error) {
	return &tmdb.SeasonDetail{}, nil
}

func TestMediaRecognitionShortUndatedTitleAndFirstEligible(t *testing.T) {
	t.Run("short-title", func(t *testing.T) {
		f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "tv", Title: "三体", Date: "2023-01-15"})
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("三体.S01E01.mkv"))
		if err != nil || match == nil || match.Detail.ID != 1 || decision.ResultCode != "TITLE_EXACT" || f.detailCalls != 1 {
			t.Fatalf("短片名无年份应匹配: match=%+v decision=%+v err=%v", match, decision, err)
		}
	})
	t.Run("first-eligible", func(t *testing.T) {
		f := newMediaDecisionMetadata(
			tmdb.Item{ID: 3, Kind: "tv", Title: "其他作品", Date: "2021-01-01"},
			tmdb.Item{ID: 110070, Kind: "tv", Title: "堀与宫村", Date: "2021-01-10"},
			tmdb.Item{ID: 113980, Kind: "tv", Title: "堀与宫村", Date: "2021-02-16"},
		)
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("堀与宫村 (2021) - S01E01.mkv"))
		if err != nil || match == nil || match.Detail.ID != 110070 || f.detailCalls != 1 {
			t.Fatalf("应跳过无关首条，选中后停止额外详情请求: match=%+v decision=%+v err=%v", match, decision, err)
		}
	})
	t.Run("duplicate-alias", func(t *testing.T) {
		f := newMediaCollectionMetadata()
		other := &tmdb.Detail{Item: tmdb.Item{ID: 999, Kind: "tv", Title: "Another work", Date: "2014-10-05"}}
		f.details[999], f.items = other, append(f.items, other.Item)
		f.aliases[999] = []string{"Nanatsu no Taizai"}
		// 别名有界查询目前只看前 3 个排名，本样本将另一条放入该范围。
		f.items = []tmdb.Item{f.items[0], other.Item}
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Nanatsu no Taizai", Kind: "tv", Season: 1})
		if err != nil || match == nil || match.Detail.ID != 62104 || decision.ResultCode != "ALIAS_EXACT" {
			t.Fatalf("同别名采用首合格条目: match=%+v decision=%+v err=%v", match, decision, err)
		}
	})
}

func TestMediaRecognitionExplicitContentTypeAndConflict(t *testing.T) {
	for _, contentType := range []string{"", "animation", "live_action"} {
		t.Run(contentType, func(t *testing.T) {
			f := newMediaDecisionMetadata(
				tmdb.Item{ID: 1, Kind: "tv", Title: "堀与宫村", Date: "2021-01-10"},
				tmdb.Item{ID: 2, Kind: "tv", Title: "堀与宫村", Date: "2021-02-16"},
			)
			f.details["tv:1"].Genres = []tmdb.Genre{{ID: 16, Name: "动画"}}
			f.details["tv:2"].Genres = []tmdb.Genre{{ID: 35, Name: "喜剧"}}
			identity := mediafacts.Identity{Name: "堀与宫村", Year: "2021", Kind: "tv", Season: 1, ContentType: contentType}
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, identity)
			wanted := 1
			if contentType == "live_action" {
				wanted = 2
			}
			if err != nil || match == nil || match.Detail.ID != wanted {
				t.Fatalf("类型证据筛选异常: match=%+v decision=%+v err=%v", match, decision, err)
			}
		})
	}
	f := newMediaDecisionMetadata()
	_, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Work", Conflict: "TMDB_ID_CONFLICT"})
	if err != nil || decision.ResultCode != "DECLARATION_CONFLICT" || len(f.queries) != 0 {
		t.Fatalf("明确声明冲突不能执行搜索: %+v err=%v", decision, err)
	}
}

func TestMediaRecognitionFuzzyBoundsAndExactPriority(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		match       bool
	}{
		{"Intersteller", "Interstellar", true},
		{"Interstellar", "The Martian", false},
		{"John Wick 3", "John Wick 2", false},
		{"Nanatsu no TaizaiZ", "Nanatsu no Taizai", false},
		{"三体", "三休", false},
	} {
		t.Run(tc.name+"/"+tc.title, func(t *testing.T) {
			f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "movie", Title: tc.title, Date: "2014-01-01"})
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: tc.name, Kind: "movie", Season: 1})
			if err != nil || (match != nil) != tc.match {
				t.Fatalf("模糊边界异常: match=%+v decision=%+v err=%v", match, decision, err)
			}
			if tc.match && decision.ResultCode != "TITLE_FUZZY" {
				t.Fatalf("模糊匹配结果错误: %+v", decision)
			}
		})
	}
	t.Run("exact-before-fuzzy-and-alias-io", func(t *testing.T) {
		f := newMediaDecisionMetadata(
			tmdb.Item{ID: 1, Kind: "movie", Title: "Interstellar", Date: "2014-01-01"},
			tmdb.Item{ID: 2, Kind: "movie", Title: "Intersteller", Date: "2014-01-01"},
		)
		f.aliasError = context.DeadlineExceeded
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Intersteller", Kind: "movie", Season: 1})
		if err != nil || match == nil || match.Detail.ID != 2 || decision.ResultCode != "TITLE_EXACT" || f.aliasCalls != 0 {
			t.Fatalf("精确结果不能受前置模糊项或别名异常影响: %+v %+v %v", match, decision, err)
		}
	})
}

func TestMediaRecognitionNumericTitleAlternativeAndUnknownIDKind(t *testing.T) {
	t.Run("numeric-title-first", func(t *testing.T) {
		f := newMediaDecisionMetadata(
			tmdb.Item{ID: 1, Kind: "movie", Title: "Blade Runner", Date: "2049-01-01"},
			tmdb.Item{ID: 2, Kind: "movie", Title: "Blade Runner 2049", Date: "2017-10-01"},
		)
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("Blade Runner 2049.mkv"))
		if err != nil || match == nil || match.Detail.ID != 2 || len(f.queries) != 1 || f.queries[0].Query != "Blade Runner 2049" {
			t.Fatalf("完整数字片名应优先: match=%+v decision=%+v err=%v", match, decision, err)
		}
	})
	t.Run("alternative-year", func(t *testing.T) {
		f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "movie", Title: "Known Film", Date: "2020-01-01"})
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("Known Film 2020.mkv"))
		if err != nil || match == nil || match.Detail.ID != 1 || len(f.queries) != 2 || f.queries[1].Query != "Known Film" {
			t.Fatalf("实际裸年份仍有备用解释: match=%+v decision=%+v err=%v", match, decision, err)
		}
	})
	t.Run("same-id-different-kind", func(t *testing.T) {
		f := newMediaDecisionMetadata(
			tmdb.Item{ID: 1, Kind: "tv", Title: "Different Show", Date: "2014-01-01"},
			tmdb.Item{ID: 1, Kind: "movie", Title: "Known Film", Date: "2014-01-01"},
		)
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Known Film", TMDBID: 1, Season: 1})
		if err != nil || match == nil || match.Detail.Kind != "movie" || len(f.queries) != 1 || f.queries[0].Kind != "multi" {
			t.Fatalf("未知类型ID不能跨命名空间误选: %+v %+v %v", match, decision, err)
		}
	})
}

func TestMediaRecognitionQueryErrorsDoNotBecomeNoResult(t *testing.T) {
	t.Run("browse", func(t *testing.T) {
		f := newMediaDecisionMetadata()
		f.browseError = context.DeadlineExceeded
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Known Film"})
		if match != nil || !errors.Is(err, context.DeadlineExceeded) || decision.ResultCode != "METADATA_QUERY_FAILED" {
			t.Fatalf("查询异常不能记作无结果: %+v %+v %v", match, decision, err)
		}
	})
	for _, failedID := range []int{1, 2} {
		t.Run("detail-"+strconv.Itoa(failedID), func(t *testing.T) {
			f := newMediaDecisionMetadata(
				tmdb.Item{ID: 1, Kind: "movie", Title: "Known Film", Date: "2014-01-01"},
				tmdb.Item{ID: 2, Kind: "movie", Title: "Known Film", Date: "2014-01-01"},
			)
			f.detailError["movie:"+strconv.Itoa(failedID)] = context.DeadlineExceeded
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Known Film", Kind: "movie", Season: 1})
			if failedID == 1 {
				if match != nil || !errors.Is(err, context.DeadlineExceeded) || decision.ResultCode != "METADATA_QUERY_FAILED" {
					t.Fatalf("首个相关候选未核实，不能静默选择后项: %+v %+v %v", match, decision, err)
				}
				return
			}
			if err != nil || match == nil || match.Detail.ID != 1 || decision.ResultCode != "TITLE_EXACT" {
				t.Fatalf("已确认首条不受后项异常影响: %+v %+v %v", match, decision, err)
			}
		})
	}
}

func TestMediaRecognitionBoundedSearch(t *testing.T) {
	items := make([]tmdb.Item, 20)
	for i := range items {
		items[i] = tmdb.Item{ID: i + 1, Kind: "movie", Title: "Unrelated " + strconv.Itoa(i), Date: "2000-01-01"}
	}
	f := newMediaDecisionMetadata(items...)
	f.pages = map[int]*tmdb.Page{1: {Items: items, TotalPages: 100}, 2: {Items: items, TotalPages: 100}}
	_, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Missing Film", AlternativeName: "Another Missing Film", AlternativeYear: "2020", ParentName: "Missing Series Subtitle", Kind: "tv"})
	if err != nil || len(f.queries) > mediaRecognitionSearchLimit || f.aliasCalls > mediaRecognitionAliasLimit || f.detailCalls > mediaRecognitionDetailLimit {
		t.Fatalf("查询必须有界: queries=%d aliases=%d details=%d decision=%+v err=%v", len(f.queries), f.aliasCalls, f.detailCalls, decision, err)
	}
}

func TestMediaRecognitionStopsAfterChosenCandidate(t *testing.T) {
	items := make([]tmdb.Item, 40)
	for i := range items {
		items[i] = tmdb.Item{ID: i + 1, Kind: "movie", Title: "Unrelated " + strconv.Itoa(i), Date: "2014-01-01"}
	}
	items[0].Title = "Known Film"
	f := newMediaDecisionMetadata(items...)
	f.pages = map[int]*tmdb.Page{
		1: {Items: items[:20], TotalPages: 2},
		2: {Items: items[20:], TotalPages: 2},
	}
	match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Missing Film 2014", AlternativeName: "Known Film", AlternativeYear: "2014", Kind: "movie", Season: 1})
	if err != nil || match == nil || match.Detail.ID != 1 || f.detailCalls != 1 {
		t.Fatalf("选中后不应继续请求候选详情: %+v %+v %v", match, decision, err)
	}
}
