package service

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

func TestMatchResourceCoverage(t *testing.T) {
	target := MediaIdentity{Kind: "tv", TMDBID: 60843, Title: "请问您今天要来点兔子吗？", Year: "2014"}
	missing := []EpisodeRef{{Season: 2, Episode: 4, Label: "S02E04"}}
	tests := []struct {
		name, title, tmdbID, coverage string
		matched                       bool
	}{
		{"season range", "[日番] 请问您今天要来点兔子吗？ (2014) {tmdb-60843} 1080p REMUX S01-S03 简中", "60843", "explicit", true},
		{"special through third", "[日番] 请问您今天要来点兔子吗？ (2014) S00-S03 4K 中字", "", "explicit", true},
		{"exact episode", "[日番] 请问您今天要来点兔子吗？ (2014) S02E04", "", "explicit", true},
		{"episode miss", "[日番] 请问您今天要来点兔子吗？ (2014) S02E01-E03", "", "explicit", false},
		{"other season", "[日番] 请问您今天要来点兔子吗？ (2014) S03", "", "explicit", false},
		{"whole resource", "[日番] 请问您今天要来点兔子吗？ (2014) 4K 59.30G", "", "all", true},
		{"wrong TMDB", "[日番] 请问您今天要来点兔子吗？ (2014) {tmdb-1} S01-S03", "1", "explicit", false},
		{"movie conflict", "[动画电影] 请问您今天要来点兔子吗？ (2014) {tmdb-60843}", "60843", "all", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := MatchResource(target, missing, ResourceCandidate{ID: tc.name, Title: tc.title, TMDBID: tc.tmdbID})
			if decision.Matched != tc.matched || decision.Coverage != tc.coverage {
				t.Fatalf("unexpected decision: %+v", decision)
			}
		})
	}
}

func TestParseResourceTitleCanonicalSpans(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  EpisodeSpan
	}{
		{"剧集 (2020) S01-S03", EpisodeSpan{StartSeason: 1, EndSeason: 3, WholeSeason: true}},
		{"剧集 (2020) S02E01-E06", EpisodeSpan{StartSeason: 2, StartEpisode: 1, EndSeason: 2, EndEpisode: 6}},
		{"剧集 (2020) S01E01-S03E12", EpisodeSpan{StartSeason: 1, StartEpisode: 1, EndSeason: 3, EndEpisode: 12}},
	} {
		parsed := ParseResourceTitle(ResourceCandidate{Title: tc.title})
		if len(parsed.Spans) != 1 || parsed.Spans[0] != tc.want {
			t.Fatalf("%q spans = %+v, want %+v", tc.title, parsed.Spans, tc.want)
		}
	}
}

func TestMatchResourceWholeSeriesExamples(t *testing.T) {
	missing := []EpisodeRef{{Season: 1, Episode: 903, Label: "S01E903"}}
	for _, tc := range []struct {
		target MediaIdentity
		title  string
	}{
		{MediaIdentity{Kind: "tv", TMDBID: 46260, Title: "火影忍者", Year: "2002"}, "[日番] 火影忍者 (2002) 4K 578.05G"},
		{MediaIdentity{Kind: "tv", TMDBID: 82169, Title: "天空之城", Year: "2018"}, "[日韩剧] 天空之城 (2018) 82.38G"},
	} {
		decision := MatchResource(tc.target, missing, ResourceCandidate{Title: tc.title})
		if !decision.Matched || decision.Coverage != "all" || len(decision.MatchedMissing) != 1 {
			t.Fatalf("whole-series resource not accepted: %+v", decision)
		}
	}
}

type resourceMetadataFake struct {
	detail    *tmdb.Detail
	schedules map[int]*tmdb.Schedule
}

func (f *resourceMetadataFake) Details(context.Context, string, int) (*tmdb.Detail, error) {
	return f.detail, nil
}
func (f *resourceMetadataFake) Schedule(_ context.Context, _ int, season int) (*tmdb.Schedule, error) {
	return f.schedules[season], nil
}

type resourceLibraryFake struct{ match model.EmbyMatch }

func (f *resourceLibraryFake) Matches(context.Context, []model.EmbyMatch, bool) ([]model.EmbyMatch, error) {
	return []model.EmbyMatch{f.match}, nil
}

func TestResourceMatchServiceBuildsMissingSnapshotOnce(t *testing.T) {
	metadata := &resourceMetadataFake{
		detail:    &tmdb.Detail{Item: tmdb.Item{ID: 60843, Kind: "tv", Title: "请问您今天要来点兔子吗？", Date: "2014-04-10"}, Seasons: []tmdb.SeasonSummary{{Number: 2, EpisodeCount: 4}}},
		schedules: map[int]*tmdb.Schedule{2: {Number: 2, Episodes: []tmdb.ScheduledEpisode{{Number: 1, Date: "2014-01-01"}, {Number: 2, Date: "2014-01-08"}, {Number: 3, Date: "2014-01-15"}, {Number: 4, Date: "2014-01-22"}}}},
	}
	library := &resourceLibraryFake{match: model.EmbyMatch{Kind: "tv", TMDB: "60843", State: "present", Items: []model.EmbyItem{{Type: "Episode", Season: 2, Episode: 1, EndEpisode: 3}}}}
	matcher := NewResourceMatchService(metadata, library)
	matcher.now = func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local) }
	batch, err := matcher.Match(context.Background(), "tv", 60843, []ResourceCandidate{
		{ID: "range", Title: "[日番] 请问您今天要来点兔子吗？ (2014) S01-S03"},
		{ID: "miss", Title: "[日番] 请问您今天要来点兔子吗？ (2014) S03"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Missing) != 1 || batch.Missing[0].Label != "S02E04" || !batch.Results[0].Matched || batch.Results[1].Matched {
		t.Fatalf("unexpected batch: %+v", batch)
	}
}

func TestResourceMatchServiceMatchesMissingMovie(t *testing.T) {
	metadata := &resourceMetadataFake{detail: &tmdb.Detail{Item: tmdb.Item{ID: 378064, Kind: "movie", Title: "死侍", Date: "2016-02-09"}}}
	library := &resourceLibraryFake{match: model.EmbyMatch{Kind: "movie", TMDB: "378064", State: "absent"}}
	matcher := NewResourceMatchService(metadata, library)
	batch, err := matcher.Match(context.Background(), "movie", 378064, []ResourceCandidate{{ID: "movie", Title: "[外语电影] 死侍 (2016) 4K"}})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Target.Kind != "movie" || len(batch.Missing) != 0 || len(batch.Results) != 1 || !batch.Results[0].Matched {
		t.Fatalf("unexpected movie batch: %+v", batch)
	}

	library.match.State = "present"
	batch, err = matcher.Match(context.Background(), "movie", 378064, []ResourceCandidate{{ID: "movie", Title: "[外语电影] 死侍 (2016) 4K"}})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Results[0].Matched || batch.Results[0].Reason != "当前电影已入库" {
		t.Fatalf("present movie should not match: %+v", batch.Results[0])
	}
}

func TestResourceMatchServiceIgnoresSpecialsAndFutureSeasons(t *testing.T) {
	metadata := &resourceMetadataFake{
		detail: &tmdb.Detail{
			Item: tmdb.Item{ID: 9, Kind: "tv", Title: "测试剧集", Date: "2020-01-01"},
			Seasons: []tmdb.SeasonSummary{
				{Number: 0, EpisodeCount: 1, Date: "2020-01-01"},
				{Number: 1, EpisodeCount: 1, Date: "2020-01-01"},
				{Number: 2, EpisodeCount: 8, Date: "2027-01-01"},
			},
		},
		schedules: map[int]*tmdb.Schedule{1: {Number: 1, Episodes: []tmdb.ScheduledEpisode{{Number: 1, Date: "2020-01-01"}}}},
	}
	matcher := NewResourceMatchService(metadata, &resourceLibraryFake{match: model.EmbyMatch{Kind: "tv", TMDB: "9", State: "absent"}})
	matcher.now = func() time.Time { return time.Date(2026, 9, 16, 23, 30, 0, 0, time.FixedZone("UTC-8", -8*60*60)) }
	batch, err := matcher.Match(context.Background(), "tv", 9, []ResourceCandidate{{ID: "series", Title: "测试剧集 (2020)"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Missing) != 1 || batch.Missing[0].Label != "S01E01" || !batch.Results[0].Matched {
		t.Fatalf("unexpected regular-season snapshot: %+v", batch)
	}
}
