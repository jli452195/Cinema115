package service

import (
	"regexp"
	"strings"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/client/mikan"
)

// Search-only hints never change RSS specification identity or download rules.
type ResourceSearchSpec struct {
	Resolution   string   `json:"resolution,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	SubtitleMode string   `json:"subtitleMode,omitempty"`
}

var (
	resourceCompoundSubtitles = regexp.MustCompile(`(?:(?:内封|內封|内嵌|內嵌|外挂|外掛|双语|雙語|多语|多語|字幕)){2,}`)
	resourceQuality           = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:[0-9]{3,4}\s*[x×]\s*(480|576|720|1080|1440|2160|4320)([pi])?|(480|576|720|1080|1440|2160|4320)([pi])|([48])k)`)
)

func resourceSearchSpec(title string) ResourceSearchSpec {
	parsed := animeparse.Specification(title)
	// Reuse language boundaries without altering RSS profiles or subtitle modes.
	if normalized := resourceCompoundSubtitles.ReplaceAllString(title, "字幕"); normalized != title {
		parsed.Languages = animeparse.Specification(normalized).Languages
	}
	return ResourceSearchSpec{Resolution: resourceSearchResolution(title), Languages: parsed.Languages, SubtitleMode: parsed.SubtitleMode}
}

func resourceSearchResolution(title string) string {
	quality := ""
	for _, index := range resourceQuality.FindAllStringSubmatchIndex(title, -1) {
		if end := index[1]; end < len(title) && (title[end] >= 'a' && title[end] <= 'z' || title[end] >= 'A' && title[end] <= 'Z' || title[end] >= '0' && title[end] <= '9') {
			continue
		}
		part := func(group int) string {
			if index[group*2] < 0 {
				return ""
			}
			return strings.ToLower(title[index[group*2]:index[group*2+1]])
		}
		value := part(1) + part(2)
		if part(1) != "" && part(2) == "" {
			value += "p"
		} else if part(3) != "" {
			value = part(3) + part(4)
		} else if part(5) != "" {
			value = map[string]string{"4": "2160p", "8": "4320p"}[part(5)]
		}
		if quality != "" && quality != value {
			return ""
		}
		quality = value
	}
	return quality
}

func mikanResourceSpecs(items []mikan.Item) map[string]ResourceSearchSpec {
	specs := make(map[string]ResourceSearchSpec, len(items))
	for _, item := range items {
		specs[item.ID] = resourceSearchSpec(item.Title)
	}
	return specs
}
