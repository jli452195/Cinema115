package animeparse

import (
	"reflect"
	"regexp"
	"testing"

	"strm115/backend/internal/model"
)

func TestSourceRegexFilteringAndCustomEpisodes(t *testing.T) {
	rules, err := CompileRules(model.AnimeRules{SchemaVersion: 1, Match: []string{`(?i)1080p`, `\d{1,3}`}, Exclude: []string{`(?i)sample`, `\[batch\]`}, EpisodePattern: `ep=(\d+)`, EpisodeGroup: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		title, reason string
		episode       int
	}{
		{"Anime ep=25 [1080P]", "", 25},
		{"Anime ep=25 [720p]", "REQUIRED_PATTERN_MISSING", 25},
		{"sample ep=25 [1080P]", "EXCLUDED_PATTERN", 25},
		{"[batch] Anime ep=25 [1080P]", "EXCLUDED_PATTERN", 25},
		{"Anime - 12 [1080P]", "", -1},
		{"Anime ep=25 ep=26 [1080P]", "", -1},
		{"Anime ep=00 [1080P]", "", 0},
		{"Anime ep=100000 [1080P]", "", -1},
	} {
		if got := rules.Filter(tc.title); got != tc.reason {
			t.Fatalf("%s: %s", tc.title, got)
		}
		parsed, err := rules.Parse(tc.title)
		if err != nil {
			t.Fatal(err)
		}
		if tc.episode < 0 && parsed.Episode != nil || tc.episode >= 0 && (parsed.Episode == nil || *parsed.Episode != tc.episode) {
			t.Fatalf("%s: %+v", tc.title, parsed)
		}
	}
}

func TestSourceRulesKeepDefaultParsingAndIndependentOverrides(t *testing.T) {
	defaultRules, _ := CompileRules(model.AnimeRules{SchemaVersion: 1})
	customRules, err := CompileRules(model.AnimeRules{SchemaVersion: 1, EpisodePattern: `chapter=(\d+)`, EpisodeGroup: 1})
	if err != nil {
		t.Fatal(err)
	}
	title := "[group] Anime - 03v2 [1080p][HEVC] chapter=27"
	a, err := defaultRules.Parse(title)
	if err != nil {
		t.Fatal(err)
	}
	b, err := customRules.Parse(title)
	if err != nil {
		t.Fatal(err)
	}
	if a.Episode == nil || *a.Episode != 3 || b.Episode == nil || *b.Episode != 27 || a.VersionLabel != b.VersionLabel || !reflect.DeepEqual(a.Spec, b.Spec) {
		t.Fatal(a, b)
	}
}

func TestSpecificationTemplatesMatchOriginalTitles(t *testing.T) {
	for _, tc := range []struct {
		spec  model.AnimeSpec
		title string
	}{
		{model.AnimeSpec{Resolution: "1080p", Languages: []string{"zh-Hans", "ja"}, Container: "mp4"}, "[银色子弹字幕组][名侦探柯南][第30集][简日双语MP4][1080P]"},
		{model.AnimeSpec{Resolution: "1080p", Languages: []string{"zh-Hant"}, Codec: "h264", Container: "mp4"}, "[ANi] Anime - 02 [1080P][Baha][AAC AVC][CHT][MP4]"},
		{model.AnimeSpec{Resolution: "1080p", Codec: "h265", BitDepth: "10", SubtitleMode: "soft", Container: "mkv"}, "[LoliHouse] Anime - 03 [1920x1080][HEVC-10bit][内封][MKV]"},
	} {
		patterns := SpecPatterns(tc.spec)
		if len(patterns) == 0 {
			t.Fatal("empty template")
		}
		for _, pattern := range patterns {
			re, err := regexp.Compile(pattern)
			if err != nil || !re.MatchString(tc.title) {
				t.Fatal(pattern, tc.title, err)
			}
		}
	}
}
