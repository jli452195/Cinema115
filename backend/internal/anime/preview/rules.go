package preview

import (
	"regexp"
	"strconv"

	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
)

// SourceRules is compiled once per feed/preview, shared by all its rows.
// Go's RE2 engine bounds matching time even for user supplied expressions.
type SourceRules struct {
	match, exclude []*regexp.Regexp
	episode        *regexp.Regexp
	group          int
}

func CompileRules(r model.AnimeRules) (SourceRules, error) {
	out := SourceRules{group: r.EpisodeGroup}
	for _, pair := range []struct {
		patterns []string
		target   *[]*regexp.Regexp
	}{{r.Match, &out.match}, {r.Exclude, &out.exclude}} {
		*pair.target = make([]*regexp.Regexp, 0, len(pair.patterns))
		for _, pattern := range pair.patterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return out, err
			}
			*pair.target = append(*pair.target, re)
		}
	}
	if r.EpisodePattern != "" {
		var err error
		out.episode, err = regexp.Compile(r.EpisodePattern)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func (r SourceRules) Filter(title string) string {
	for _, re := range r.exclude {
		if re.MatchString(title) {
			return "EXCLUDED_PATTERN"
		}
	}
	for _, re := range r.match {
		if !re.MatchString(title) {
			return "REQUIRED_PATTERN_MISSING"
		}
	}
	return ""
}

// Parse overrides only the source's episode interpretation. A custom rule that
// fails or matches multiple times never falls back to a different episode.
func (r SourceRules) Parse(title string) (releaseparse.Result, error) {
	out, err := releaseparse.Title(title)
	if err != nil || r.episode == nil || out.Kind == "batch" {
		return out, err
	}
	out.Kind, out.Episode, out.RawEpisode = "unknown", nil, ""
	matches := r.episode.FindAllStringSubmatch(title, 2)
	if len(matches) != 1 || r.group < 1 || r.group >= len(matches[0]) {
		return out, nil
	}
	raw := matches[0][r.group]
	if len(raw) < 1 || len(raw) > 5 {
		return out, nil
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return out, nil
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 99999 {
		return out, nil
	}
	out.Kind, out.Episode, out.RawEpisode = "episode", &n, raw
	return out, nil
}

// SpecPatterns creates editable starter rules; a parsed profile is a picker
// suggestion, not a second persisted filtering system.
func SpecPatterns(s releaseparse.Spec) []string {
	out := make([]string, 0, 10)
	add := func(pattern string) {
		if pattern != "" {
			out = append(out, "(?i)"+pattern)
		}
	}
	add(map[string]string{"720p": `720p|1280[x×]720`, "1080p": `1080p|1920[x×]1080`, "2160p": `2160p|3840[x×]2160|4k`}[s.Resolution])
	for _, language := range s.Languages {
		add(map[string]string{"zh-Hans": `简|簡|chs|sc|gb`, "zh-Hant": `繁|cht|tc|big5`, "ja": `日|jpn|jp|ja`, "en": `英|eng`}[language])
	}
	add(map[string]string{"hard": `内嵌|內嵌|硬字幕|hardsub`, "soft": `内封|內封|softsub`, "external": `外挂|外掛|外置|外挂字幕|external`}[s.SubtitleMode])
	add(map[string]string{"h264": `h[. ]?264|x264|avc`, "h265": `h[. ]?265|x265|hevc`, "av1": `av1`, "vp9": `vp9`}[s.Codec])
	if s.BitDepth != "" {
		add(s.BitDepth + `[ -]?bit`)
	}
	if s.Container != "" {
		add(regexp.QuoteMeta(s.Container))
	}
	return out
}
