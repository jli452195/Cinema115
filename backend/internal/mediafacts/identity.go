package mediafacts

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"strm115/backend/internal/releaseparse"
)

// Identity contains filename declarations, not an established metadata match.
type Identity struct {
	Name   string `json:"name"`
	Year   string `json:"year,omitempty"`
	Kind   string `json:"kind,omitempty"`
	TMDBID int    `json:"tmdbId,omitempty"`
	Season int    `json:"season"`
	// Parent hints are independent declarations, not permission to replace a
	// filename identity with the contents of a mixed collection directory.
	ParentName     string `json:"parentName,omitempty"`
	YearFromParent bool   `json:"yearFromParent,omitempty"`
	SeasonExplicit bool   `json:"seasonExplicit,omitempty"`
}

var identityID = regexp.MustCompile(`(?i)[\[{]tmdb(?:id)?[=-](\d+)[\]}]`)
var identityYear = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])((?:19|20)\d{2})`)
var identityYearRange = regexp.MustCompile(`(?:19|20)\d{2}\s*[-~～至到]\s*(?:19|20)\d{2}`)
var identitySeason = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:S|SEASON[ ._-]*)(\d{1,4})(?:E|$|[^0-9])|第\s*([0-9一二三四五六七八九十]+)\s*季`)
var identityEpisode = regexp.MustCompile(`(?i)(?:^|[ ._\-])S\d{1,4}[ ._-]*E\d+|(?:^|[ ._\-])(?:EPISODE|EP|E)[ ._-]*\d+|[ ._]+-[ ._]*\d+|第\s*\d+\s*[集话話]|[\[【]\d{1,4}(?:v\d+)?[\]】]`)
var identitySpec = regexp.MustCompile(`(?i)(?:^|[ ._\[【(（-])(?:\d{3,4}[pi]|\d{3,4}\s*[x×]\s*\d{3,4}|[48]K|WEB[ ._-]?DL|WEBRIP|BLU[ ._-]?RAY|BD(?:RIP)?|REMUX|HDTV|HEVC|AVC|X26[45]|H[ .]?26[45]|AAC|FLAC|DUAL AUDIO|10BIT|CHS|CHT|SC_TC|GB|BIG5|MP4|MKV)(?:$|[^a-z0-9])`)
var identityGroup = regexp.MustCompile(`(?i)字幕[组組]|(?:SUBS?|RAWS?)$|^(?:SubsPlease|VCB(?:-S)?|Cleo|ANK|LoliHouse|Nix)(?:$|[-_ ])`)
var identitySpecial = regexp.MustCompile(`(?i)(?:^|[ ._\[【(（-])(?:NCOP|NCED|OVA|OAD|SP|PV|CM)(?:[0-9]|$|[^a-z0-9])|[\[【(（](?:SPECIALS?|BONUS|EXTRAS?|特典|番外|特别篇|特別篇)(?:\s*[0-9]+)?[\]】)）]`)
var identityMovie = regexp.MustCompile(`(?i)(?:^|[ ._\[【(（-])(?:MOVIE|FILM|GEKIJOUBAN)(?:$|[^a-z0-9])|剧场版|劇場版`)
var identityMovieTag = regexp.MustCompile(`(?i)[\[【(（](?:MOVIE|FILM)[\]】)）]`)
var identityEpisodeCount = regexp.MustCompile(`全\s*[0-9]+\s*[集话話]`)
var identityCollection = regexp.MustCompile(`(?i)合集|\bcollection\b`)
var identityBareEpisode = regexp.MustCompile(`(?i)^[0-9]{2,4}(?:v[0-9]+)?$`)

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
	tvContext := identitySeason.MatchString(name)
	for _, parent := range parents[:min(len(parents), 2)] {
		tvContext = tvContext || identitySeason.MatchString(parent) || identityEpisodeCount.MatchString(parent)
	}
	fileName, parentChosen := false, false
	specialFile := identitySpecial.MatchString(parts[0])
	if specialFile {
		out.Kind = "tv"
	}
	for i, original := range parts {
		if i > 2 {
			break
		}
		if i > 0 && identityCollection.MatchString(original) {
			continue
		}
		value := strings.TrimSpace(original)
		if m := identityID.FindStringSubmatch(value); m != nil {
			id, _ := strconv.Atoi(m[1])
			if out.TMDBID == 0 {
				out.TMDBID = id
			}
			value = identityID.ReplaceAllString(value, "")
		}
		if m := identitySeason.FindStringSubmatch(value); m != nil {
			if (out.Kind == "" || i == 0 && specialFile) && (i == 0 || !specialFile) {
				out.Kind = "tv"
				out.SeasonExplicit = true
				if m[1] != "" {
					out.Season, _ = strconv.Atoi(m[1])
				} else {
					out.Season = chineseSeason(m[2])
				}
			}
		}
		if out.Kind == "" && identityMovie.MatchString(value) && (i == 0 || !specialFile) {
			out.Kind = "movie"
		}
		value = identityStripGroup(value)
		year := identityYearIndex(value)
		if year != nil && out.Year == "" {
			out.Year = value[year[0]:year[1]]
			out.YearFromParent = i > 0
		}
		end := len(value)
		if year != nil {
			end = year[0]
		}
		for _, pattern := range []*regexp.Regexp{identityYearRange, identityEpisode, identitySeason, identitySpec, identitySpecial, identityMovieTag, identityEpisodeCount} {
			if loc := pattern.FindStringIndex(value); loc != nil && loc[0] < end {
				end = loc[0]
			}
		}
		// Reuse the release parser's ambiguity checks. Bare numbers in film
		// names are retained unless the file has credible episode context.
		if !identityMovie.MatchString(value) {
			parsed, err := releaseparse.Title(value)
			if err == nil && parsed.Kind == "episode" && identityBareEpisode.MatchString(parsed.RawEpisode) && (tvContext || year == nil && strings.HasPrefix(parsed.RawEpisode, "0")) {
				if start := strings.LastIndex(value[:end], parsed.RawEpisode); start > 0 {
					end = start
				}
			}
		}
		title := strings.Trim(strings.NewReplacer("_", " ", ".", " ").Replace(value[:end]), " []【】()（）- ")
		key := NameKey(title)
		numericTitle := i == 0 && len(key) == 4 && year != nil && key != out.Year
		if out.Name == "" && key != "" && (!onlyDigits(key) || numericTitle) && key != "season" && key != "specials" && key != "sp" {
			out.Name = title
		}
		if i == 0 {
			fileName = out.Name != ""
		} else if fileName && !parentChosen && key != "" && !onlyDigits(key) && key != "season" && key != "specials" && key != "sp" {
			parentChosen = true
			if key != NameKey(out.Name) {
				out.ParentName = title
			}
		}
	}
	return out
}

// The year token must stand alone. Leading numeric titles, dimensions and
// collection ranges are not release years; the last remaining year wins.
func identityYearIndex(value string) []int {
	ranges := append(identityYearRange.FindAllStringIndex(value, -1), resourceQuality.FindAllStringIndex(value, -1)...)
	var found []int
	for _, m := range identityYear.FindAllStringSubmatchIndex(value, -1) {
		start, end := m[2], m[3]
		if start == 0 || end < len(value) && numberingAlnum(value[end]) {
			continue
		}
		inRange := false
		for _, span := range ranges {
			inRange = inRange || start >= span[0] && end <= span[1]
		}
		if !inRange {
			found = []int{start, end}
		}
	}
	return found
}

func identityStripGroup(value string) string {
	if !strings.HasPrefix(value, "[") {
		return value
	}
	end := strings.IndexByte(value, ']')
	if end <= 0 {
		return value
	}
	prefix, rest := value[1:end], strings.TrimSpace(value[end+1:])
	known := identityGroup.MatchString(prefix)
	// An unknown compact publisher needs a separate title after it. Never
	// remove [Work title] just because numbering or specifications follow.
	if !known && rest != "" && !strings.ContainsAny(prefix, " \t_") && !onlyDigits(prefix) && len(prefix) == len([]rune(prefix)) {
		candidate := strings.TrimLeft(rest, " ._-[]【】()（）")
		known = !identitySpec.MatchString("[" + prefix + "]")
		for _, pattern := range []*regexp.Regexp{identityEpisode, identitySeason, identitySpec, identitySpecial} {
			if m := pattern.FindStringIndex(rest); m != nil && m[0] == 0 {
				known = false
			}
		}
		if candidate != "" && candidate[0] >= '0' && candidate[0] <= '9' {
			known = false
		}
	}
	if known {
		return rest
	}
	return value
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
