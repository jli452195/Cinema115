package mediafacts

import (
	"path"
	"regexp"
	"strconv"
	"strings"

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
	// 裸年份保留完整片名和“片名＋年份”两种解释，不能直接当作明确声明冲突。
	AlternativeName   string `json:"alternativeName,omitempty"`
	AlternativeYear   string `json:"alternativeYear,omitempty"`
	ContentType       string `json:"contentType,omitempty"`
	NameSource        string `json:"-"`
	YearSource        string `json:"-"`
	KindSource        string `json:"-"`
	TMDBIDSource      string `json:"-"`
	SeasonSource      string `json:"-"`
	ContentTypeSource string `json:"-"`
	Conflict          string `json:"conflict,omitempty"`
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
var identityVersion = regexp.MustCompile(`(?i)(?:^|[ ._-])(?:V|REV)[0-9]{1,3}(?:$|[^a-z0-9])`)

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
		out.KindSource = "filename"
	}
	for i, original := range parts {
		if i > 2 {
			break
		}
		if i > 0 && identityCollection.MatchString(original) {
			continue
		}
		source := "filename"
		if i > 0 {
			source = "parent:" + strconv.Itoa(i)
		}
		value := strings.TrimSpace(original)
		value = identityContentDeclaration(value, source, &out)
		for _, m := range identityID.FindAllStringSubmatch(value, -1) {
			id, _ := strconv.Atoi(m[1])
			if out.TMDBID == 0 {
				out.TMDBID = id
				out.TMDBIDSource = source
			} else if id > 0 && id != out.TMDBID {
				identityAddConflict(&out, "TMDB_ID_CONFLICT")
			}
		}
		value = identityID.ReplaceAllString(value, "")
		if m := identitySeason.FindStringSubmatch(value); m != nil {
			if (out.Kind == "" || i == 0 && specialFile) && (i == 0 || !specialFile) {
				out.Kind = "tv"
				out.KindSource = source
				out.SeasonExplicit = true
				out.SeasonSource = source
				if m[1] != "" {
					out.Season, _ = strconv.Atoi(m[1])
				} else {
					out.Season = chineseSeason(m[2])
				}
			}
		}
		if out.Kind == "" && identityMovie.MatchString(value) && (i == 0 || !specialFile) {
			out.Kind = "movie"
			out.KindSource = source
		}
		value = identityStripGroup(value)
		year := identityYearIndex(value)
		ambiguousYear := year != nil && i == 0 && !tvContext && !identityStrongYear(value, year)
		if year != nil && !ambiguousYear && out.Year == "" {
			out.Year = value[year[0]:year[1]]
			out.YearFromParent = i > 0
			out.YearSource = source
		}
		end := len(value)
		if year != nil && !ambiguousYear {
			end = year[0]
		}
		for _, pattern := range []*regexp.Regexp{identityYearRange, identityEpisode, identitySeason, identitySpec, identitySpecial, identityMovieTag, identityEpisodeCount} {
			if loc := pattern.FindStringIndex(value); loc != nil && loc[0] < end {
				end = loc[0]
			}
		}
		if ambiguousYear {
			if loc := identityVersion.FindStringIndex(value); loc != nil && loc[0] >= year[1] && loc[0] < end {
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
		if ambiguousYear && year[0] < end {
			prefix := strings.Trim(strings.NewReplacer("_", " ", ".", " ").Replace(value[:year[0]]), " []【】()（）- ")
			title = strings.TrimSpace(prefix + " " + strings.NewReplacer("_", " ", ".", " ").Replace(value[year[0]:end]))
		}
		key := NameKey(title)
		numericTitle := i == 0 && len(key) == 4 && year != nil && key != out.Year
		if out.Name == "" && key != "" && (!onlyDigits(key) || numericTitle) && key != "season" && key != "specials" && key != "sp" {
			out.Name = title
			out.NameSource = source
			if ambiguousYear && year[0] < end {
				alternative := strings.Trim(strings.NewReplacer("_", " ", ".", " ").Replace(value[:year[0]]), " []【】()（）- ")
				if NameKey(alternative) != "" && NameKey(alternative) != key {
					out.AlternativeName, out.AlternativeYear = alternative, value[year[0]:year[1]]
				}
			}
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
	indices := identityYearIndices(value, false)
	if len(indices) == 0 {
		return nil
	}
	return indices[len(indices)-1]
}

func identityYearIndices(value string, leading bool) [][]int {
	ranges := append(identityYearRange.FindAllStringIndex(value, -1), resourceQuality.FindAllStringIndex(value, -1)...)
	found := make([][]int, 0, 2)
	for _, m := range identityYear.FindAllStringSubmatchIndex(value, -1) {
		start, end := m[2], m[3]
		if start == 0 && !leading || end < len(value) && numberingAlnum(value[end]) {
			continue
		}
		inRange := false
		for _, span := range ranges {
			inRange = inRange || start >= span[0] && end <= span[1]
		}
		if !inRange {
			found = append(found, []int{start, end})
		}
	}
	return found
}

// 配对括号和多个年份词元提供发行年份证据，单独的裸年份仍可能属于作品标题。
func identityStrongYear(value string, year []int) bool {
	left, right := strings.TrimSpace(value[:year[0]]), strings.TrimSpace(value[year[1]:])
	for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}, {"（", "）"}, {"【", "】"}} {
		if strings.HasSuffix(left, pair[0]) && strings.HasPrefix(right, pair[1]) {
			return true
		}
	}
	return len(identityYearIndices(value, true)) > 1
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
			if m := pattern.FindStringIndex(candidate); m != nil && m[0] == 0 {
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
