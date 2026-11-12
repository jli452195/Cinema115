package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/bangumidata"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

type animeNamingLinkFake struct {
	result bangumidata.Result
	err    error
}

func (f animeNamingLinkFake) Lookup(context.Context, string) (bangumidata.Result, error) {
	return f.result, f.err
}

func TestAnimeNamingExactLinksAndSearchFallback(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		link                        animeNamingLinkFake
		wantSource, wantRecommended string
		wantNotice                  bool
	}{
		{"精确作品关联", animeNamingLinkFake{result: bangumidata.Result{Candidates: []bangumidata.Candidate{{TMDBID: "117061"}}}}, "bangumi-data", "117061", false},
		{"重复作品不同季仍为一个候选", animeNamingLinkFake{result: bangumidata.Result{Candidates: []bangumidata.Candidate{{TMDBID: "117061"}, {TMDBID: "117061"}}}}, "bangumi-data", "117061", false},
		{"无关联使用名称", animeNamingLinkFake{}, "name", "117061", false},
		{"上游失败提示回退", animeNamingLinkFake{err: errors.New("network")}, "name", "117061", true},
		{"过期关联不可自动采用", animeNamingLinkFake{result: bangumidata.Result{Stale: true, Candidates: []bangumidata.Candidate{{TMDBID: "99"}}}}, "name", "117061", true},
		{"失效关联不能推荐", animeNamingLinkFake{result: bangumidata.Result{Candidates: []bangumidata.Candidate{{TMDBID: "99"}, {TMDBID: "117061"}}}}, "bangumi-data", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := &animeNamingFake{}
			s := &AnimeNamingService{Metadata: metadata, Links: tc.link}
			got, err := s.SearchSubject(context.Background(), "584847", "侦探已死")
			if err != nil || got.Source != tc.wantSource || got.RecommendedID != tc.wantRecommended || (got.Notice != "") != tc.wantNotice {
				t.Fatal(got, err)
			}
		})
	}
}

type animeChapterFake struct {
	items []bangumi.Episode
	err   error
	stale bool
	calls int
}

func (f *animeChapterFake) Episodes(_ context.Context, _ string, offset int) (bangumi.EpisodesResult, error) {
	f.calls++
	first, last := min(offset, len(f.items)), min(offset+bangumi.EpisodePageSize, len(f.items))
	return bangumi.EpisodesResult{CacheMeta: bangumi.CacheMeta{Stale: f.stale}, Total: len(f.items), Offset: offset, Items: f.items[first:last]}, f.err
}

type animeMatchMetadata struct{ animeNamingFake }

func (f *animeMatchMetadata) AnimeSeason(_ context.Context, _ int, season int) (*tmdb.SeasonDetail, error) {
	// 两季标题相同，日期证据决定章节归属，而不是总集数或“第二季”文字。
	date := time.Date(2021, 7, 1, 0, 0, 0, 0, time.UTC)
	if season == 2 {
		date = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	}
	episodes := make([]tmdb.Episode, 13)
	for i := range episodes {
		episodes[i] = tmdb.Episode{ID: season*100 + i + 1, Number: i + 1, Name: fmt.Sprintf("第%d集", i+1), Date: date.AddDate(0, 0, i*7).Format("2006-01-02"), SourceSeason: season, SourceNumber: i + 1}
	}
	return &tmdb.SeasonDetail{Number: season, Episodes: episodes}, nil
}

func matchChapterFixture() *animeChapterFake {
	f := &animeChapterFake{}
	for i := 1; i <= 13; i++ {
		f.items = append(f.items, bangumi.Episode{ID: fmt.Sprint(i), Number: float64(i + 170), Date: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, (i-1)*7).Format("2006-01-02")})
	}
	return f
}

func TestAnimeNamingChapterSuggestionDoesNotChangeFingerprintOrSources(t *testing.T) {
	s := &AnimeNamingService{Metadata: &animeMatchMetadata{}}
	in := AnimeNamingTargetInput{TMDBID: "117061", BangumiID: "567896"}
	view, err := s.TargetWithChapters(context.Background(), matchChapterFixture(), in, "", "作品")
	if err != nil || view.ChapterMapping == nil || view.ChapterMapping.Status != "verified" || view.Season != 2 || view.ChapterMapping.Offset != -170 {
		t.Fatal(view, err)
	}
	season := 2
	plain, err := s.Target(context.Background(), AnimeNamingTargetInput{TMDBID: in.TMDBID, Season: &season}, "")
	if err != nil || plain.Fingerprint != view.Fingerprint {
		t.Fatal("建议污染命名指纹", plain, view, err)
	}
	_, err = s.Resolve(context.Background(), model.AnimeNamingInput{TMDBID: in.TMDBID, Season: 2, Fingerprint: view.Fingerprint, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 0, SourceBase: 0, TargetBase: 0, TargetSeason: 2}}}}}, nil)
	if err != nil {
		t.Fatal("额外章节读取破坏正常保存核验", err)
	}
	manual := 1
	in.Season = &manual
	kept, err := s.TargetWithChapters(context.Background(), matchChapterFixture(), in, "", "作品")
	if err != nil || kept.Season != 1 || kept.ChapterMapping.Status != "verified" || kept.ChapterMapping.Season != 2 {
		t.Fatal("覆盖了明确选择的季", kept, err)
	}
}

func TestAnimeNamingChapterFailuresRemainAdvisory(t *testing.T) {
	s := &AnimeNamingService{Metadata: &animeMatchMetadata{}}
	for _, kind := range []string{"failure", "stale", "too_many"} {
		t.Run(kind, func(t *testing.T) {
			reader := matchChapterFixture()
			switch kind {
			case "failure":
				reader.err = errors.New("public metadata offline")
			case "stale":
				reader.stale = true
			case "too_many":
				reader.items = make([]bangumi.Episode, 301)
			}
			view, err := s.TargetWithChapters(context.Background(), reader, AnimeNamingTargetInput{TMDBID: "117061", BangumiID: "567896"}, "", "作品")
			if err != nil || len(view.Fingerprint) != 64 || view.ChapterMapping == nil || view.ChapterMapping.Status != "unresolved" || strings.TrimSpace(view.ChapterMapping.Reason) == "" {
				t.Fatal(view, err)
			}
			if reader.calls != 1 {
				t.Fatal("失败后继续抓取", reader.calls)
			}
		})
	}
}
