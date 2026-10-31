package service

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

var mediaSeasonLabel = regexp.MustCompile(`(?i)^\s*(?:第\s*[0-9一二三四五六七八九十]+\s*季|SEASON[ ._-]*\d+|S\d+)[ ._:：-]*`)
var mediaTitleSeparators = strings.NewReplacer("_", " ", ".", " ", "-", " ", ":", " ", "：", " ")

func mediaExactName(name string, names []string) bool {
	key := mediafacts.NameKey(name)
	for _, other := range names {
		if key != "" && key == mediafacts.NameKey(other) {
			return true
		}
	}
	return false
}

func mediaTitleSuffix(title, base string) string {
	words := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(mediaTitleSeparators.Replace(s)), " "))
	}
	title, base = words(title), words(base)
	if base != "" && strings.HasPrefix(title, base+" ") {
		return mediafacts.NameKey(title[len(base):])
	}
	// CJK titles do not require a space before their seasonal subtitle.
	last, _ := utf8.DecodeLastRuneInString(base)
	key, prefix := mediafacts.NameKey(title), mediafacts.NameKey(base)
	if last > 127 && prefix != "" && strings.HasPrefix(key, prefix) {
		return strings.TrimPrefix(key, prefix)
	}
	return ""
}

func mediaRelatedName(name string, names []string) bool {
	if mediaExactName(name, names) {
		return true
	}
	for _, other := range names {
		if mediaTitleSuffix(name, other) != "" {
			return true
		}
	}
	return false
}

// Season names and dates are recognition evidence, never an episode-count limit.
func mediaRecognitionSeason(identity mediafacts.Identity, detail *tmdb.Detail, names []string, year string) (int, bool) {
	if detail.Kind != "tv" {
		return identity.Season, true
	}
	if identity.SeasonExplicit {
		return identity.Season, year == "" || identity.Season != 1 || strings.HasPrefix(detail.Date, year)
	}
	suffixes := make([]string, 0, 2)
	fileSuffix := false
	for index, title := range []string{identity.Name, identity.ParentName} {
		for _, base := range names {
			if suffix := mediaTitleSuffix(title, base); suffix != "" {
				suffixes = append(suffixes, suffix)
				fileSuffix = fileSuffix || index == 0
			}
		}
	}
	if len(suffixes) == 0 {
		return identity.Season, year == "" || strings.HasPrefix(detail.Date, year)
	}
	named, dated := make([]int, 0, 1), make([]int, 0, 1)
	for _, season := range detail.Seasons {
		if season.Number <= 0 || year != "" && !strings.HasPrefix(season.Date, year) {
			continue
		}
		dated = append(dated, season.Number)
		label := mediafacts.NameKey(mediaSeasonLabel.ReplaceAllString(season.Name, ""))
		for _, suffix := range suffixes {
			if label != "" && suffix == label {
				named = append(named, season.Number)
				break
			}
		}
	}
	if len(named) == 1 {
		return named[0], true
	}
	// Translated subtitles need an exact filename alias in addition to a
	// unique seasonal year. A pack label alone is never season evidence.
	if len(named) == 0 && fileSuffix && mediaExactName(identity.Name, names) && identity.YearFromParent && year != "" && mediaRelatedName(identity.ParentName, names) && len(dated) == 1 {
		return dated[0], true
	}
	if len(named) == 0 && mediaExactName(identity.Name, names) {
		return identity.Season, year == "" || strings.HasPrefix(detail.Date, year)
	}
	return 0, false
}
