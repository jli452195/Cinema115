package model

import (
	"strings"
	"testing"
)

func animeValue[T any](v T) *T { return &v }

func validAnimeSource() AnimeSourceConfig {
	return AnimeSourceConfig{Ref: "main", Provider: "mikan", Role: "main", Status: "paused", RunWhenEnabled: true, ItemID: animeValue("2259"), GroupID: animeValue("45"), GroupName: animeValue("字幕组"), Binding: AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &AnimeScope{Kind: "range", First: animeValue(25), Last: animeValue(36)}, ChapterCorrespondence: &AnimeChapterCorrespondence{SourceEpisode: 25, ChapterEpisode: 1}}, Rules: AnimeRules{SchemaVersion: 1, Resolution: animeValue("1080p"), Include: []string{}, Exclude: []string{}, MinimumEpisode: animeValue(30), InitialMode: "from_episode"}}
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
		{"unknown-resolution", func(s *AnimeSourceConfig) { s.Rules.Resolution = animeValue("4k") }},
		{"out-of-range", func(s *AnimeSourceConfig) { s.Rules.MinimumEpisode = animeValue(0) }},
		{"reverse-scope", func(s *AnimeSourceConfig) { s.Binding.Scope.Last = animeValue(24) }},
		{"unconfirmed", func(s *AnimeSourceConfig) { s.Binding.Confirmed = false }},
		{"chapter-without-evidence", func(s *AnimeSourceConfig) { s.Binding.Scope = &AnimeScope{Kind: "confirmed_chapter"} }},
		{"negative-chapter-range", func(s *AnimeSourceConfig) { s.Binding.ChapterCorrespondence.SourceEpisode = 30 }},
		{"overflow-chapter-range", func(s *AnimeSourceConfig) { s.Binding.ChapterCorrespondence.ChapterEpisode = 99999 }},
		{"missing-resolution", func(s *AnimeSourceConfig) { s.Rules.Resolution = nil }},
		{"duplicate-words", func(s *AnimeSourceConfig) { s.Rules.Include = []string{"HEVC", "HEVC"} }},
		{"long-unicode-word", func(s *AnimeSourceConfig) { s.Rules.Exclude = []string{strings.Repeat("字", 65)} }},
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
