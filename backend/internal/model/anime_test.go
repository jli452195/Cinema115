package model

import (
	"strings"
	"testing"
)

func animeValue[T any](v T) *T { return &v }

func validAnimeSource() AnimeSourceConfig {
	return AnimeSourceConfig{Ref: "main", Provider: "mikan", Role: "main", Status: "paused", RunWhenEnabled: true, ItemID: animeValue("2259"), GroupID: animeValue("45"), GroupName: animeValue("字幕组"), Binding: AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &AnimeScope{Kind: "range", First: animeValue(25), Last: animeValue(36)}, ChapterCorrespondence: &AnimeChapterCorrespondence{SourceEpisode: 25, ChapterEpisode: 1}}, Rules: AnimeRules{SchemaVersion: 1, Match: []string{"(?i)1080p"}, Exclude: []string{}, MinimumEpisode: animeValue(30)}}
}

func TestAnimeSourceRulesAreIndependentOfCorrespondence(t *testing.T) {
	s := validAnimeSource()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if *s.Rules.MinimumEpisode != 30 || s.Binding.ChapterCorrespondence.SourceEpisode != 25 {
		t.Fatal("补番下限改写了编号基准")
	}
	for _, tc := range []struct {
		name   string
		change func(*AnimeSourceConfig)
	}{
		{"invalid-regex", func(s *AnimeSourceConfig) { s.Rules.Match = []string{"["} }},
		{"out-of-range", func(s *AnimeSourceConfig) { s.Rules.MinimumEpisode = animeValue(0) }},
		{"reverse-scope", func(s *AnimeSourceConfig) { s.Binding.Scope.Last = animeValue(24) }},
		{"unconfirmed", func(s *AnimeSourceConfig) { s.Binding.Confirmed = false }},
		{"chapter-without-evidence", func(s *AnimeSourceConfig) { s.Binding.Scope = &AnimeScope{Kind: "confirmed_chapter"} }},
		{"negative-chapter-range", func(s *AnimeSourceConfig) { s.Binding.ChapterCorrespondence.SourceEpisode = 30 }},
		{"overflow-chapter-range", func(s *AnimeSourceConfig) { s.Binding.ChapterCorrespondence.ChapterEpisode = 99999 }},
		{"missing-minimum", func(s *AnimeSourceConfig) { s.Rules.MinimumEpisode = nil }},
		{"too-many-patterns", func(s *AnimeSourceConfig) { s.Rules.Match = make([]string, 21) }},
		{"long-unicode-pattern", func(s *AnimeSourceConfig) { s.Rules.Exclude = []string{strings.Repeat("字", 513)} }},
		{"invalid-external-id", func(s *AnimeSourceConfig) { s.ItemID = animeValue("1e6") }},
		{"zero-group", func(s *AnimeSourceConfig) { s.GroupID = animeValue("0") }},
		{"enable-unavailable", func(s *AnimeSourceConfig) { s.Status = "enabled" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validAnimeSource()
			tc.change(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	draft := AnimeSourceConfig{Ref: "backup", Provider: "mikan", Role: "standby", Status: "draft", Binding: AnimeBinding{SchemaVersion: 1}, Rules: AnimeRules{SchemaVersion: 1}}
	if err := draft.Validate(); err != nil {
		t.Fatal("草稿必须允许不完整", err)
	}
}

func TestAnimeJSONRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	for _, raw := range []string{`{"schemaVersion":1,"cookie":"secret"}`, `{"schemaVersion":1} {"schemaVersion":1}`, `{"schemaVersion":1} garbage`} {
		var rules AnimeRules
		if err := DecodeAnimeJSON(strings.NewReader(raw), &rules); err == nil {
			t.Fatal("accepted unknown/trailing JSON", raw)
		}
	}
	var rules AnimeRules
	if err := DecodeAnimeJSON(strings.NewReader(`{"schemaVersion":1,"minimumEpisode":30}`), &rules); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeRegexAndReviewValidation(t *testing.T) {
	valid := func() AnimeSourceConfig {
		s := validAnimeSource()
		s.Binding.Scope = &AnimeScope{Kind: "confirmed_chapter"}
		s.Binding.Review = &AnimeSourceReview{Method: "source_review", ItemID: *s.ItemID, GroupID: *s.GroupID}
		s.Rules.Match = []string{`(?i)1080p`, `\d{1,3}`}
		s.Rules.EpisodePattern, s.Rules.EpisodeGroup = `-\s*(\d+)`, 1
		return s
	}
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*AnimeSourceConfig){
		func(s *AnimeSourceConfig) { s.Binding.Review.ItemID = "999" },
		func(s *AnimeSourceConfig) { s.Binding.Review.Method = "automatic" },
		func(s *AnimeSourceConfig) { s.Binding.Confirmed = false },
		func(s *AnimeSourceConfig) { s.Rules.Match = []string{`(?<=-)\d+`} },
		func(s *AnimeSourceConfig) { s.Rules.Exclude = []string{"["} },
		func(s *AnimeSourceConfig) { s.Rules.EpisodePattern = "[" },
		func(s *AnimeSourceConfig) { s.Rules.EpisodeGroup = 2 },
		func(s *AnimeSourceConfig) { s.Rules.EpisodeGroup = 0 },
		func(s *AnimeSourceConfig) { s.Rules.EpisodePattern = "" },
	} {
		s := valid()
		change(&s)
		if s.Validate() == nil {
			t.Fatal("invalid specification/review accepted", s)
		}
	}
	draft := valid()
	draft.Status = "draft"
	draft.Binding.Confirmed = false
	draft.Binding.Review = nil
	draft.Rules.Match = nil
	if err := draft.Validate(); err != nil {
		t.Fatal("incomplete draft blocked", err)
	}
}
