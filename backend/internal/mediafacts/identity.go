package mediafacts

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Identity contains filename declarations, not an established metadata match.
type Identity struct {
	Name   string `json:"name"`
	Year   string `json:"year,omitempty"`
	Kind   string `json:"kind,omitempty"`
	TMDBID int    `json:"tmdbId,omitempty"`
	Season int    `json:"season"`
}

var identityID = regexp.MustCompile(`(?i)[\[{]tmdb(?:id)?[=-](\d+)[\]}]`)
var identityYear = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})(?:$|[^0-9])`)
var identitySeason = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:S|SEASON[ ._-]*)(\d{1,3})(?:E|$|[^0-9])|第\s*([0-9一二三四五六七八九十]+)\s*季`)
var identityEpisode = regexp.MustCompile(`(?i)(?:^|[ ._\-])S\d{1,3}[ ._-]*E\d+|(?:^|[ ._\-])(?:EPISODE|EP|E)[ ._-]*\d+|[ ._]+-[ ._]*\d+|第\s*\d+\s*[集话話]|[\[【]\d{1,4}(?:v\d+)?[\]】]`)
var identitySpec = regexp.MustCompile(`(?i)(?:^|[ ._\[【(（-])(?:\d{3,4}[pi]|[48]K|WEB[ ._-]?DL|WEBRIP|BLU[ ._-]?RAY|BDRIP|REMUX|HDTV|HEVC|AVC|X26[45]|H[ .]?26[45]|AAC|FLAC|DUAL AUDIO|10BIT|CHS|CHT|SC_TC)(?:$|[^a-z0-9])`)
var identityGroup = regexp.MustCompile(`(?i)字幕[组組]|SUBS?|RAWS?|VCB|Cleo|ANK|LoliHouse|Nix`)

func NameKey(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

// ParseIdentity uses the file first and at most two meaningful parent folders.
// Generic download/library roots must be excluded by the caller.
func ParseIdentity(name string, parents ...string) Identity {
	out := Identity{Season: 1}
	parts := append([]string{strings.TrimSuffix(name, path.Ext(name))}, parents...)
	for i, original := range parts {
		if i > 2 {
			break
		}
		value := original
		if m := identityID.FindStringSubmatch(value); m != nil {
			id, _ := strconv.Atoi(m[1])
			if out.TMDBID == 0 {
				out.TMDBID = id
			}
			value = identityID.ReplaceAllString(value, "")
		}
		if m := identitySeason.FindStringSubmatch(value); m != nil {
			if out.Kind == "" {
				out.Kind = "tv"
				if m[1] != "" {
					out.Season, _ = strconv.Atoi(m[1])
				} else {
					out.Season = chineseSeason(m[2])
				}
			}
		}
		if m := identityYear.FindStringSubmatch(value); m != nil && out.Year == "" {
			out.Year = m[1]
		}
		// Publisher prefixes are auxiliary; a bracketed work title is retained.
		if strings.HasPrefix(value, "[") {
			if end := strings.IndexByte(value, ']'); end > 0 && identityGroup.MatchString(value[1:end]) {
				value = value[end+1:]
			}
		}
		end := len(value)
		for _, pattern := range []*regexp.Regexp{identityYear, identityEpisode, identitySeason, identitySpec} {
			if loc := pattern.FindStringIndex(value); loc != nil && loc[0] < end {
				end = loc[0]
			}
		}
		title := strings.Trim(strings.NewReplacer("_", " ", ".", " ").Replace(value[:end]), " []【】()（）- ")
		key := NameKey(title)
		if out.Name == "" && key != "" && !onlyDigits(key) && key != "season" && key != "specials" && key != "sp" {
			out.Name = title
		}
	}
	return out
}

func onlyDigits(value string) bool {
	return strings.Trim(value, "0123456789") == ""
}

func chineseSeason(value string) int {
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	digits := map[rune]int{'一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	n, last := 0, 0
	for _, r := range value {
		if r == '十' {
			if last == 0 {
				last = 1
			}
			n += last * 10
			last = 0
		} else {
			last = digits[r]
		}
	}
	return n + last
}
