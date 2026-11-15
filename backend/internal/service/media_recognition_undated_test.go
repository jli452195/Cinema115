package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

func TestMediaRecognitionUndatedCandidates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		identity  mediafacts.Identity
		items     []tmdb.Item
		seasons   map[string][]tmdb.SeasonSummary
		genres    map[string][]tmdb.Genre
		wantID    int
		wantKind  string
		wantCode  string
		wantCalls int
	}{
		{name: "唯一无年份电影", identity: mediafacts.Identity{Name: "Wonderland", Kind: "movie"}, items: []tmdb.Item{{ID: 1, Kind: "movie", Title: "Wonderland"}}, wantID: 1, wantKind: "movie", wantCode: "TITLE_EXACT", wantCalls: 1},
		{name: "同名异年不按搜索顺序选择", identity: mediafacts.Identity{Name: "Wonderland", Kind: "tv", Season: 1}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Wonderland", Date: "2013-01-01"}, {ID: 2, Kind: "tv", Title: "Wonderland", Date: "2021-01-01"}}, wantCode: "AMBIGUOUS_TITLE", wantCalls: 2},
		{name: "明确季号排除没有该季的作品", identity: mediafacts.Identity{Name: "Show", Kind: "tv", Season: 5, SeasonExplicit: true}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Show"}, {ID: 2, Kind: "tv", Title: "Show"}}, seasons: map[string][]tmdb.SeasonSummary{"tv:1": {{Number: 1}}, "tv:2": {{Number: 5}}}, wantID: 2, wantKind: "tv", wantCode: "TITLE_EXACT", wantCalls: 2},
		{name: "特别季存在即可无需日期或集数上限", identity: mediafacts.Identity{Name: "Show", Kind: "tv", Season: 0, SeasonExplicit: true}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Show"}}, seasons: map[string][]tmdb.SeasonSummary{"tv:1": {{Number: 0, EpisodeCount: 1}}}, wantID: 1, wantKind: "tv", wantCode: "TITLE_EXACT", wantCalls: 1},
		{name: "明确缺季拒绝", identity: mediafacts.Identity{Name: "Show", Kind: "tv", Season: 5, SeasonExplicit: true}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Show"}}, seasons: map[string][]tmdb.SeasonSummary{"tv:1": {{Number: 1}}}, wantCode: "SEASON_MISMATCH", wantCalls: 1},
		{name: "空季资料不能断言不存在", identity: mediafacts.Identity{Name: "Show", Kind: "tv", Season: 5, SeasonExplicit: true}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Show"}}, wantCode: "SEASON_METADATA_INSUFFICIENT", wantCalls: 1},
		{name: "同名候选缺季资料不能自动选另一项", identity: mediafacts.Identity{Name: "Show", Kind: "tv", Season: 5, SeasonExplicit: true}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Show"}, {ID: 2, Kind: "tv", Title: "Show"}}, seasons: map[string][]tmdb.SeasonSummary{"tv:2": {{Number: 5}}}, wantCode: "SEASON_METADATA_INSUFFICIENT", wantCalls: 2},
		{name: "明确类型消除同名歧义", identity: mediafacts.Identity{Name: "三体", Kind: "tv", ContentType: "live_action"}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "三体"}, {ID: 2, Kind: "tv", Title: "三体"}}, genres: map[string][]tmdb.Genre{"tv:1": {{ID: 16}}, "tv:2": {{ID: 18}}}, wantID: 2, wantKind: "tv", wantCode: "TITLE_EXACT", wantCalls: 2},
		{name: "同ID不同类型仍是不同候选", identity: mediafacts.Identity{Name: "Show"}, items: []tmdb.Item{{ID: 1, Kind: "movie", Title: "Show"}, {ID: 1, Kind: "tv", Title: "Show"}}, wantCode: "AMBIGUOUS_TITLE", wantCalls: 2},
		{name: "已知电影类型排除剧集", identity: mediafacts.Identity{Name: "Show", Kind: "movie"}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Show"}, {ID: 1, Kind: "movie", Title: "Show"}}, wantID: 1, wantKind: "movie", wantCode: "TITLE_EXACT", wantCalls: 1},
		{name: "重复结果不构成歧义", identity: mediafacts.Identity{Name: "Show", Kind: "movie"}, items: []tmdb.Item{{ID: 1, Kind: "movie", Title: "Show"}, {ID: 1, Kind: "movie", Title: "Show"}}, wantID: 1, wantKind: "movie", wantCode: "TITLE_EXACT", wantCalls: 1},
		{name: "有年份保留首合格性能", identity: mediafacts.Identity{Name: "Show", Year: "2021", Kind: "tv", Season: 5, SeasonExplicit: true}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "Show", Date: "2021-01-01"}, {ID: 2, Kind: "tv", Title: "Show", Date: "2021-01-01"}}, wantID: 1, wantKind: "tv", wantCode: "TITLE_EXACT", wantCalls: 1},
		{name: "双语完整名称分别匹配", identity: mediafacts.Identity{Name: "你好，星期六 Hello Saturday", ChineseTitle: "你好，星期六", EnglishTitle: "Hello Saturday", Kind: "tv", Season: 5, SeasonExplicit: true}, items: []tmdb.Item{{ID: 154770, Kind: "tv", Title: "你好，星期六"}}, seasons: map[string][]tmdb.SeasonSummary{"tv:154770": {{Number: 5}}}, wantID: 154770, wantKind: "tv", wantCode: "TITLE_EXACT", wantCalls: 1},
		{name: "双语译名不能被当作季副标题", identity: mediafacts.Identity{Name: "中文作品 English Work", ChineseTitle: "中文作品", EnglishTitle: "English Work", Kind: "tv", Season: 1}, items: []tmdb.Item{{ID: 1, Kind: "tv", Title: "中文作品"}}, wantID: 1, wantKind: "tv", wantCode: "TITLE_EXACT", wantCalls: 1},
		{name: "双语变体不同作品不得猜测", identity: mediafacts.Identity{Name: "中文名 English Name", ChineseTitle: "中文名", EnglishTitle: "English Name", Kind: "movie"}, items: []tmdb.Item{{ID: 1, Kind: "movie", Title: "中文名"}, {ID: 2, Kind: "movie", Title: "English Name"}}, wantCode: "AMBIGUOUS_TITLE", wantCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMediaDecisionMetadata(tc.items...)
			for key, seasons := range tc.seasons {
				f.details[key].Seasons = seasons
			}
			for key, genres := range tc.genres {
				f.details[key].Genres = genres
			}
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, tc.identity)
			if err != nil || decision.ResultCode != tc.wantCode || f.detailCalls != tc.wantCalls {
				t.Fatalf("match=%+v decision=%+v err=%v details=%d", match, decision, err, f.detailCalls)
			}
			if tc.wantID == 0 {
				if match != nil {
					t.Fatalf("歧义或证据不足不能自动选择: %+v", match)
				}
			} else if match == nil || match.Detail.ID != tc.wantID || match.Detail.Kind != tc.wantKind {
				t.Fatalf("唯一候选识别错误: %+v", match)
			}
		})
	}
}

func TestMediaRecognitionUndatedPaginationAndAliases(t *testing.T) {
	for _, tc := range []struct {
		name       string
		second     tmdb.Item
		totalPages int
		wantCode   string
		wantMatch  bool
	}{
		{"跨页同名也必须判歧义", tmdb.Item{ID: 2, Kind: "movie", Title: "Show"}, 2, "AMBIGUOUS_TITLE", false},
		{"扫描完两页的唯一作品", tmdb.Item{ID: 2, Kind: "movie", Title: "Other"}, 2, "TITLE_EXACT", true},
		{"超过页数上限不冒充唯一", tmdb.Item{ID: 2, Kind: "movie", Title: "Other"}, 3, "SEARCH_INCOMPLETE", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := tmdb.Item{ID: 1, Kind: "movie", Title: "Show"}
			f := newMediaDecisionMetadata(first, tc.second)
			f.pages = map[int]*tmdb.Page{1: {Items: []tmdb.Item{first}, TotalPages: tc.totalPages}, 2: {Items: []tmdb.Item{tc.second}, TotalPages: tc.totalPages}}
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Show", Kind: "movie"})
			if err != nil || (match != nil) != tc.wantMatch || decision.ResultCode != tc.wantCode || len(f.queries) != 2 {
				t.Fatalf("分页唯一性错误: match=%+v decision=%+v err=%v queries=%d", match, decision, err, len(f.queries))
			}
		})
	}
	for _, count := range []int{1, 2, 4} {
		t.Run("别名候选数-"+strconv.Itoa(count), func(t *testing.T) {
			items := make([]tmdb.Item, count)
			for i := range items {
				items[i] = tmdb.Item{ID: i + 1, Kind: "movie", Title: "Original " + strconv.Itoa(i)}
			}
			f := newMediaDecisionMetadata(items...)
			f.aliases["movie:1"] = []string{"Search Name"}
			if count == 2 {
				f.aliases["movie:2"] = []string{"Search Name"}
			}
			wantCode := "ALIAS_EXACT"
			if count == 2 {
				wantCode = "AMBIGUOUS_TITLE"
			} else if count == 4 {
				wantCode = "SEARCH_INCOMPLETE"
			}
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Search Name", Kind: "movie"})
			if err != nil || decision.ResultCode != wantCode || (match != nil) != (count == 1) {
				t.Fatalf("别名唯一性错误: match=%+v decision=%+v err=%v", match, decision, err)
			}
		})
	}
}

func TestMediaRecognitionUndatedRejectsFuzzyAndUnknownCompetitor(t *testing.T) {
	t.Run("无年份不自动接受拼写近似", func(t *testing.T) {
		f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "movie", Title: "Interstellar"})
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Intersteller", Kind: "movie"})
		if err != nil || match != nil || decision.ResultCode != "NO_ELIGIBLE_CANDIDATE" || f.detailCalls != 0 {
			t.Fatalf("无年份模糊名称不应自动整理: %+v %+v %v", match, decision, err)
		}
	})
	t.Run("第二个相关候选查询失败必须保留错误", func(t *testing.T) {
		f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "movie", Title: "Show"}, tmdb.Item{ID: 2, Kind: "movie", Title: "Show"})
		f.detailError["movie:2"] = context.DeadlineExceeded
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Show", Kind: "movie"})
		if match != nil || !errors.Is(err, context.DeadlineExceeded) || decision.ResultCode != "METADATA_QUERY_FAILED" {
			t.Fatalf("未核实另一候选不能断言唯一: %+v %+v %v", match, decision, err)
		}
	})
	t.Run("精确标题优先且不额外读取不相关别名", func(t *testing.T) {
		f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "movie", Title: "Show"}, tmdb.Item{ID: 2, Kind: "movie", Title: "Original"})
		f.aliasError = context.DeadlineExceeded
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Show", Kind: "movie"})
		if err != nil || match == nil || match.Detail.ID != 1 || decision.ResultCode != "TITLE_EXACT" || f.aliasCalls != 0 {
			t.Fatalf("精确层唯一结果不受别名层影响: %+v %+v %v", match, decision, err)
		}
	})
}

func TestMediaRecognitionUndatedWeakHintsAndTruncation(t *testing.T) {
	t.Run("详情核验预算不足不能报告候选唯一", func(t *testing.T) {
		items := make([]tmdb.Item, mediaRecognitionDetailLimit+1)
		for i := range items {
			items[i] = tmdb.Item{ID: i + 1, Kind: "tv", Title: "Show"}
		}
		f := newMediaDecisionMetadata(items...)
		for i, detail := range f.details {
			if i == "tv:1" {
				detail.Genres = []tmdb.Genre{{ID: 18}}
			} else {
				detail.Genres = []tmdb.Genre{{ID: 16}}
			}
		}
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Show", Kind: "tv", ContentType: "live_action"})
		if err != nil || match != nil || decision.ResultCode != "SEARCH_INCOMPLETE" || f.detailCalls != mediaRecognitionDetailLimit {
			t.Fatalf("详情上限不能证明候选唯一: %+v %+v %v calls=%d", match, decision, err, f.detailCalls)
		}
	})
	t.Run("丢弃弱目录年份后必须核对唯一候选", func(t *testing.T) {
		f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "tv", Title: "Show"}, tmdb.Item{ID: 2, Kind: "tv", Title: "Show"})
		for _, detail := range f.details {
			detail.Seasons = []tmdb.SeasonSummary{{Number: 1}}
		}
		identity := mediafacts.Identity{Name: "Show", Year: "2021", YearFromParent: true, ParentName: "Uploads", Kind: "tv", Season: 1, SeasonExplicit: true}
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, identity)
		if err != nil || match != nil || decision.ResultCode != "AMBIGUOUS_TITLE" {
			t.Fatalf("弱目录年份不应绕过唯一性: %+v %+v %v", match, decision, err)
		}
	})
	t.Run("未知类型ID仍需命名空间唯一", func(t *testing.T) {
		f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "tv", Title: "Show"}, tmdb.Item{ID: 1, Kind: "movie", Title: "Show"})
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Show", TMDBID: 1, Season: 1})
		if err != nil || match != nil || decision.ResultCode != "AMBIGUOUS_TITLE" {
			t.Fatalf("无命名空间的ID不能当作强ID: %+v %+v %v", match, decision, err)
		}
	})
	t.Run("数字标题未查完不能改用年份解释", func(t *testing.T) {
		item := tmdb.Item{ID: 1, Kind: "movie", Title: "Known Film", Date: "2020-01-01"}
		f := newMediaDecisionMetadata(item)
		f.pages = map[int]*tmdb.Page{1: {Items: []tmdb.Item{item}, TotalPages: 3}, 2: {TotalPages: 3}}
		match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.ParseIdentity("Known Film 2020.mkv"))
		if err != nil || match != nil || decision.ResultCode != "SEARCH_INCOMPLETE" || len(f.queries) != 2 {
			t.Fatalf("无年份解释未查完不得备用年份抢先通过: %+v %+v %v", match, decision, err)
		}
	})
	for _, truncated := range []string{"别名数量", "别名长度"} {
		t.Run(truncated+"裁剪不能证明唯一", func(t *testing.T) {
			f := newMediaDecisionMetadata(tmdb.Item{ID: 1, Kind: "movie", Title: "Original A"}, tmdb.Item{ID: 2, Kind: "movie", Title: "Original B"})
			f.aliases["movie:1"] = []string{"Shared Alias"}
			if truncated == "别名数量" {
				f.aliases["movie:2"] = make([]string, 51)
				f.aliases["movie:2"][50] = "Shared Alias"
			} else {
				f.aliases["movie:2"] = []string{strings.Repeat("a", 201)}
			}
			match, decision, err := recognizeMediaWithDecision(context.Background(), f, mediafacts.Identity{Name: "Shared Alias", Kind: "movie"})
			if err != nil || match != nil || decision.ResultCode != "SEARCH_INCOMPLETE" {
				t.Fatalf("裁剪后不得冒充别名候选唯一: %+v %+v %v", match, decision, err)
			}
		})
	}
}

func BenchmarkMediaRecognitionUndated(b *testing.B) {
	for _, count := range []int{1, 2} {
		name := "Unique"
		if count == 2 {
			name = "Ambiguous"
		}
		b.Run(name, func(b *testing.B) {
			items := make([]tmdb.Item, count)
			details := make(map[int]*tmdb.Detail, count)
			for i := range items {
				items[i] = tmdb.Item{ID: i + 1, Kind: "tv", Title: "Show"}
				details[i+1] = &tmdb.Detail{Item: items[i], Seasons: []tmdb.SeasonSummary{{Number: 5}}}
			}
			metadata := &mediaYearBenchMetadata{page: tmdb.Page{Items: items, TotalPages: 1}, details: details}
			identity := mediafacts.Identity{Name: "Show", Kind: "tv", Season: 5, SeasonExplicit: true}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				match, decision, err := recognizeMediaWithDecision(context.Background(), metadata, identity)
				if err != nil || (match != nil) != (count == 1) || count == 2 && decision.ResultCode != "AMBIGUOUS_TITLE" {
					b.Fatalf("识别结果错误: %+v %+v %v", match, decision, err)
				}
			}
			b.ReportMetric(float64(metadata.browseCalls)/float64(b.N), "search/op")
			b.ReportMetric(float64(metadata.detailCalls)/float64(b.N), "detail/op")
			b.ReportMetric(float64(metadata.seasonCalls)/float64(b.N), "season/op")
		})
	}
}
