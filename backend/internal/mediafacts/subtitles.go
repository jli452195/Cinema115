package mediafacts

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"strm115/backend/internal/releaseparse"
)

// SubtitleVideo describes every video in the listing, including unselected ones.
type SubtitleVideo struct{ ID, ParentID, Name string }

type subtitleIdentity struct {
	key, version, tail string
	episode            int
	spec               releaseparse.Spec
}

type subtitleCandidate struct {
	SubtitleVideo
	identity *subtitleIdentity
	parsed   bool
}

// SubtitleMatcher is a per-listing, local-only fallback after exact stem matching.
// Video facts are parsed lazily once; the matcher is not shared across goroutines.
type SubtitleMatcher struct {
	videos map[string][]*subtitleCandidate
}

func NewSubtitleMatcher(videos []SubtitleVideo) *SubtitleMatcher {
	m := &SubtitleMatcher{videos: make(map[string][]*subtitleCandidate, len(videos))}
	for _, video := range videos {
		m.videos[video.ParentID] = append(m.videos[video.ParentID], &subtitleCandidate{SubtitleVideo: video})
	}
	return m
}

// Only complete, known metadata fields may be removed. Title/episode/group and
// unknown edition tags stay part of identity; this is not fuzzy title matching.
const subtitleMetadataToken = `(?:(?:480|576|720|1080|1440|2160|4320)[pi]|[0-9]{3,4}\s*[x×]\s*(?:480|576|720|1080|1440|2160|4320)|[48]k|h[ ._-]?26[45]|x26[45]|avc|hevc|av1|vp9|(?:8|10|12)[ -]?bit|mkv|mp4|chs|cht|sc|tc|gb|big5|ja|jpn|eng|简体(?:中文)?|簡體(?:中文)?|繁体(?:中文)?|繁體(?:中文)?|简繁|簡繁|简|繁|内嵌|內嵌|内封|內封|外挂|外掛|forced|sdh|default)`

var (
	subtitleMetadata = regexp.MustCompile(`(?i)^` + subtitleMetadataToken + `(?:[ ._+&/-]+` + subtitleMetadataToken + `)*$`)
	subtitleBrackets = regexp.MustCompile(`[\[【(（]([^\[\]【】()（）]+)[\]】)）]`)
)

func subtitleFileIdentity(name string) *subtitleIdentity {
	stem := strings.TrimSuffix(name, path.Ext(name))
	parsed, err := releaseparse.Title(stem)
	if err != nil || parsed.Kind != "episode" || parsed.Episode == nil || parsed.SpecAmbiguous {
		return nil
	}
	parsed.Spec.Resolution = resolution(stem)
	if resourceQuality.MatchString(stem) && parsed.Spec.Resolution == "" {
		return nil // Conflicting explicit qualities must not become unspecified.
	}
	flags := make(map[string]bool, 3)
	metadata := func(tag string) bool {
		if !subtitleMetadata.MatchString(strings.TrimSpace(tag)) {
			return false
		}
		for _, field := range strings.FieldsFunc(strings.ToLower(tag), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("._+&/-", r) }) {
			if field == "forced" || field == "sdh" || field == "default" {
				flags[field] = true
			}
		}
		return true
	}
	core := subtitleBrackets.ReplaceAllStringFunc(stem, func(block string) string {
		parts := subtitleBrackets.FindStringSubmatch(block)
		if metadata(parts[1]) {
			return " "
		}
		return block
	})
	fields := strings.FieldsFunc(strings.ToLower(core), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("[]【】()（）._-!！?？", r) })
	for len(fields) > 0 && metadata(fields[len(fields)-1]) {
		fields = fields[:len(fields)-1]
	}
	if len(fields) < 2 {
		return nil // A bare episode number cannot establish the work's identity.
	}
	for i, field := range fields {
		if field == strings.ToLower(parsed.RawEpisode) {
			fields[i] = strconv.Itoa(*parsed.Episode)
		}
	}
	tail := ""
	for _, language := range parsed.Spec.Languages {
		tail += "." + language
	}
	for _, flag := range []string{"forced", "sdh", "default"} {
		if flags[flag] {
			tail += "." + flag
		}
	}
	return &subtitleIdentity{key: strings.Join(fields, " "), version: parsed.VersionLabel, episode: *parsed.Episode, spec: parsed.Spec, tail: tail}
}

// Candidates includes unselected videos so callers can retain ambiguous subtitles.
// A subtitle may be assigned only when exactly one candidate remains.
func (m *SubtitleMatcher) Candidates(parentID, name string) ([]string, string) {
	sub := subtitleFileIdentity(name)
	if sub == nil {
		return nil, ""
	}
	var owners []string
	for _, video := range m.videos[parentID] {
		if !video.parsed {
			video.identity, video.parsed = subtitleFileIdentity(video.Name), true
		}
		v := video.identity
		if v == nil || v.key != sub.key || v.episode != sub.episode || v.version != sub.version {
			continue
		}
		compatible := true
		for _, pair := range [][2]string{{sub.spec.Resolution, v.spec.Resolution}, {sub.spec.Codec, v.spec.Codec}, {sub.spec.BitDepth, v.spec.BitDepth}, {sub.spec.Container, strings.ToLower(strings.TrimPrefix(path.Ext(video.Name), "."))}} {
			if pair[0] != "" && pair[0] != pair[1] {
				compatible = false
			}
		}
		if !compatible {
			continue
		}
		owners = append(owners, video.ID)
	}
	if len(owners) == 0 {
		return nil, ""
	}
	return owners, sub.tail
}
