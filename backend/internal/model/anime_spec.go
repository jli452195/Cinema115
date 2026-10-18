package model

import "fmt"

func (s AnimeSpec) Empty() bool {
	return s.Resolution == "" && len(s.Languages) == 0 && s.SubtitleMode == "" && s.Codec == "" && s.Container == "" && s.BitDepth == ""
}

func (s AnimeSpec) Validate() error {
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
