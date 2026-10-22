// Package animeparse interprets publisher metadata without network access,
// source binding, TMDB numbering, selection, or download side effects.
package animeparse

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/model"
)

const ParserVersion int64 = 6

var ErrInput = errors.New("invalid anime parser input")

// These grammars deliberately require publisher numbering syntax. An arbitrary
// number in a series name, codec, CRC, size, year or season is not an episode.
const (
	number     = `[0-9]{1,6}(?:\.[0-9]{1,3})?[ABab]?(?:\s*[vV][0-9]{1,3})?`
	numberJoin = `\s*[-~～〜至到+&/、,]\s*(?:(?i:SP|OVA|EP?|第)\s*)?`
	expression = number + `(?:` + numberJoin + number + `)*`
)

var (
	brackets       = regexp.MustCompile(`[\[【(（]([^\[\]【】()（）]+)[\]】)）]`)
	bracketNumber  = regexp.MustCompile(`(?i)^\s*(` + expression + `)(?:\s*(?:END|FIN|完|完结|完結))*\s*$`)
	chineseNumber  = regexp.MustCompile(`第\s*(` + expression + `)\s*[话話集回]`)
	seasonEpisode  = regexp.MustCompile(`(?i)S[0-9]{1,2}\s*E(` + expression + `)`)
	labeledEpisode = regexp.MustCompile(`(?i)(?:EPISODE|EP|E)\s*#?\s*(` + expression + `)`)
	dashedEpisode  = regexp.MustCompile(`(?:^|[\s|：:])-\s*(` + expression + `)`)
	bareEpisode    = regexp.MustCompile(`(?:^|[\s_])(` + expression + `)`)
	seasonMetadata = regexp.MustCompile(`(?i)SEASON\s*[0-9]{1,3}`)
	// More than one numbering system in a title needs confirmation, even when
	// one of them looks like a perfectly ordinary standalone bracket number.
	ordinalEpisode = regexp.MustCompile(`(?i)[0-9]{1,2}(?:st|nd|rd|th)\s*-\s*(` + expression + `)`)
	rangeEpisode   = regexp.MustCompile(number + numberJoin + number + `(?:` + numberJoin + number + `)*`)
	titleRangeTail = regexp.MustCompile(`(?i)^\s*(?:[（(]\s*)?(?:19[0-9]{2}|20[0-9]{2}|21[0-9]{2})(?:$|[^0-9])|^\s*SEASON\s*[0-9]{1,3}(?:$|[^0-9])`)
	titleFraction  = regexp.MustCompile(`^[1-9][0-9]?/[1-9][0-9]?$`)
	batchWords     = regexp.MustCompile(`(?i)合集|全集|全\s*[0-9]+\s*[话話集]|\bbatch\b|\bcomplete\b`)
	specialWords   = regexp.MustCompile(`(?i)(?:SPECIAL|SAMPLE|TRAILER|NCOP|NCED|OVA|OAD|SP|PV|CM|OP|ED)(?:\s*[-#]?\s*` + number + `)?|特别篇|特別篇|特典|番外|样片|樣片|总集篇|總集篇|先行预告|先行預告`)
	qualityToken   = regexp.MustCompile(`(?i)[0-9]{3,4}[pi]|[48]k`)
	versionToken   = regexp.MustCompile(`(?i)v[0-9]{1,3}|rev[0-9]{1,3}|修正版|修正|重发|重發|修复版|修復版`)
	decimalOrPart  = regexp.MustCompile(`(?i)\.[0-9]|[ab](?:\s*v[0-9]+)?$`)
	joinedNumber   = regexp.MustCompile(`[-~～〜至到+&/、,]`)
	bareTail       = regexp.MustCompile(`(?i)^(?:(?:\[[^\]]*\]|【[^】]*】|\([^)]*\)|（[^）]*）)|(?:[0-9]{3,4}[pi]|[48]k|GB|BIG5|CHS|CHT|SC|TC|简体|簡體|繁体|繁體|简繁|簡繁|HEVC|AVC|AV1|AAC|FLAC|MP4|MKV|WEB-DL|WEBRIP|BDRIP|END|FIN|完结|完結)(?:$|[\s_.]))`)
)

type candidate struct {
	start, end int
	kind       string
	episode    int
}

// Title returns only an unambiguous ordinary integer as Episode. RawEpisode
// retains the publisher's actual notation; special/batch/composite/unknown
// results never carry an executable ordinary episode number.
func Title(title string) (model.AnimeReleaseParse, error) {
	out := model.AnimeReleaseParse{SchemaVersion: model.AnimeSchemaVersion, Kind: "unknown"}
	if !validText(title, 1024) || strings.TrimSpace(title) == "" {
		return out, ErrInput
	}
	out.Resolution = resolution(title)
	out.Spec, out.SpecAmbiguous = specification(title)
	if out.Resolution != nil && *out.Resolution == "unknown" && out.Spec.Resolution != "" {
		value := out.Spec.Resolution
		out.Resolution = &value
	}
	out.VersionLabel = version(title)
	// "Season 2 - 01" declares season 2 and episode 01. Do not let the
	// generic range/bare-number rules turn the season counter into "2 - 01".
	// Retain the original title and offsets as evidence, and keep genuine
	// episode ranges after the separator available to the other grammars.
	seasonSpans := seasonMetadata.FindAllStringIndex(title, -1)
	metadataSpans := make([][]int, 0, len(seasonSpans))
	for _, span := range seasonSpans {
		if leftBoundary(title, span[0]) && rightBoundary(title, span[1]) {
			metadataSpans = append(metadataSpans, span)
		}
	}
	candidates := make([]candidate, 0, 8)
	add := func(start, end int, kind string, strong bool) {
		if start < 0 || end <= start {
			return
		}
		for _, span := range metadataSpans {
			if start < span[1] && end > span[0] {
				return
			}
		}
		raw := title[start:end]
		n := 0
		if kind == "" {
			kind = "episode"
			switch {
			case joinedNumber.MatchString(raw):
				kind = "batch"
			case decimalOrPart.MatchString(raw):
				kind = "special"
			default:
				base := strings.TrimSpace(stripVersionSuffix(raw))
				var err error
				n, err = strconv.Atoi(base)
				if err != nil || n < 0 || n > 99999 {
					kind = "unknown"
				} else if !strong && metadataNumber(n) {
					return
				}
			}
		}
		candidates = append(candidates, candidate{start: start, end: end, kind: kind, episode: n})
	}
	for _, b := range brackets.FindAllStringSubmatchIndex(title, -1) {
		body := title[b[2]:b[3]]
		if m := bracketNumber.FindStringSubmatchIndex(body); m != nil {
			add(b[2]+m[2], b[2]+m[3], "", false)
		}
	}
	for _, rule := range []struct {
		re       *regexp.Regexp
		boundary bool
	}{{chineseNumber, false}, {seasonEpisode, true}, {labeledEpisode, true}, {dashedEpisode, false}} {
		for _, m := range rule.re.FindAllStringSubmatchIndex(title, -1) {
			if rule.boundary && !leftBoundary(title, m[0]) || !rightBoundary(title, m[3]) {
				continue
			}
			// Chinese suffixes are part of the explicit syntax, not a token
			// boundary. Other matches must not consume a prefix of "01xyz".
			add(m[2], m[3], "", rule.re == chineseNumber || rule.re == seasonEpisode || rule.re == labeledEpisode)
		}
	}
	for _, m := range ordinalEpisode.FindAllStringIndex(title, -1) {
		if leftBoundary(title, m[0]) && rightBoundary(title, m[1]) {
			add(m[0], m[1], "composite", true)
		}
	}
	for _, m := range rangeEpisode.FindAllStringIndex(title, -1) {
		if leftBoundary(title, m[0]) && rightBoundary(title, m[1]) && rangeNumberingPosition(title, m[0], m[1]) {
			add(m[0], m[1], "batch", true)
		}
	}
	for _, m := range bareEpisode.FindAllStringSubmatchIndex(title, -1) {
		raw := title[m[2]:m[3]]
		// Unlabelled single digits are too easily part of a series title.
		if len(raw) < 2 || !rightBoundary(title, m[3]) || !metadataTail(title[m[3]:]) {
			continue
		}
		add(m[2], m[3], "", false)
	}
	for _, m := range batchWords.FindAllStringIndex(title, -1) {
		add(m[0], m[1], "batch", true)
	}
	for _, m := range specialWords.FindAllStringIndex(title, -1) {
		if title[m[0]] >= utf8.RuneSelf || leftBoundary(title, m[0]) && rightBoundary(title, m[1]) {
			add(m[0], m[1], "special", true)
		}
	}
	// Merge overlapping evidence (e.g. EP01-03 and its range). Keep every
	// non-overlapping numbering system instead of preferring the first/last.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].start == candidates[j].start {
			return candidates[i].end > candidates[j].end
		}
		return candidates[i].start < candidates[j].start
	})
	merged := make([]candidate, 0, len(candidates))
	for _, c := range candidates {
		if len(merged) > 0 && c.start < merged[len(merged)-1].end {
			last := &merged[len(merged)-1]
			if c.end > last.end {
				last.end = c.end
			}
			if kindRank(c.kind) > kindRank(last.kind) {
				last.kind, last.episode = c.kind, c.episode
			}
			continue
		}
		merged = append(merged, c)
	}
	if len(merged) == 0 {
		return out, nil
	}
	parts := make([]string, 0, len(merged))
	best := merged[0]
	for _, c := range merged {
		parts = append(parts, title[c.start:c.end])
		if kindRank(c.kind) > kindRank(best.kind) {
			best = c
		}
	}
	out.RawEpisode = strings.Join(parts, " / ")
	if utf8.RuneCountInString(out.RawEpisode) > 128 {
		// The complete title remains available on the release. Do not truncate
		// ambiguous evidence into what looks like a confirmed single number.
		out.RawEpisode = ""
		return out, nil
	}
	out.Kind = best.kind
	if len(merged) > 1 && kindRank(best.kind) < kindRank("special") {
		out.Kind = "composite"
	}
	if out.Kind == "episode" {
		n := best.episode
		out.Episode = &n
	}
	return out, nil
}

// Exclude digits embedded in a named title field, rather than preferring the
// last number. Explicit 第/EP/SxxExx/dash grammars above remain authoritative;
// numeric bracket fields and other ranges retain their batch evidence.
func rangeNumberingPosition(title string, start, end int) bool {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(title[:start])
		if unicode.IsLetter(previous) || unicode.IsDigit(previous) {
			return false
		}
	}
	// Pure numeric fields, including mixed packs, keep their range evidence.
	// A compact fraction after a name in [Ranma 1/2] belongs to that name;
	// [01/02] and [Show 01-12] must still block single-episode execution.
	for _, b := range brackets.FindAllStringSubmatchIndex(title, -1) {
		if start >= b[2] && end <= b[3] {
			prefix := strings.TrimSpace(title[b[2]:start])
			if prefix == "" {
				return true
			}
			if strings.ContainsFunc(prefix, unicode.IsLetter) && titleFraction.MatchString(title[start:end]) {
				return false
			}
		}
	}
	if metadataTail(title[end:]) {
		return true
	}
	// Year/season text after a number belongs to the series-title portion,
	// before the publisher's actual episode field. Other unlabelled ranges
	// remain conservative batch evidence, even if their tail is unfamiliar.
	if !titleRangeTail.MatchString(title[end:]) {
		return true
	}
	prefix := strings.TrimSpace(title[:start])
	previous, _ := utf8.DecodeLastRuneInString(prefix)
	return !unicode.IsLetter(previous)
}

func kindRank(kind string) int {
	switch kind {
	case "batch":
		return 5
	case "composite":
		return 4
	case "special":
		return 3
	case "unknown":
		return 2
	default:
		return 1
	}
}

func metadataNumber(n int) bool {
	if n >= 1900 && n <= 2199 {
		return true
	}
	switch n {
	case 264, 265, 480, 576, 720, 1080, 1440, 3840, 4320, 7680:
		return true
	}
	return false
}

func stripVersionSuffix(raw string) string {
	if i := strings.IndexAny(raw, "vV"); i >= 0 {
		return raw[:i]
	}
	return raw
}

func asciiAlnum(b byte) bool {
	return b >= '0' && b <= '9' || asciiLetter(b)
}

func asciiLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

func leftBoundary(s string, i int) bool { return i == 0 || !asciiAlnum(s[i-1]) }
func rightBoundary(s string, i int) bool {
	if i == len(s) {
		return true
	}
	if asciiAlnum(s[i]) {
		return false
	}
	return s[i] != '.' || i+1 == len(s) || s[i+1] < '0' || s[i+1] > '9'
}

func metadataTail(s string) bool {
	s = strings.TrimSpace(s)
	for s != "" {
		m := bareTail.FindStringIndex(s)
		if m == nil {
			return false
		}
		s = strings.TrimLeft(s[m[1]:], " \t_.")
	}
	return true
}

func resolution(title string) *string {
	value := "unknown"
	seen := false
	for _, m := range qualityToken.FindAllStringIndex(title, -1) {
		if !leftBoundary(title, m[0]) || !rightBoundary(title, m[1]) {
			continue
		}
		token := strings.ToLower(title[m[0]:m[1]])
		if token == "4k" {
			token = "2160p"
		}
		if token != "720p" && token != "1080p" && token != "2160p" {
			value = "unknown"
			return &value
		}
		if seen && token != value {
			value = "unknown"
			return &value
		}
		value, seen = token, true
	}
	return &value
}

func version(title string) string {
	labels := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	for _, m := range versionToken.FindAllStringIndex(title, -1) {
		// Permit the attached publisher notation 01v2, but not codec AV1.
		if m[0] > 0 && asciiLetter(title[m[0]-1]) || !rightBoundary(title, m[1]) {
			continue
		}
		raw := title[m[0]:m[1]]
		key := strings.ToLower(raw)
		if !seen[key] {
			labels = append(labels, raw)
			seen[key] = true
		}
	}
	label := strings.Join(labels, " / ")
	if utf8.RuneCountInString(label) > 64 {
		return ""
	}
	return label
}

func validText(s string, max int) bool {
	if len(s) > max*utf8.UTFMax || !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
