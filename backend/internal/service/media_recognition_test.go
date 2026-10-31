package service

import (
	"context"
	"fmt"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

// Synthetic metadata keeps collection regressions independent of remote APIs.
type mediaCollectionMetadata struct {
	queries []string
	items   []tmdb.Item
	details map[int]*tmdb.Detail
	aliases map[int][]string
}

func newMediaCollectionMetadata() *mediaCollectionMetadata {
	f := &mediaCollectionMetadata{
		details: map[int]*tmdb.Detail{
			62104: {
				Item: tmdb.Item{ID: 62104, Kind: "tv", Title: "七大罪", OriginalTitle: "七つの大罪", Date: "2014-10-05"},
				Seasons: []tmdb.SeasonSummary{
					{Number: 0, Name: "特别篇", Date: "2015-01-04"},
					{Number: 1, Name: "第 1 季 七大罪", Date: "2014-10-05"},
					{Number: 2, Name: "第 2 季 戒律的复活", Date: "2018-01-13"},
					{Number: 3, Name: "第 3 季 诸神的逆鳞", Date: "2019-10-09"},
					{Number: 4, Name: "第 4 季 愤怒的审判", Date: "2021-01-13"},
				},
			},
			101: {Item: tmdb.Item{ID: 101, Kind: "movie", Title: "七大罪剧场版：天空的囚人", Date: "2018-08-18"}},
			102: {Item: tmdb.Item{ID: 102, Kind: "movie", Title: "七大罪剧场版：光芒诅咒之人", Date: "2021-07-02"}},
		},
		aliases: map[int][]string{
			62104: {"Nanatsu no Taizai", "The Seven Deadly Sins"},
			101: {
				"The Seven Deadly Sins the Movie - Prisoners of the Sky",
				"Gekijouban Nanatsu no Taizai Tenkuu no Torawarebito",
			},
			102: {"The Seven Deadly Sins-Hikari ni Norowareshi Mono tachi"},
		},
	}
	for _, id := range []int{62104, 101, 102} {
		f.items = append(f.items, f.details[id].Item)
	}
	return f
}

func (f *mediaCollectionMetadata) Browse(ctx context.Context, options tmdb.Options) (*tmdb.Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.queries = append(f.queries, options.Query)
	items := make([]tmdb.Item, 0, len(f.items))
	for _, item := range f.items {
		if options.Kind == "multi" || options.Kind == "" || item.Kind == options.Kind {
			items = append(items, item)
		}
	}
	return &tmdb.Page{Items: items}, nil
}

func (f *mediaCollectionMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	detail := f.details[id]
	if detail == nil || detail.Kind != kind {
		return nil, tmdb.ErrNotFound
	}
	return detail, nil
}

func (f *mediaCollectionMetadata) AlternativeNames(ctx context.Context, _ string, id int) ([]string, error) {
	return f.aliases[id], ctx.Err()
}

func (f *mediaCollectionMetadata) Season(ctx context.Context, _ int, number int) (*tmdb.SeasonDetail, error) {
	out := &tmdb.SeasonDetail{Number: number, Episodes: make([]tmdb.Episode, 24)}
	for i := range out.Episodes {
		out.Episodes[i] = tmdb.Episode{Number: i + 1, Name: fmt.Sprintf("Episode %d", i+1)}
	}
	return out, ctx.Err()
}

type mediaFailingAliasMetadata struct {
	*mediaCollectionMetadata
	aliasCalls int
}

func (f *mediaFailingAliasMetadata) AlternativeNames(context.Context, string, int) ([]string, error) {
	f.aliasCalls++
	return nil, context.DeadlineExceeded
}

func TestMediaRecognitionDirectMatchSkipsUnrelatedAliases(t *testing.T) {
	f := &mediaFailingAliasMetadata{mediaCollectionMetadata: newMediaCollectionMetadata()}
	f.items = []tmdb.Item{
		{ID: 201, Kind: "movie", Title: "Unrelated", Date: "2019-01-01"},
		{ID: 202, Kind: "movie", Title: "Known Movie", Date: "2019-01-01"},
	}
	f.details[202] = &tmdb.Detail{Item: f.items[1]}
	got, err := recognizeMedia(context.Background(), f, mediafacts.Identity{Name: "Known Movie", Year: "2019", Season: 1})
	if err != nil || got == nil || got.Detail.ID != 202 || f.aliasCalls != 0 {
		t.Fatalf("unrelated alias lookup affected an exact title: match=%+v err=%v calls=%d", got, err, f.aliasCalls)
	}
}

func TestMediaRecognitionCollectionSeasons(t *testing.T) {
	for _, tc := range []struct {
		file, parent string
		season       int
		alias        string
	}{
		{"[BeanSub][Nanatsu_no_Taizai_Fundo_no_Shinpan][01][GB][1080P][x264_AAC].mp4", "七大罪 愤怒的审判 2021.全24集.1080P.日语中字", 4, ""},
		{"[BeanSub][Nanatsu_no_Taizai_Kamigami_no_Gekirin][01][GB][1080P][x264_AAC].mp4", "七大罪 众神的逆鳞.2019.全24集.1080P.日语中字", 0, ""},
		{"[BeanSub][Nanatsu_no_Taizai_Kamigami_no_Gekirin][01][GB][1080P][x264_AAC].mp4", "七大罪 众神的逆鳞.2019.全24集.1080P.日语中字", 3, "Nanatsu no Taizai Kamigami no Gekirin"},
	} {
		t.Run(fmt.Sprintf("%s/verified-alias=%t", tc.parent, tc.alias != ""), func(t *testing.T) {
			f := newMediaCollectionMetadata()
			if tc.alias != "" {
				f.aliases[62104] = append(f.aliases[62104], tc.alias)
			}
			identity := mediafacts.ParseIdentity(tc.file, tc.parent)
			identity.Kind = "tv" // The organizer already has a numbered video.
			got, err := recognizeMedia(context.Background(), f, identity)
			if tc.season == 0 {
				if err != nil || got != nil {
					t.Fatalf("translated subtitle must wait for evidence: recognition=%+v err=%v", got, err)
				}
				return
			}
			if err != nil || got == nil || got.Detail.ID != 62104 || got.Season != tc.season {
				t.Fatalf("recognition=%+v err=%v identity=%+v", got, err, identity)
			}
			if tc.alias == "" && (len(f.queries) != 3 || f.queries[2] != "七大罪") {
				t.Fatalf("unexpected fallback queries: %v", f.queries)
			}
		})
	}
}

func TestMediaRecognitionPackLabelsKeepEstablishedFirstSeason(t *testing.T) {
	for _, parent := range []string{"Work Complete (2024)", "Work [01-12] (2024)"} {
		t.Run(parent, func(t *testing.T) {
			f := newMediaCollectionMetadata()
			item := tmdb.Item{ID: 105, Kind: "tv", Title: "Work", Date: "2024-01-01"}
			f.items = append(f.items, item)
			f.details[105] = &tmdb.Detail{Item: item, Seasons: []tmdb.SeasonSummary{{Number: 1, Name: "Season 1", Date: "2024-01-01"}}}
			identity := mediafacts.ParseIdentity("Work 01 [1080p].mkv", parent)
			identity.Kind = "tv"
			got, err := recognizeMedia(context.Background(), f, identity)
			if err != nil || got == nil || got.Detail.ID != 105 || got.Season != 1 {
				t.Fatalf("pack label blocked first season: recognition=%+v err=%v identity=%+v", got, err, identity)
			}
		})
	}
}

func TestMediaRecognitionExplicitCoordinatesRemainStable(t *testing.T) {
	for _, season := range []int{0, 2} {
		t.Run(fmt.Sprintf("season-%d", season), func(t *testing.T) {
			f := newMediaCollectionMetadata()
			identity := mediafacts.ParseIdentity(fmt.Sprintf("Nanatsu no Taizai.S%02dE13.mkv", season), "七大罪 愤怒的审判 2021.全24集")
			got, err := recognizeMedia(context.Background(), f, identity)
			if err != nil || got == nil || got.Detail.ID != 62104 || got.Season != season {
				t.Fatalf("explicit coordinates changed: recognition=%+v err=%v identity=%+v", got, err, identity)
			}
		})
	}
	t.Run("explicit-tmdb-id", func(t *testing.T) {
		f := newMediaCollectionMetadata()
		got, err := recognizeMedia(context.Background(), f, mediafacts.Identity{Name: "Explicit work", Kind: "tv", TMDBID: 62104, Season: 2, SeasonExplicit: true})
		if err != nil || got == nil || got.Season != 2 || len(f.queries) != 0 {
			t.Fatalf("explicit ID should not search: recognition=%+v err=%v queries=%v", got, err, f.queries)
		}
	})
}

func TestMediaRecognitionRejectsAmbiguousOrUnrelatedHints(t *testing.T) {
	for _, scenario := range []string{"wrong-parent-year", "same-year-seasons", "duplicate-alias", "unrelated-parent", "adjacent-title-prefix", "base-title-pack-year"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMediaCollectionMetadata()
			identity := mediafacts.Identity{Name: "Nanatsu no Taizai", Kind: "tv", Season: 1}
			switch scenario {
			case "wrong-parent-year":
				identity = mediafacts.ParseIdentity("Nanatsu no Taizai 01 [BD 1920x1080 HEVC].mkv", "七大罪.2017.全24集")
				identity.Kind = "tv"
				other := &tmdb.Detail{Item: tmdb.Item{ID: 999, Kind: "tv", Title: "七大罪", OriginalTitle: "sin 七つの大罪", Date: "2017-04-15"}}
				f.details[999], f.items = other, append(f.items, other.Item)
			case "same-year-seasons":
				f.details[62104].Seasons[3].Date = "2021-02-01"
				identity = mediafacts.Identity{Name: "Nanatsu no Taizai Unknown Subtitle", ParentName: "七大罪 未知副标题", Kind: "tv", Year: "2021", YearFromParent: true, Season: 1}
			case "duplicate-alias":
				other := &tmdb.Detail{Item: tmdb.Item{ID: 999, Kind: "tv", Title: "Another work", Date: "2014-10-05"}}
				f.details[999], f.items = other, append(f.items, other.Item)
				f.aliases[999] = []string{"Nanatsu no Taizai"}
			case "unrelated-parent":
				identity = mediafacts.ParseIdentity("Unrelated Show.S01E01.mkv", "七大罪 2014")
			case "adjacent-title-prefix":
				identity = mediafacts.Identity{Name: "Nanatsu no TaizaiZ", ParentName: "七大罪 愤怒的审判", Kind: "tv", Year: "2021", YearFromParent: true, Season: 1}
			case "base-title-pack-year":
				identity = mediafacts.Identity{Name: "Nanatsu no Taizai", ParentName: "七大罪 后续合集", Kind: "tv", Year: "2021", YearFromParent: true, Season: 1}
			}
			got, err := recognizeMedia(context.Background(), f, identity)
			if err != nil || got != nil {
				t.Fatalf("ambiguous identity accepted: recognition=%+v err=%v identity=%+v", got, err, identity)
			}
		})
	}
}

func TestMediaRecognitionIgnoresUnrelatedParentYear(t *testing.T) {
	f := newMediaCollectionMetadata()
	identity := mediafacts.ParseIdentity("Nanatsu no Taizai.S02E01.mkv", "Unrelated Show 2021")
	got, err := recognizeMedia(context.Background(), f, identity)
	if err != nil || got == nil || got.Detail.ID != 62104 || got.Season != 2 {
		t.Fatalf("unrelated parent displaced filename: recognition=%+v err=%v identity=%+v", got, err, identity)
	}
}

func TestMediaRecognitionMovieAndNumericTitles(t *testing.T) {
	for _, tc := range []struct {
		file, parent string
		item         tmdb.Item
	}{
		{"[DragsterPS] The Seven Deadly Sins the Movie - Prisoners of the Sky [1080p].mkv", "Season 03", tmdb.Item{ID: 101, Kind: "movie"}},
		{"[BeanSub][The_Seven_Deadly_Sins-Hikari_ni_Norowareshi_Mono_tachi][MOVIE][GB][1080P].mp4", "Season 03", tmdb.Item{ID: 102, Kind: "movie"}},
		{"1917.2019.1080p.BluRay.mkv", "Unrelated Show 2021", tmdb.Item{ID: 103, Kind: "movie", Title: "1917", Date: "2019-12-25"}},
		{"[Cleo]Planet 51 [1080p].mkv", "", tmdb.Item{ID: 104, Kind: "movie", Title: "Planet 51", Date: "2009-11-19"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f := newMediaCollectionMetadata()
			if tc.item.Title != "" {
				f.items = append(f.items, tc.item)
				f.details[tc.item.ID] = &tmdb.Detail{Item: tc.item}
			}
			identity := mediafacts.ParseIdentity(tc.file, tc.parent)
			got, err := recognizeMedia(context.Background(), f, identity)
			if err != nil || got == nil || got.Detail.ID != tc.item.ID || got.Detail.Kind != "movie" || got.Season != 1 {
				t.Fatalf("movie identity changed: recognition=%+v err=%v identity=%+v", got, err, identity)
			}
		})
	}
}
