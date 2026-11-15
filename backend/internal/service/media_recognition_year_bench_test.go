package service

import (
	"context"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

// 固定返回值与计数器避免基准自身累积查询历史，便于比较识别开销。
type mediaYearBenchMetadata struct {
	page                     tmdb.Page
	details                  map[int]*tmdb.Detail
	seasons                  map[int]*tmdb.SeasonDetail
	browseCalls, detailCalls int
	seasonCalls              int
}

func (f *mediaYearBenchMetadata) Browse(ctx context.Context, _ tmdb.Options) (*tmdb.Page, error) {
	f.browseCalls++
	return &f.page, ctx.Err()
}

func (f *mediaYearBenchMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	f.detailCalls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if detail := f.details[id]; detail != nil && detail.Kind == kind {
		return detail, nil
	}
	return nil, tmdb.ErrNotFound
}

func (f *mediaYearBenchMetadata) Season(ctx context.Context, _ int, number int) (*tmdb.SeasonDetail, error) {
	f.seasonCalls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if season := f.seasons[number]; season != nil {
		return season, nil
	}
	return nil, tmdb.ErrNotFound
}

func (f *mediaYearBenchMetadata) AlternativeNames(ctx context.Context, _ string, _ int) ([]string, error) {
	return nil, ctx.Err()
}

func BenchmarkMediaRecognitionYearEvidence(b *testing.B) {
	for _, tc := range []struct {
		name, year, firstYear, seasonYear string
		id, season                        int
	}{
		{"ExplicitID", "2021", "2021", "2021", 126398, 0},
		{"FirstAirYear", "2013", "2013", "2021", 0, 2},
		{"SeasonAirYear", "2021", "2013", "2021", 0, 2},
		{"CrossYearSeason", "2022", "2013", "2021", 0, 2},
		{"CrossYearSpecials", "2022", "2013", "2021", 0, 0},
	} {
		b.Run(tc.name, func(b *testing.B) {
			const id = 126398
			item := tmdb.Item{ID: id, Kind: "tv", Title: "Wonderland", Date: tc.firstYear + "-05-23"}
			metadata := &mediaYearBenchMetadata{
				page: tmdb.Page{Items: []tmdb.Item{item}, TotalPages: 1},
				details: map[int]*tmdb.Detail{id: {Item: item, Seasons: []tmdb.SeasonSummary{
					{Number: tc.season, Date: tc.seasonYear + "-12-20"},
				}}},
				seasons: map[int]*tmdb.SeasonDetail{tc.season: {Number: tc.season, Episodes: []tmdb.Episode{
					{Number: 1, Date: "2021-12-20"}, {Number: 2, Date: "2022-01-02"},
				}}},
			}
			identity := mediafacts.Identity{Name: "Wonderland", Year: tc.year, Kind: "tv", TMDBID: tc.id, Season: tc.season, SeasonExplicit: true}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				match, _, err := recognizeMediaWithDecision(ctx, metadata, identity)
				if err != nil || match == nil || match.Detail.ID != id || match.Season != tc.season {
					b.Fatalf("识别结果错误: match=%+v err=%v", match, err)
				}
			}
			b.ReportMetric(float64(metadata.browseCalls)/float64(b.N), "search/op")
			b.ReportMetric(float64(metadata.detailCalls)/float64(b.N), "detail/op")
			b.ReportMetric(float64(metadata.seasonCalls)/float64(b.N), "season/op")
		})
	}
}

func BenchmarkMediaRecognitionWonderlandBatch(b *testing.B) {
	entries := wonderlandBatchEntries(289)
	metadata := wonderlandYearMetadata()
	organizer := &MediaOrganizer{metadata: metadata}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run := MediaOrganizeRun{Rule: MediaOrganizeRule{Source: "/strm/待整理"}}
		mediaAppendScanEntries(&run, entries)
		if err := organizer.recognize(ctx, &run, nil); err != nil {
			b.Fatal(err)
		}
		if len(run.Groups) != 1 || len(run.Entries) != 289 || run.Groups[0].TMDBID == 0 {
			b.Fatalf("批量作品未归并: groups=%d entries=%d", len(run.Groups), len(run.Entries))
		}
	}
	b.ReportMetric(float64(metadata.browseCalls)/float64(b.N), "search/batch")
	b.ReportMetric(float64(metadata.detailCalls)/float64(b.N), "detail/batch")
	b.ReportMetric(float64(metadata.seasonCalls)/float64(b.N), "season/batch")
}

func BenchmarkMediaRecognitionFilenameFacts(b *testing.B) {
	for _, name := range []string{
		"Wonderland.2021.S00E169.2160p.WEB-DL.60fps.HEVC.DDP 2.0-WF.strm",
		"Movie (2021).2160p.WEB-DL.HEVC.mkv",
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				facts := mediaOrganizeFilenameFacts(name)
				if facts.Identity.Name == "" || facts.Identity.Year != "2021" {
					b.Fatal("文件事实解析结果不正确", facts)
				}
			}
		})
	}
}
