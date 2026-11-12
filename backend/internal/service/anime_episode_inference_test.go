package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/model"
)

func inferenceChapters(first, last int, startDay int) []bangumi.Episode {
	items := make([]bangumi.Episode, 0, last-first+1)
	start := time.Date(2026, time.October, startDay, 0, 0, 0, 0, time.UTC)
	for n := first; n <= last; n++ {
		date := start.AddDate(0, 0, (n-first)*7).Format("2006-01-02")
		items = append(items, bangumi.Episode{ID: fmt.Sprint(n), Number: float64(n), Title: fmt.Sprintf("具体篇章标题 %d", n), Date: date})
	}
	return items
}

func inferenceSeason(season, first, last int, startDay int) model.AnimeNamingSeason {
	items := make([]model.AnimeNamingEpisode, 0, last-first+1)
	start := time.Date(2026, time.October, startDay, 0, 0, 0, 0, time.UTC)
	for n := first; n <= last; n++ {
		date := start.AddDate(0, 0, (n-first)*7).Format("2006-01-02")
		items = append(items, model.AnimeNamingEpisode{ID: fmt.Sprintf("%d", season*100+n), Number: n, Name: fmt.Sprintf("具体篇章标题 %d", n), Date: date})
	}
	return model.AnimeNamingSeason{Number: season, Episodes: items}
}

func TestInferAnimeEpisodesSupportsOffsetAndSplitCours(t *testing.T) {
	tests := []struct {
		name       string
		chapters   []bangumi.Episode
		season     model.AnimeNamingSeason
		wantOffset int
		wantFirst  int
	}{
		{name: "split cour retains first season numbering", chapters: inferenceChapters(14, 24, 1), season: inferenceSeason(1, 1, 25, -90), wantOffset: 0, wantFirst: 14},
		{name: "season restarts after full series numbering", chapters: inferenceChapters(171, 183, 1), season: inferenceSeason(2, 1, 26, 1), wantOffset: -170, wantFirst: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := InferAnimeEpisodes(test.chapters, []model.AnimeNamingSeason{test.season})
			if got.Status != "verified" || got.Offset != test.wantOffset || got.TargetFirst != test.wantFirst || got.Matched < 2 || got.Compared != len(test.chapters) {
				t.Fatalf("unexpected inference: %+v", got)
			}
		})
	}
}

func TestInferAnimeEpisodesDoesNotAssumeEqualCounts(t *testing.T) {
	chapters := inferenceChapters(14, 24, 1)
	season := inferenceSeason(1, 14, 25, 1)
	got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season})
	if got.Status != "verified" || got.TargetFirst != 14 || got.TargetLast != 24 {
		t.Fatalf("unequal current counts blocked date alignment: %+v", got)
	}
}

func TestInferAnimeEpisodesRequiresIndependentEvidence(t *testing.T) {
	chapters := inferenceChapters(1, 3, 1)
	season := inferenceSeason(1, 1, 3, 1)
	chapters = chapters[:1]
	if got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season}); got.Status != "unresolved" {
		t.Fatalf("single anchor was accepted: %+v", got)
	}
	chapters = inferenceChapters(1, 3, 1)
	for i := range chapters {
		chapters[i].Date = ""
		chapters[i].Title = fmt.Sprintf("第%d集", i+1)
		chapters[i].OriginalTitle = ""
	}
	for i := range season.Episodes {
		season.Episodes[i].Date = ""
		season.Episodes[i].Name = fmt.Sprintf("Episode %d", i+1)
	}
	if got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season}); got.Status != "unresolved" {
		t.Fatalf("generic title labels were treated as evidence: %+v", got)
	}
	if got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season, inferenceSeason(2, 1, 3, 1)}); got.Status != "unresolved" {
		t.Fatalf("ambiguous seasons were accepted: %+v", got)
	}
}

func TestInferAnimeEpisodesAllowsTitleOnlyAndGaps(t *testing.T) {
	chapters := inferenceChapters(20, 23, 1)
	season := inferenceSeason(2, 1, 3, 1)
	for i := range chapters {
		chapters[i].Date = ""
		if i < len(season.Episodes) {
			chapters[i].Title = season.Episodes[i].Name
		}
	}
	for i := range season.Episodes {
		season.Episodes[i].Date = ""
	}
	got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season})
	if got.Status != "partial" || got.Offset != -19 || got.Matched != 3 || got.TargetFirst != 1 {
		t.Fatalf("exact title anchors did not produce a partial mapping: %+v", got)
	}
}

func TestInferAnimeEpisodesRejectsConflictsAndAmbiguity(t *testing.T) {
	chapters := inferenceChapters(1, 3, 1)
	season := inferenceSeason(1, 1, 3, 1)
	season.Episodes[2].Date = "2026-11-03"
	if got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season}); got.Status != "conflict" {
		t.Fatalf("contradictory mapped date was accepted: %+v", got)
	}

	chapters = inferenceChapters(1, 3, 1)
	chapters[2].Number = 2.5
	if got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{inferenceSeason(1, 1, 3, 1)}); got.Status != "conflict" {
		t.Fatalf("fractional chapter sort was accepted: %+v", got)
	}

	chapters = inferenceChapters(1, 2, 1)
	seasons := []model.AnimeNamingSeason{inferenceSeason(1, 1, 2, 1), inferenceSeason(2, 1, 2, 1)}
	if got := InferAnimeEpisodes(chapters, seasons); got.Status != "unresolved" {
		t.Fatalf("two matching seasons were not left for manual choice: %+v", got)
	}
}

func TestInferAnimeEpisodesMarksMissingTMDBTailPartial(t *testing.T) {
	chapters := inferenceChapters(171, 183, 1)
	season := inferenceSeason(2, 1, 8, 1)
	got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season})
	if got.Status != "partial" || got.Offset != -170 || got.TargetLast != 13 {
		t.Fatalf("missing TMDB tail was reported complete: %+v", got)
	}
}

func TestInferAnimeEpisodesUsesNumbersRatherThanListPosition(t *testing.T) {
	chapters := inferenceChapters(171, 175, 1)
	chapters = []bangumi.Episode{chapters[4], chapters[0], chapters[2]}
	season := inferenceSeason(2, 1, 5, 1)
	got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season})
	if got.Status != "verified" || got.Offset != -170 || got.Pairs[1].BangumiNumber != 173 || got.Pairs[1].TMDBEpisode != 3 {
		t.Fatalf("gaps or ordering were treated as sequential numbering: %+v", got)
	}
}

func TestInferAnimeEpisodesRejectsDuplicateIdentityOrDates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]bangumi.Episode, *model.AnimeNamingSeason)
		want   string
	}{
		{name: "duplicate Bangumi sort", want: "conflict", mutate: func(chapters []bangumi.Episode, _ *model.AnimeNamingSeason) {
			chapters[1].Number = chapters[0].Number
		}},
		{name: "duplicate TMDB number", want: "conflict", mutate: func(_ []bangumi.Episode, season *model.AnimeNamingSeason) {
			season.Episodes[1].Number = season.Episodes[0].Number
		}},
		{name: "duplicate TMDB identity", want: "conflict", mutate: func(_ []bangumi.Episode, season *model.AnimeNamingSeason) {
			season.Episodes[1].ID = season.Episodes[0].ID
		}},
		{name: "duplicate episode dates", want: "unresolved", mutate: func(chapters []bangumi.Episode, season *model.AnimeNamingSeason) {
			for i := range chapters {
				chapters[i].Title = ""
				chapters[i].Date = "2026-10-01"
				season.Episodes[i].Name = ""
				season.Episodes[i].Date = "2026-10-01"
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chapters := inferenceChapters(1, 3, 1)
			season := inferenceSeason(1, 1, 3, 1)
			test.mutate(chapters, &season)
			if got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season}); got.Status != test.want {
				t.Fatalf("unexpected inference: %+v", got)
			}
		})
	}
}

func TestInferAnimeEpisodesRejectsSingleChapterEvidenceInAnotherSeason(t *testing.T) {
	chapters := inferenceChapters(1, 3, 1)
	firstSeason := inferenceSeason(1, 1, 3, 1)
	otherSeason := inferenceSeason(2, 1, 1, 15)
	otherSeason.Episodes[0].Name = "另一季不同标题"
	got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{firstSeason, otherSeason})
	if got.Status != "unresolved" {
		t.Fatalf("one explicit chapter in another season was ignored: %+v", got)
	}
}

func TestInferAnimeEpisodesDateToleranceAndTitleConflict(t *testing.T) {
	chapters := inferenceChapters(14, 16, 1)
	season := inferenceSeason(1, 14, 16, 2)
	got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season})
	if got.Status != "verified" || got.Offset != 0 {
		t.Fatalf("one day timezone difference failed: %+v", got)
	}
	payload, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(payload), `"offset":0`) {
		t.Fatalf("zero offset disappeared from API response: %s, %v", payload, err)
	}
	season.Episodes[0].Name, season.Episodes[2].Name = season.Episodes[2].Name, season.Episodes[0].Name
	if got := InferAnimeEpisodes(chapters, []model.AnimeNamingSeason{season}); got.Status != "conflict" {
		t.Fatalf("dates and titles pointing to different episodes were accepted: %+v", got)
	}
}

func TestInferAnimeEpisodesNeverMapsRegularChaptersToSpecials(t *testing.T) {
	got := InferAnimeEpisodes(inferenceChapters(1, 3, 1), []model.AnimeNamingSeason{inferenceSeason(0, 1, 3, 1)})
	if got.Status != "unresolved" {
		t.Fatalf("regular chapters were promoted into special mappings: %+v", got)
	}
}
