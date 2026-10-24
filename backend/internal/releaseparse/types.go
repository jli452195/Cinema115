package releaseparse

import (
	"fmt"
	"unicode/utf8"
)

const SchemaVersion = 1

// Result retains the publisher's numbering and explicit title metadata.
// It does not establish a TMDB episode mapping or authorize a download.
type Result struct {
	SchemaVersion int     `json:"schemaVersion"`
	Kind          string  `json:"kind"`
	RawEpisode    string  `json:"rawEpisode"`
	Episode       *int    `json:"episode"`
	Resolution    *string `json:"resolution"`
	VersionLabel  string  `json:"versionLabel"`
	Spec          Spec    `json:"spec"`
	SpecAmbiguous bool    `json:"specAmbiguous,omitempty"`
}

// Spec describes title-declared properties, never verified file tracks.
// Empty dimensions are unconstrained in a profile and unknown on a release.
type Spec struct {
	Resolution   string   `json:"resolution,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	SubtitleMode string   `json:"subtitleMode,omitempty"`
	Codec        string   `json:"codec,omitempty"`
	Container    string   `json:"container,omitempty"`
	BitDepth     string   `json:"bitDepth,omitempty"`
}

func (p Result) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported parse schema")
	}
	switch p.Kind {
	case "unknown", "episode", "special", "batch", "composite":
	default:
		return fmt.Errorf("invalid parsed release kind")
	}
	if p.Episode != nil && (*p.Episode < 0 || *p.Episode > 99999 || p.Kind != "episode") {
		return fmt.Errorf("invalid parsed episode")
	}
	valid := func(s string, max int) bool {
		return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max
	}
	if !valid(p.RawEpisode, 128) || !valid(p.VersionLabel, 64) {
		return fmt.Errorf("invalid parse text")
	}
	if p.Resolution != nil {
		switch *p.Resolution {
		case "720p", "1080p", "2160p", "unknown":
		default:
			return fmt.Errorf("invalid parsed resolution")
		}
	}
	return p.Spec.Validate()
}

func (s Spec) Empty() bool {
	return s.Resolution == "" && len(s.Languages) == 0 && s.SubtitleMode == "" && s.Codec == "" && s.Container == "" && s.BitDepth == ""
}

func (s Spec) Validate() error {
	valid := func(value string, choices ...string) bool {
		if value == "" {
			return true
		}
		for _, choice := range choices {
			if value == choice {
				return true
			}
		}
		return false
	}
	if !valid(s.Resolution, "720p", "1080p", "2160p") || !valid(s.SubtitleMode, "hard", "soft", "external") || !valid(s.Codec, "h264", "h265", "av1", "vp9") || !valid(s.Container, "mp4", "mkv") || !valid(s.BitDepth, "8", "10", "12") || len(s.Languages) > 4 {
		return fmt.Errorf("invalid title specification")
	}
	seen := make(map[string]bool, len(s.Languages))
	for _, language := range s.Languages {
		if language == "" || !valid(language, "zh-Hans", "zh-Hant", "ja", "en") || seen[language] {
			return fmt.Errorf("invalid subtitle languages")
		}
		seen[language] = true
	}
	return nil
}
