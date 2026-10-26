package preview

import (
	"fmt"
	"reflect"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/utils/torrentmeta"
)

func TestMatchBatchFilesKeepsExplicitFileSpecificationsAndExclusions(t *testing.T) {
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	rules := model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(1), Match: []string{`(?i)h[. ]?264|x264|avc`, `(?i)mp4`, `(?i)繁|cht|tc|big5`}, Exclude: []string{`(?i)sample`}}
	names := []string{
		"[组] Anime - 08 [AVC][CHT].mp4",
		"[组] Anime - 09 [HEVC][CHT].mp4",
		"[组] Anime - 10 [AVC][CHS].mp4",
		"[组] Anime - 11 [AVC][CHT].mkv",
		"[组] Anime - 12 sample [AVC][CHT].mp4",
		"[组] Anime - 13 [AVC][CHT].mp4",
		"[组] Anime - SP01 [AVC][CHT].mp4",
		"[组] Anime - 14-16 [AVC][CHT].mp4",
		"[组] Anime - 17 [AVC][CHT].iso",
		"[组] Anime [AVC][CHT].mp4",
		"[组] Anime - 08 [AVC][CHT].ass",
	}
	files := make([]torrentmeta.File, 0, len(names))
	for _, name := range names {
		files = append(files, torrentmeta.File{Path: "合集 AVC CHT MP4/" + name, Size: 100})
	}
	got, err := MatchBatchFiles(binding, rules, "Anime 01-17 合集 [AVC][CHT][MP4]", files)
	if err != nil || len(got) != 2 || *got[0].Parse.Episode != 8 || *got[1].Parse.Episode != 13 {
		t.Fatalf("only 08 and 13 satisfy every filename rule: %+v %v", got, err)
	}
	// The real extension must not borrow a contradictory container label.
	got, err = MatchBatchFiles(binding, rules, "Anime 01-17 合集 [AVC][CHT][MP4]", []torrentmeta.File{{Path: "AVC CHT MP4/Anime - 01.mkv", Size: 1}})
	if err != nil || len(got) != 0 {
		t.Fatal("directory tags bypassed filename rules", got, err)
	}
}

func TestBatchTitleExclusionsDoNotFetchMetadata(t *testing.T) {
	c := config()
	c.Sources[0].Rules.Exclude = []string{"合集"}
	s := build(t, c, page("Anime 01-13 合集 [1080p]", "Anime - 30 [1080p]"))
	if s.Items[0].Selectable || s.Items[0].ReasonCode != "EXCLUDED_PATTERN" || !s.Items[1].Selectable {
		t.Fatal("an excluded post became a manifest candidate", s.Items)
	}
}

func TestMatchBatchFilesRealMikanCollectionNames(t *testing.T) {
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	// File names confirmed from the three real Mikan torrents on 2026-10-26.
	for _, sample := range []struct {
		title, filename string
		patterns        []string
		first, last     int
	}{
		{"【MMSUB】★4月新番★[一弦定音 / この音とまれ / Kono Oto Tomare][01-13][1080P][WebRip 1080p AVC-8bit AAC][简繁外挂字幕]", "[MMSUB] Kono Oto Tomare - %02d [WebRip 1080p AVC-8bit AAC].mkv", []string{`(?i)1080p|1920[x×]1080`, `外挂|外掛`}, 1, 13},
		{"[漫猫字幕组]★4月新番[一弦定音][08-13][720P][简体][MP4]", "[Comicat][Kono Oto Tomare!][%02d][GB][720P][x264_AAC].mp4", []string{`(?i)720p`, `简`, `(?i)mp4`}, 8, 13},
		{"[漫猫字幕组]★4月新番[一弦定音][01-07][720P][简体][MP4]", "[Comicat][Kono Oto Tomare!][%02d][GB][720P][x264_AAC].mp4", []string{`(?i)720p`, `简`, `(?i)mp4`}, 1, 7},
	} {
		t.Run(sample.title, func(t *testing.T) {
			files := make([]torrentmeta.File, 0, sample.last-sample.first+1)
			for n := sample.first; n <= sample.last; n++ {
				files = append(files, torrentmeta.File{Path: fmt.Sprintf(sample.filename, n), Size: 1024})
			}
			got, err := MatchBatchFiles(binding, model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(1), Match: sample.patterns}, sample.title, files)
			if err != nil || len(got) != len(files) || *got[0].Parse.Episode != sample.first || *got[len(got)-1].Parse.Episode != sample.last {
				t.Fatal(got, err)
			}
		})
	}
}

func TestMatchBatchFilesVersionsAmbiguityAndCustomNumbering(t *testing.T) {
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	rules := model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(1)}
	files := []torrentmeta.File{
		{Path: "Anime - 01 [1080p].mkv"}, {Path: "Anime - 01v2 [1080p].mkv"}, {Path: "Anime - 01v3 [1080p].mkv"},
		{Path: "Anime - 02 [1080p].mkv"}, {Path: "Anime - 02 [720p].mkv"},
		{Path: "Anime - 00.mkv"},
		{Path: "a/Anime - 03.mkv"}, {Path: "b/Anime - 03.mkv"},
	}
	got, err := MatchBatchFiles(binding, rules, "", files)
	if err != nil || len(got) != 1 || got[0].Path != files[2].Path {
		t.Fatal("choose current v3, never guess between distinct eligible editions", got, err)
	}
	rules.MinimumEpisode = ptr(0)
	rules.EpisodePattern, rules.EpisodeGroup = `ep=(\d+)`, 1
	got, err = MatchBatchFiles(binding, rules, "", []torrentmeta.File{{Path: "Anime ep=00.mkv"}, {Path: "Anime ep=13.mkv"}, {Path: "Anime ep=14-16.mkv"}, {Path: "Anime - 15.mkv"}})
	if err != nil || len(got) != 2 || *got[0].Parse.Episode != 0 || *got[1].Parse.Episode != 13 {
		t.Fatal("custom extraction, zero and unresolved ranges", got, err)
	}
}

func TestMatchBatchFilesRejectsUnboundedOrInvalidTrees(t *testing.T) {
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	rules := model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(1)}
	for _, files := range [][]torrentmeta.File{nil, {{Path: "../Anime - 01.mkv"}}, {{Path: "a/../Anime - 01.mkv"}}, {{Path: "a\\Anime - 01.mkv"}}, {{Path: "Anime - 01.mkv", Size: -1}}, {{Path: "Anime - 01.mkv"}, {Path: "Anime - 01.mkv"}}, {{Path: "a/b/c/d/e/f/g/h/i/Anime - 01.mkv"}}} {
		if _, err := MatchBatchFiles(binding, rules, "", files); err == nil {
			t.Fatal("invalid tree accepted", files)
		}
	}
	// The provider list is input data: matching must not rewrite cached paths.
	files := []torrentmeta.File{{Path: "folder/Anime - 01.mkv", Size: 1}}
	before := append([]torrentmeta.File(nil), files...)
	if _, err := MatchBatchFiles(binding, rules, "", files); err != nil || !reflect.DeepEqual(before, files) {
		t.Fatal("mutated manifest", err)
	}
}

func TestMatchBatchFilesUsesPublisherContextWithoutHidingConflicts(t *testing.T) {
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	rules := model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(1), Match: []string{`(?i)1080p|1920[x×]1080`, `外挂|外掛`}, Exclude: []string{`(?i)sample`}}
	for _, tc := range []struct {
		name, title, file string
		want              int
	}{
		{"post subtitle labels", "Anime 01-13 合集 [1080p][简繁外挂字幕]", "[Kono Oto Tomare][02][1080P].mkv", 1},
		{"directory labels", "Anime 01-13 合集", "Anime [1080p][简繁外挂字幕]/Anime - 02.mkv", 1},
		{"file resolution wins", "Anime 01-13 合集 [1080p][外挂]", "Anime - 02 [720p].mkv", 0},
		{"ambiguous post resolution", "Anime 01-13 合集 [1080p/720p][外挂]", "Anime - 02 [720p].mkv", 0},
		{"ambiguous file resolution", "Anime 01-13 合集 [1080p][外挂]", "Anime - 02 [1080p/720p].mkv", 0},
		{"ambiguous post codec", "Anime 01-13 合集 [1080p][AVC/HEVC][外挂]", "Anime - 02 [1080p][AVC].mkv", 0},
		{"file subtitle mode wins", "Anime 01-13 合集 [1080p][外挂]", "Anime - 02 [1080p][内嵌].mkv", 0},
		{"other video extensions remain explicit", "Anime 01-13 合集 [1080p][外挂][MKV]", "Anime - 02 [1080p].avi", 0},
		{"post exclusion", "Anime 01-13 合集 sample [1080p][外挂]", "Anime - 02 [1080p][外挂].mkv", 0},
		{"directory exclusion", "Anime 01-13 合集 [1080p][外挂]", "sample/Anime - 02 [1080p].mkv", 0},
		{"file exclusion", "Anime 01-13 合集 [1080p][外挂]", "Anime - 02 sample [1080p].mkv", 0},
		{"title cannot invent episode", "Anime 01-13 合集 [1080p][外挂]", "Anime.mkv", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchBatchFiles(binding, rules, tc.title, []torrentmeta.File{{Path: tc.file, Size: 1}})
			if err != nil || len(got) != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}
