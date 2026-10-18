package animeparse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/model"
)

const SpecVersion int64 = 2

const specLanguage = `(?:[简繁](?:体|中|文)?|[日英](?:文|语)?)`
const specLanguageSequence = specLanguage + `(?:[+&/ _-]*` + specLanguage + `)*`

var (
	specTokenCanonical  = strings.NewReplacer(" ", "", ".", "", "_", "", "-", "")
	specTokens          = regexp.MustCompile(`(?i)h[ ._-]?26[45]|[0-9]{1,2}[ ._-]?bit|[a-z0-9]+`)
	specLanguages       = regexp.MustCompile(`^` + specLanguageSequence + `$`)
	specMarkedLanguages = regexp.MustCompile(`(` + specLanguageSequence + `)(?:字幕|内封|内嵌|外挂|硬字幕|软字幕|双语|多语)`)
	specDimensions      = regexp.MustCompile(`(?i)(1920\s*[x×]\s*1080|3840\s*[x×]\s*2160)`)
	specChinese         = strings.NewReplacer("簡", "简", "體", "体", "內", "内", "語", "语", "雙", "双", "嵌入", "内嵌", "外掛", "外挂")
)

// Specification only interprets bracket metadata and the suffix following an
// explicit episode. A character in the series name is not a language tag.
func Specification(title string) model.AnimeSpec {
	spec, _ := specification(title)
	return spec
}

func specification(title string) (model.AnimeSpec, bool) {
	out := model.AnimeSpec{}
	if q := resolution(title); q != nil && *q != "unknown" {
		out.Resolution = *q
	}
	if out.Resolution == "" && !qualityToken.MatchString(title) {
		seen := make(map[string]bool, 2)
		for _, value := range specDimensions.FindAllString(title, -1) {
			if strings.HasPrefix(value, "1920") {
				seen["1080p"] = true
			} else {
				seen["2160p"] = true
			}
		}
		if len(seen) == 1 {
			for value := range seen {
				out.Resolution = value
			}
		}
	}
	blocks := make([]string, 0, 12)
	tail := -1
	for _, index := range brackets.FindAllStringSubmatchIndex(title, -1) {
		body := title[index[2]:index[3]]
		if bracketNumber.MatchString(body) {
			tail = index[1]
			continue
		}
		if strings.Contains(body, "字幕组") || strings.Contains(body, "字幕組") || strings.Contains(body, "字幕社") {
			continue
		}
		blocks = append(blocks, body)
	}
	for _, rule := range []*regexp.Regexp{chineseNumber, seasonEpisode, labeledEpisode, dashedEpisode} {
		for _, index := range rule.FindAllStringSubmatchIndex(title, -1) {
			if rightBoundary(title, index[3]) && index[1] > tail {
				tail = index[1]
			}
		}
	}
	if tail >= 0 && tail < len(title) {
		blocks = append(blocks, brackets.ReplaceAllString(title[tail:], " "))
	}
	languages := make(map[string]bool, 4)
	codecs, containers, depths, modes := make(map[string]bool, 2), make(map[string]bool, 2), make(map[string]bool, 2), make(map[string]bool, 2)
	for _, block := range blocks {
		body := specChinese.Replace(block)
		if strings.Contains(body, "内嵌") || strings.Contains(body, "硬字幕") {
			modes["hard"] = true
		}
		if strings.Contains(body, "内封") || strings.Contains(body, "软字幕") {
			modes["soft"] = true
		}
		if strings.Contains(body, "外挂") {
			modes["external"] = true
		}
		// Language runs must be a complete language tag or immediately
		// qualify a subtitle/multilingual marker. This accepts concatenated
		// publisher tags (繁日双语MP4), without interpreting 日 in a series name.
		fragments := make([]string, 0, 4)
		if specLanguages.MatchString(strings.TrimSpace(body)) {
			fragments = append(fragments, body)
		}
		for _, index := range specMarkedLanguages.FindAllStringSubmatchIndex(body, -1) {
			if index[0] > 0 {
				previous, _ := utf8.DecodeLastRuneInString(body[:index[0]])
				if unicode.Is(unicode.Han, previous) {
					continue
				}
			}
			if index[1] < len(body) {
				next, _ := utf8.DecodeRuneInString(body[index[1]:])
				if unicode.Is(unicode.Han, next) {
					continue
				}
			}
			fragments = append(fragments, body[index[2]:index[3]])
		}
		for _, fragment := range fragments {
			for _, pair := range [][2]string{{"简", "zh-Hans"}, {"繁", "zh-Hant"}, {"日", "ja"}, {"英", "en"}} {
				if strings.Contains(fragment, pair[0]) {
					languages[pair[1]] = true
				}
			}
		}
		for _, token := range specTokens.FindAllString(strings.ToLower(block), -1) {
			token = specTokenCanonical.Replace(token)
			switch token {
			case "chs", "sc", "gb":
				languages["zh-Hans"] = true
			case "cht", "tc", "big5":
				languages["zh-Hant"] = true
			case "jpn", "ja":
				languages["ja"] = true
			case "eng":
				languages["en"] = true
			case "h264", "x264", "avc":
				codecs["h264"] = true
			case "h265", "x265", "hevc":
				codecs["h265"] = true
			case "av1", "vp9":
				codecs[token] = true
			case "mp4", "mkv":
				containers[token] = true
			case "8bit", "10bit", "12bit":
				depths[strings.TrimSuffix(token, "bit")] = true
			}
		}
	}
	one := func(values map[string]bool) string {
		if len(values) == 1 {
			for value := range values {
				return value
			}
		}
		return ""
	}
	out.Codec, out.Container, out.BitDepth, out.SubtitleMode = one(codecs), one(containers), one(depths), one(modes)
	for language := range languages {
		out.Languages = append(out.Languages, language)
	}
	slices.Sort(out.Languages)
	return out, len(codecs) > 1 || len(containers) > 1 || len(depths) > 1 || len(modes) > 1
}

func NormalizeSpec(spec model.AnimeSpec) model.AnimeSpec {
	spec.Languages = append([]string(nil), spec.Languages...)
	slices.Sort(spec.Languages)
	return spec
}

func SpecKey(spec model.AnimeSpec) string {
	data, _ := json.Marshal(NormalizeSpec(spec)) // Fixed scalar model has no failing marshaler.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:12])
}

func SpecLabel(spec model.AnimeSpec) string {
	parts := make([]string, 0, 6)
	if spec.Resolution != "" {
		parts = append(parts, spec.Resolution)
	}
	lang := ""
	for _, pair := range [][2]string{{"zh-Hans", "简"}, {"zh-Hant", "繁"}, {"ja", "日"}, {"en", "英"}} {
		if slices.Contains(spec.Languages, pair[0]) {
			lang += pair[1]
		}
	}
	if lang != "" {
		parts = append(parts, lang)
	}
	if label := map[string]string{"hard": "内嵌", "soft": "内封", "external": "外挂"}[spec.SubtitleMode]; label != "" {
		parts = append(parts, label)
	}
	if spec.Codec != "" {
		parts = append(parts, map[string]string{"h264": "AVC", "h265": "HEVC", "av1": "AV1", "vp9": "VP9"}[spec.Codec])
	}
	if spec.BitDepth != "" {
		parts = append(parts, spec.BitDepth+"bit")
	}
	if spec.Container != "" {
		parts = append(parts, strings.ToUpper(spec.Container))
	}
	return strings.Join(parts, " ")
}

// MatchSpec requires every selected dimension. Missing required title metadata
// is unknown, not a match; unselected dimensions never restrict future titles.
func MatchSpec(profile, observed model.AnimeSpec) (matched, unknown bool) {
	for _, pair := range [][2]string{{profile.Resolution, observed.Resolution}, {profile.SubtitleMode, observed.SubtitleMode}, {profile.Codec, observed.Codec}, {profile.Container, observed.Container}, {profile.BitDepth, observed.BitDepth}} {
		if pair[0] == "" {
			continue
		}
		if pair[1] == "" {
			unknown = true
			continue
		}
		if pair[0] != pair[1] {
			return false, false
		}
	}
	if len(profile.Languages) > 0 {
		if len(observed.Languages) == 0 {
			unknown = true
		} else if !slices.Equal(NormalizeSpec(profile).Languages, NormalizeSpec(observed).Languages) {
			return false, false
		}
	}
	return !unknown, unknown
}
