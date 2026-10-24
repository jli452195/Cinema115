package model

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestAnimeNamingProjectionContract(t *testing.T) {
	data, err := os.ReadFile("testdata/anime_naming_preview.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Plan  AnimeNamingPlan `json:"plan"`
		Cases []struct {
			Name      string                `json:"name"`
			SourceRef string                `json:"sourceRef"`
			Parse     AnimeReleaseParse     `json:"parse"`
			Expected  AnimeNamingProjection `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Plan.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, example := range fixture.Cases {
		t.Run(example.Name, func(t *testing.T) {
			got := fixture.Plan.Project(example.SourceRef, example.Parse)
			if !reflect.DeepEqual(got, example.Expected) {
				t.Fatalf("got %+v; want %+v", got, example.Expected)
			}
		})
	}
}

func namingNumber(n int) *int { return &n }
func namingPlan() AnimeNamingPlan {
	ep := make([]AnimeNamingEpisode, 12)
	for i := range ep {
		ep[i] = AnimeNamingEpisode{ID: "123" + strings.Repeat("0", i), Number: i + 1, Name: "分集"}
	}
	return AnimeNamingPlan{SchemaVersion: 1, Target: AnimeNamingTarget{TMDBID: "117061", Title: "侦探已死", Year: "2021", Season: 2, GroupName: "TMDB 默认", Fingerprint: strings.Repeat("a", 64), Seasons: []AnimeNamingSeason{{Number: 2, Episodes: ep}, {Number: 0, Episodes: []AnimeNamingEpisode{{ID: "901", Number: 1, Name: "特典"}}}}}, Sources: []AnimeSourceMapping{{SourceRef: "main", Segments: []AnimeMappingSegment{{First: 25, Last: namingNumber(36), SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}, {SourceRef: "standby", Segments: []AnimeMappingSegment{{First: 1, SourceBase: 1, TargetSeason: 2, TargetBase: 1}}}}}
}
func TestAnimeNamingPerSourceAndDownloadMinimum(t *testing.T) {
	p := namingPlan()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ref    string
		n, ep  int
		status string
	}{{"main", 30, 6, "mapped"}, {"main", 24, 0, "unmapped"}, {"main", 37, 0, "unmapped"}, {"standby", 6, 6, "mapped"}, {"standby", 13, 13, "mapped"}} {
		got := p.Project(tc.ref, AnimeReleaseParse{Kind: "episode", Episode: &tc.n})
		if got.Status != tc.status || got.Episode != nil && *got.Episode != tc.ep {
			t.Fatalf("%+v: %+v", tc, got)
		}
	}
	if got := p.Project("main", AnimeReleaseParse{Kind: "special", RawEpisode: "12.5"}); got.Status != "unmapped" {
		t.Fatal(got)
	}
	p.Sources[0].Overrides = []AnimeMappingOverride{{RawKind: "special", RawNumber: "12.5", Action: "map", TargetSeason: 0, TargetEpisode: 1}}
	got := p.Project("main", AnimeReleaseParse{Kind: "special", RawEpisode: "12.5"})
	if got.Status != "mapped" || *got.Season != 0 {
		t.Fatal(got)
	}
}
func TestAnimeNamingRejectsOverlapCollisionAndFalseChapter(t *testing.T) {
	for _, change := range []func(*AnimeNamingPlan){
		func(p *AnimeNamingPlan) {
			p.Sources[0].Segments = append(p.Sources[0].Segments, AnimeMappingSegment{First: 30, SourceBase: 30, TargetBase: 1, TargetSeason: 3})
		},
		func(p *AnimeNamingPlan) {
			p.Sources[0].Overrides = []AnimeMappingOverride{{RawKind: "special", RawNumber: "SP1", Action: "map", TargetSeason: 2, TargetEpisode: 1}}
		},
		func(p *AnimeNamingPlan) { p.Target.Seasons[0].Episodes[1].Number = 1 },
		func(p *AnimeNamingPlan) { p.Target.Fingerprint = strings.Repeat("z", 64) },
	} {
		p := namingPlan()
		change(&p)
		if p.Validate() == nil {
			t.Fatal("invalid plan accepted", p)
		}
	}
	b := AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &AnimeScope{Kind: "feed"}, Review: &AnimeSourceReview{Method: "source_review", ItemID: "4100", GroupID: "583"}}
	if err := b.Validate(true); err != nil {
		t.Fatal(err)
	}
	b.ChapterCorrespondence = &AnimeChapterCorrespondence{SourceEpisode: 1, ChapterEpisode: 1}
	if b.Validate(true) == nil {
		t.Fatal("feed selection must not assert chapter numbering")
	}
}

func TestAnimeNamingOrdinaryOverrideNormalizesPaddingAndVersion(t *testing.T) {
	p := namingPlan()
	p.Sources[0].Overrides = []AnimeMappingOverride{{RawKind: "episode", RawNumber: "25", Action: "ignore"}}
	for _, raw := range []string{"25", "025", "25v2"} {
		n := 25
		if got := p.Project("main", AnimeReleaseParse{Kind: "episode", RawEpisode: raw, Episode: &n}); got.Status != "ignored" {
			t.Fatal(raw, got)
		}
	}
}
