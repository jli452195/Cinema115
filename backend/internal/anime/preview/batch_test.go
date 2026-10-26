package preview

import (
	"reflect"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/utils/torrentmeta"
)

func TestMatchBatchFilesUsesEveryRuleOnIndividualVideos(t *testing.T) {
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
	got, err := MatchBatchFiles(binding, rules, files)
	if err != nil || len(got) != 2 || *got[0].Parse.Episode != 8 || *got[1].Parse.Episode != 13 {
		t.Fatalf("only 08 and 13 satisfy every filename rule: %+v %v", got, err)
	}
	// Directory quality tags must not authorize otherwise unqualified videos.
	got, err = MatchBatchFiles(binding, rules, []torrentmeta.File{{Path: "AVC CHT MP4/Anime - 01.mkv", Size: 1}})
	if err != nil || len(got) != 0 {
		t.Fatal("directory tags bypassed filename rules", got, err)
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
	got, err := MatchBatchFiles(binding, rules, files)
	if err != nil || len(got) != 1 || got[0].Path != files[2].Path {
		t.Fatal("choose current v3, never guess between distinct eligible editions", got, err)
	}
	rules.MinimumEpisode = ptr(0)
	rules.EpisodePattern, rules.EpisodeGroup = `ep=(\d+)`, 1
	got, err = MatchBatchFiles(binding, rules, []torrentmeta.File{{Path: "Anime ep=00.mkv"}, {Path: "Anime ep=13.mkv"}, {Path: "Anime ep=14-16.mkv"}, {Path: "Anime - 15.mkv"}})
	if err != nil || len(got) != 2 || *got[0].Parse.Episode != 0 || *got[1].Parse.Episode != 13 {
		t.Fatal("custom extraction, zero and unresolved ranges", got, err)
	}
}

func TestMatchBatchFilesRejectsUnboundedOrInvalidTrees(t *testing.T) {
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	rules := model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(1)}
	for _, files := range [][]torrentmeta.File{nil, {{Path: "../Anime - 01.mkv"}}, {{Path: "a/../Anime - 01.mkv"}}, {{Path: "a\\Anime - 01.mkv"}}, {{Path: "Anime - 01.mkv", Size: -1}}, {{Path: "Anime - 01.mkv"}, {Path: "Anime - 01.mkv"}}, {{Path: "a/b/c/d/e/f/g/h/i/Anime - 01.mkv"}}} {
		if _, err := MatchBatchFiles(binding, rules, files); err == nil {
			t.Fatal("invalid tree accepted", files)
		}
	}
	// The provider list is input data: matching must not rewrite cached paths.
	files := []torrentmeta.File{{Path: "folder/Anime - 01.mkv", Size: 1}}
	before := append([]torrentmeta.File(nil), files...)
	if _, err := MatchBatchFiles(binding, rules, files); err != nil || !reflect.DeepEqual(before, files) {
		t.Fatal("mutated manifest", err)
	}
}
