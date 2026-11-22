package preview

import (
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
	"strm115/backend/internal/utils/torrentmeta"
)

func TestSpecialBatchSourceEpisodesUsesConfirmedPartAndTarget(t *testing.T) {
	const title = "[ToonsHub] 我的幸福婚姻 特别篇 / My Happy Marriage Special Episodes - 01-03 [AMZN][1080P][DUAL][CHS&CHT&JP&EN&KO][MKV]"
	const directory = "My.Happy.Marriage.S00E02-E04.Specials.1080p.AMZN.WEB-DL.DUAL.DDP2.0.H.264.MSubs-ToonsHub/"
	files := []torrentmeta.File{
		{Path: directory + "My.Happy.Marriage.S00E04.Special.Part.3.1080p.AMZN.WEB-DL.DUAL.DDP2.0.H.264.MSubs-ToonsHub.mkv", Size: 3},
		{Path: directory + "My.Happy.Marriage.S00E02.Special.Part.1.1080p.AMZN.WEB-DL.DUAL.DDP2.0.H.264.MSubs-ToonsHub.mkv", Size: 1},
		{Path: directory + "My.Happy.Marriage.S00E03.Special.Part.2.1080p.AMZN.WEB-DL.DUAL.DDP2.0.H.264.MSubs-ToonsHub.mkv", Size: 2},
		{Path: directory + "readme.txt", Size: 10},
	}
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: ptr(1), Last: ptr(3)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 1, ChapterEpisode: 1}}
	rules := model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(2), SpecialChapter: true, Match: []string{`(?i)1080p`, `(?i)chs`, `(?i)cht`}}
	parsed, err := releaseparse.TitleForChapter(title, true)
	if err != nil {
		t.Fatal(err)
	}
	plan := model.AnimeNamingPlan{Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{TargetBase: 1}}}}}
	episodes := SpecialBatchSourceEpisodes(rules, parsed, &plan, "main", files)
	if want := (BatchEpisodeMap{files[0]: 3, files[1]: 1, files[2]: 2}); !reflect.DeepEqual(episodes, want) {
		t.Fatalf("来源集号 = %v，预期 %v，RSS 集号 = %q", episodes, want, parsed.RawEpisode)
	}
	got, err := MatchBatchFilesWithSourceEpisodes(binding, rules, title, files, episodes)
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	for _, file := range got {
		episode := int(file.Size)
		projection := plan.Project("main", file.Parse)
		if !file.SourceEpisodeNormalized || episode < 2 || *file.Parse.Episode != episode || projection.Episode == nil || *projection.Episode != episode+1 {
			t.Fatal(file, projection)
		}
	}
	selected := []torrentmeta.File{files[0], files[2]}
	replayed, err := MatchBatchFilesWithSourceEpisodes(binding, rules, title, selected, FrozenSpecialBatchSourceEpisodes(got))
	if err != nil || !reflect.DeepEqual(replayed, got) {
		t.Fatal(replayed, err)
	}
	rules.MinimumEpisode = ptr(1)
	replayed, err = MatchBatchFilesWithSourceEpisodes(binding, rules, title, files, FrozenSpecialBatchSourceEpisodes(got))
	if err != nil || !reflect.DeepEqual(replayed, got) {
		t.Fatal("回放时未记录的视频不能以文件名中的目标集号参与去重", replayed, err)
	}
}

func TestSpecialBatchSourceEpisodesKeepsOrdinaryAndAmbiguousCases(t *testing.T) {
	const title = "特别篇 - 01-03 [1080p]"
	release, err := releaseparse.TitleForChapter(title, true)
	if err != nil {
		t.Fatal(err)
	}
	files := []torrentmeta.File{{Path: "Anime.S00E02.Special.Part.1.mkv"}, {Path: "Anime.S00E03.Special.Part.2.mkv"}, {Path: "Anime.S00E04.Special.Part.3.mkv"}}
	for _, test := range []struct {
		name, replace string
		offset        int
		count         int
		custom        bool
	}{
		{name: "零偏移", count: 3},
		{name: "偏移不符", offset: 2, count: 3},
		{name: "缺集", offset: 1, count: 2},
		{name: "分部重复", replace: "Anime.S00E04.Special.Part.2.mkv", offset: 1, count: 3},
		{name: "季号冲突", replace: "Anime.S01E04.Special.Part.3.mkv", offset: 1, count: 3},
		{name: "仅有显式季集号", replace: "Anime.S00E04.mkv", offset: 1, count: 3},
		{name: "裸数字", replace: "Anime - 04.mkv", offset: 1, count: 3},
		{name: "疑似小数分部", replace: "Anime.S00E04.Special.Part.3.5.mkv", offset: 1, count: 3},
		{name: "分部范围", replace: "Anime.S00E04.Special.Part.3-4.mkv", offset: 1, count: 3},
		{name: "自定义集号", offset: 1, count: 3, custom: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := append([]torrentmeta.File(nil), files[:test.count]...)
			if test.replace != "" {
				manifest[2].Path = test.replace
			}
			rules := model.AnimeRules{SpecialChapter: true}
			if test.custom {
				rules.EpisodePattern = `E([0-9]+)`
			}
			plan := model.AnimeNamingPlan{Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{TargetBase: test.offset}}}}}
			if got := SpecialBatchSourceEpisodes(rules, release, &plan, "main", manifest); got != nil {
				t.Fatal(got)
			}
		})
	}
	ordinary := []torrentmeta.File{{Path: "Anime - 01.mkv"}, {Path: "Anime - 02.mkv"}, {Path: "Anime - 03.mkv"}}
	binding := model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: ptr(1), Last: ptr(3)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 1, ChapterEpisode: 1}}
	rules := model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(1), SpecialChapter: true}
	plan := model.AnimeNamingPlan{Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{TargetBase: 1}}}}}
	episodes := SpecialBatchSourceEpisodes(rules, release, &plan, "main", ordinary)
	got, err := MatchBatchFilesWithSourceEpisodes(binding, rules, title, ordinary, episodes)
	if err != nil || episodes != nil || len(got) != 3 {
		t.Fatal(got, err)
	}
	for _, file := range got {
		if file.SourceEpisodeNormalized || !strings.HasSuffix(file.Path, " - "+file.Parse.RawEpisode+".mkv") {
			t.Fatal(file)
		}
	}
}
