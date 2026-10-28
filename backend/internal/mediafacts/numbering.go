package mediafacts

import (
	"regexp"
	"strconv"

	"strm115/backend/internal/releaseparse"
)

// Numbering describes explicit file coordinates, not an RSS numbering mapping.
type Numbering struct {
	Season, Episode, EndEpisode int
}

var (
	standardNumbering     = regexp.MustCompile(`(?i)S([0-9]{1,4})\s*E([0-9]{1,5})(?:v[0-9]{1,3})?(?:\s*([-~～〜至到+&])\s*E?([0-9]{1,5})(?:v[0-9]{1,3})?)?`)
	numberingSceneYear    = regexp.MustCompile(`(?i)(S[0-9]{1,4}\s*E[0-9]{1,5}(?:v[0-9]{1,3})?(?:\s*[-~～〜至到+&]\s*E?[0-9]{1,5}(?:v[0-9]{1,3})?)?)\.(19[0-9]{2}|20[0-9]{2}|21[0-9]{2})($|[ ._\-])`)
	numberingContinuation = regexp.MustCompile(`(?i)^\s*[-~～〜至到+&/、,]\s*(?:(?:SP|OVA|EP?|第)\s*)?[0-9]`)
)

// StandardNumbering reads SxxExx before publisher-number inference. Found means
// standard syntax was present; a zero Episode requires review, not a fallback.
// Only the unnumbered remainder uses release parsing to detect competing clues.
func StandardNumbering(stem string) (numbering Numbering, found bool) {
	if !standardNumbering.MatchString(stem) {
		return numbering, false
	}
	stem = numberingSceneYear.ReplaceAllString(stem, "${1} ${2}${3}")
	// Complete resolution fields cannot extend an episode range. Bracket them
	// only in the parsing copy; the filename and declared quality stay untouched.
	spans := resourceQuality.FindAllStringIndex(stem, -1)
	for i := len(spans) - 1; i >= 0; i-- {
		start, end := spans[i][0], spans[i][1]
		if end < len(stem) && numberingAlnum(stem[end]) {
			continue
		}
		stem = stem[:start] + "[" + stem[start:end] + "]" + stem[end:]
	}
	for _, m := range standardNumbering.FindAllStringSubmatchIndex(stem, -1) {
		if m[0] > 0 && numberingAlnum(stem[m[0]-1]) {
			continue
		}
		if found || !numberingEnd(stem, m[1]) || numberingContinuation.MatchString(stem[m[1]:]) {
			return Numbering{}, true
		}
		found = true
		season, _ := strconv.Atoi(stem[m[2]:m[3]])
		episode, _ := strconv.Atoi(stem[m[4]:m[5]])
		end := 0
		if m[8] >= 0 {
			end, _ = strconv.Atoi(stem[m[8]:m[9]])
			join := stem[m[6]:m[7]]
			if end <= episode || end > 10000 || (join == "+" || join == "&") && end != episode+1 {
				return Numbering{}, true
			}
		}
		if season > 1000 || episode < 1 || episode > 10000 {
			return Numbering{}, true
		}
		// Keep a non-metadata placeholder so a number in the work's name does
		// not become a trailing bare episode after removing SxxExx.
		rest := stem[:m[0]] + " NUMBERING " + stem[m[1]:]
		parsed, err := releaseparse.Title(rest)
		if err != nil || parsed.Kind != "unknown" || parsed.RawEpisode != "" {
			return Numbering{}, true
		}
		numbering = Numbering{Season: season, Episode: episode, EndEpisode: end}
	}
	return numbering, found
}

func numberingAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func numberingEnd(value string, end int) bool {
	return end == len(value) || !numberingAlnum(value[end]) &&
		(value[end] != '.' || end+1 == len(value) || value[end+1] < '0' || value[end+1] > '9')
}
