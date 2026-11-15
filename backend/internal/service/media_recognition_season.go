package service

import (
	"context"
	"errors"
	"fmt"
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

// 地区版、续作等前缀相同的作品不能仅靠名称后缀被当成季包目录。
func mediaRecognitionSeasonSubtitle(parent string, detail *tmdb.Detail, names []string) bool {
	for _, name := range names {
		suffix := mediaTitleSuffix(parent, name)
		if suffix == "" {
			continue
		}
		for _, season := range detail.Seasons {
			if suffix == mediafacts.NameKey(mediaSeasonLabel.ReplaceAllString(season.Name, "")) {
				return true
			}
		}
	}
	return false
}

// 季名称和日期用于识别作品，不作为文件集号上限。
func mediaRecognitionSeason(identity mediafacts.Identity, detail *tmdb.Detail, names []string, year string) (int, bool) {
	if detail.Kind != "tv" {
		return identity.Season, true
	}
	if identity.SeasonExplicit {
		if year == "" || strings.HasPrefix(detail.Date, year) {
			return identity.Season, true
		}
		for _, season := range detail.Seasons {
			if season.Number == identity.Season && strings.HasPrefix(season.Date, year) {
				return identity.Season, true
			}
		}
		return identity.Season, false
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

type mediaRecognitionSeasonKey struct {
	id, season int
}

// 季日期证据只用于本次作品匹配，不参与分组，也不限制文件集号。
type mediaRecognitionSeasonEvidence struct {
	years      map[string]bool
	incomplete bool
}

func (s *mediaRecognitionSearch) rejectSeason(code, message string) {
	if s.seasonRejection.ResultCode == "" || code == "SEASON_METADATA_INSUFFICIENT" && s.seasonRejection.ResultCode == "YEAR_MISMATCH" {
		s.seasonRejection = MediaRecognitionDecision{ResultCode: code, Message: message}
	}
}

func (s *mediaRecognitionSearch) season(ctx context.Context, identity mediafacts.Identity, detail *tmdb.Detail, names []string, year string) (int, bool, error) {
	if (mediaRecognitionUndated(identity) || s.yearDiscarded) && year == "" && detail.Kind == "tv" && identity.SeasonExplicit {
		for _, season := range detail.Seasons {
			if season.Number == identity.Season {
				return identity.Season, true, nil
			}
		}
		if len(detail.Seasons) == 0 {
			s.undatedUncertain = true
			s.rejectSeason("SEASON_METADATA_INSUFFICIENT", "来源没有年份，候选缺少季资料，无法核对明确季号，请指定作品")
		} else {
			s.rejectSeason("SEASON_MISMATCH", "候选不包含来源声明的季号，请核对作品")
		}
		return identity.Season, false, nil
	}
	number, ok := mediaRecognitionSeason(identity, detail, names, year)
	if ok || detail.Kind != "tv" || !identity.SeasonExplicit {
		return number, ok, nil
	}
	// 年份不同只补查明确指定的季，不能用其他季度的日期解释当前季。
	found := false
	for _, season := range detail.Seasons {
		found = found || season.Number == identity.Season
	}
	if !found {
		s.rejectSeason("SEASON_METADATA_INSUFFICIENT", "候选首播年份不符，且缺少对应季信息，无法确认作品")
		return number, false, nil
	}
	source, supported := s.source.(interface {
		Season(context.Context, int, int) (*tmdb.SeasonDetail, error)
	})
	if !supported {
		s.rejectSeason("SEASON_METADATA_INSUFFICIENT", "候选首播年份不符，无法读取对应季日期，请确认作品")
		return number, false, nil
	}
	key := mediaRecognitionSeasonKey{id: detail.ID, season: identity.Season}
	evidence, cached := s.seasonEvidence[key]
	if !cached {
		if s.seasonCalls >= mediaRecognitionSeasonLimit {
			s.rejectSeason("SEASON_METADATA_INSUFFICIENT", "季日期核验已达查询上限，请指定对应作品后重试")
			return number, false, nil
		}
		s.seasonCalls++
		lookup := ctx
		if pinned := s.seasonContexts[detail.ID]; pinned != nil {
			lookup = pinned
		}
		season, err := source.Season(lookup, detail.ID, identity.Season)
		if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
			return number, false, fmt.Errorf("核对候选季日期: %w", err)
		}
		evidence = mediaRecognitionSeasonEvidence{incomplete: true}
		if err == nil && season != nil && season.Number == identity.Season && len(season.Episodes) > 0 {
			evidence.years = make(map[string]bool, 4)
			evidence.incomplete = false
			for _, episode := range season.Episodes {
				date := mediaRecognitionYear(episode.Date)
				if date == "" {
					evidence.incomplete = true
					continue
				}
				evidence.years[date] = true
			}
		}
		if s.seasonEvidence == nil {
			s.seasonEvidence = make(map[mediaRecognitionSeasonKey]mediaRecognitionSeasonEvidence, mediaRecognitionSeasonLimit)
		}
		s.seasonEvidence[key] = evidence
	}
	if evidence.years[year] {
		return number, true, nil
	}
	if evidence.incomplete {
		s.rejectSeason("SEASON_METADATA_INSUFFICIENT", "候选首播年份不符，对应季日期资料不足，请确认作品")
	} else {
		s.rejectSeason("YEAR_MISMATCH", "来源年份与候选作品首播及对应季日期不符，请确认作品")
	}
	return number, false, nil
}
