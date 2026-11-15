package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

func (s *mediaRecognitionSearch) choose(ctx context.Context, observations []*mediaRecognitionObservation, mode string) (*mediaRecognition, error) {
	for _, observation := range observations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, identity := observation.item, observation.query.identity
		undated := mediaRecognitionUndated(identity) || s.yearDiscarded
		if undated && mode == "fuzzy" {
			continue
		}
		key := item.Kind + ":" + strconv.Itoa(item.ID)
		if item.ID < 1 || item.Kind != "tv" && item.Kind != "movie" || identity.Kind != "" && item.Kind != identity.Kind || identity.TMDBID > 0 && item.ID != identity.TMDBID {
			continue
		}
		names := []string{item.Title, item.OriginalTitle}
		if mode == "alias" {
			if undated && observation.rank > 3 {
				s.undatedIncomplete = true
				continue
			}
			if undated && s.aliasCalls >= mediaRecognitionAliasLimit {
				if _, cached := s.aliases[key]; !cached {
					s.undatedIncomplete = true
					continue
				}
			}
			aliases, err := s.alternativeNames(ctx, observation)
			if err != nil {
				s.queryError = fmt.Errorf("查询作品别名: %w", err)
				continue
			}
			names = append(names, aliases...)
		}
		if mode == "fuzzy" {
			names = append(names, s.aliases[key]...)
		}
		basis := ""
		for _, name := range []string{identity.ChineseTitle, identity.EnglishTitle, identity.Name} {
			if name == "" {
				continue
			}
			basis, _ = mediaRecognitionNameBasis(observation.query.name, name, item.Kind, names, mode)
			if basis != "" {
				// 完整双语标题是同一来源身份的不同名称，不按零散词放宽。
				identity.Name = name
				break
			}
		}
		if basis == "" {
			continue
		}
		if identity.YearFromParent && !identity.YearExplicit && identity.ParentName != "" && !mediaRelatedName(identity.ParentName, names) {
			identity.Year = ""
			undated, s.yearDiscarded, s.undatedSearched = true, true, true
		}
		if undated && mode == "fuzzy" {
			continue
		}
		if undated && s.detailCalls >= mediaRecognitionDetailLimit {
			if _, cached := s.details[key]; !cached {
				s.undatedIncomplete = true
				continue
			}
		}
		detail, err := s.detail(ctx, item.Kind, item.ID)
		if err != nil {
			return nil, fmt.Errorf("查询作品候选详情: %w", err)
		}
		if detail == nil || detail.ID != item.ID || detail.Kind != item.Kind {
			if undated {
				s.undatedUncertain = true
			}
			continue
		}
		if code, _ := mediaRecognitionContentType(identity.ContentType, detail); code != "" {
			if undated && code == "CONTENT_TYPE_UNKNOWN" {
				s.undatedUncertain = true
			}
			continue
		}
		year := identity.Year
		if identity.YearFromParent && identity.ParentName != "" {
			related := mediaRelatedName(identity.ParentName, names)
			seasonPack := related && identity.SeasonExplicit && !mediaExactName(identity.ParentName, names) &&
				(!identity.YearExplicit && identity.Season != 1 || mediaRecognitionSeasonSubtitle(identity.ParentName, detail, names))
			if !related && !identity.YearExplicit || seasonPack {
				// 明确目录年份只在副标题得到季资料印证时忽略，不能把地区版等同名前缀当作季度。
				year = ""
			}
		}
		if year == "" && (identity.TMDBID == 0 || identity.Kind != "tv" && identity.Kind != "movie") {
			undated = true
			s.undatedSearched = true
			if identity.Year != "" {
				// 已放弃的弱年份不能再让后续同名候选按首条短路。
				s.yearDiscarded = true
				identity.Year = ""
			}
		}
		if undated && mode == "fuzzy" {
			continue
		}
		if item.Kind == "movie" && year != "" && !strings.HasPrefix(detail.Date, year) {
			continue
		}
		season, ok, err := s.season(ctx, identity, detail, names, year)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if s.queryError != nil {
			return nil, s.queryError
		}
		if undated {
			if s.undatedMatch != nil && s.undatedKey != key {
				s.decision.ResultCode, s.decision.Message = "AMBIGUOUS_TITLE", "来源没有年份，存在多个符合条件的同名作品，请补充年份或指定作品"
				s.undatedTerminal = true
				return nil, nil
			}
			if s.undatedMatch == nil {
				s.undatedMatch = &mediaRecognition{Detail: detail, Season: season}
				s.undatedBasis, s.undatedKey = basis, key
			}
			continue
		}
		if s.undatedMatch != nil || s.undatedSearched && (s.undatedIncomplete || s.undatedUncertain) {
			return s.finishUndated(), nil
		}
		// 第一条合格结果核实后立即返回，不为统计候选数量继续请求。
		s.decision.ResultCode, s.decision.Message = mediaRecognitionBasisCode(basis), "已匹配作品"
		return &mediaRecognition{Detail: detail, Season: season}, nil
	}
	return nil, nil
}

func mediaRecognitionNameBasis(query, name, kind string, names []string, mode string) (string, float64) {
	if mode == "fuzzy" {
		querySimilarity := mediaRecognitionSimilarity(query, names)
		if querySimilarity == 0 {
			return "", 0
		}
		nameSimilarity := mediaRecognitionSimilarity(name, names)
		if mediaExactName(name, names) || kind == "tv" && mediaRelatedName(name, names) {
			nameSimilarity = 1
		}
		if nameSimilarity == 0 {
			return "", 0
		}
		return "fuzzy", min(querySimilarity, nameSimilarity)
	}
	if !mediaExactName(query, names) || !mediaExactName(name, names) && (kind != "tv" || !mediaRelatedName(name, names)) {
		return "", 0
	}
	if mode == "alias" {
		directNames := names[:min(2, len(names))]
		if !mediaExactName(name, directNames) && (kind != "tv" || !mediaRelatedName(name, directNames)) {
			return "alias", 0
		}
	}
	for index, candidate := range names {
		if !mediaExactName(query, []string{candidate}) {
			continue
		}
		if index == 0 {
			return "exact_title", 0
		}
		if index == 1 {
			return "original_title", 0
		}
		return "alias", 0
	}
	return "", 0
}

func mediaRecognitionBasisCode(basis string) string {
	switch basis {
	case "alias":
		return "ALIAS_EXACT"
	case "fuzzy":
		return "TITLE_FUZZY"
	default:
		return "TITLE_EXACT"
	}
}

func (s *mediaRecognitionSearch) detail(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	key := kind + ":" + strconv.Itoa(id)
	if detail, ok := s.details[key]; ok {
		return detail, s.detailErrors[key]
	}
	if s.detailCalls >= mediaRecognitionDetailLimit {
		return nil, fmt.Errorf("本次作品详情查询已达到数量上限")
	}
	s.detailCalls++
	lookup := ctx
	if source, ok := s.source.(interface {
		PinNumbering(context.Context, int) (context.Context, error)
	}); ok && kind == "tv" {
		var err error
		lookup, err = source.PinNumbering(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("固定候选季集编排: %w", err)
		}
		if s.seasonContexts == nil {
			s.seasonContexts = make(map[int]context.Context, mediaRecognitionSeasonLimit)
		}
		s.seasonContexts[id] = lookup
	}
	detail, err := s.source.Details(lookup, kind, id)
	if errors.Is(err, tmdb.ErrNotFound) {
		detail, err = nil, nil
	}
	s.details[key], s.detailErrors[key] = detail, err
	return detail, err
}

func (s *mediaRecognitionSearch) alternativeNames(ctx context.Context, observation *mediaRecognitionObservation) ([]string, error) {
	key := observation.item.Kind + ":" + strconv.Itoa(observation.item.ID)
	if names, ok := s.aliases[key]; ok {
		return names, s.aliasErrors[key]
	}
	source, ok := s.source.(interface {
		AlternativeNames(context.Context, string, int) ([]string, error)
	})
	if !ok || observation.rank > 3 {
		return nil, nil
	}
	if s.aliasCalls >= mediaRecognitionAliasLimit {
		return nil, nil
	}
	s.aliasCalls++
	names, err := source.AlternativeNames(ctx, observation.item.Kind, observation.item.ID)
	// 别名只保留本次比较所需的有界文本，外部返回不能使任务快照无限增长。
	if len(names) > 50 {
		s.undatedIncomplete = true
		names = names[:50]
	}
	bounded := make([]string, 0, len(names))
	for _, name := range names {
		if len([]rune(name)) > 200 {
			s.undatedIncomplete = true
			continue
		}
		bounded = append(bounded, name)
	}
	names = bounded
	s.aliases[key], s.aliasErrors[key] = names, err
	return names, err
}

func mediaRecognitionSimilarity(name string, names []string) float64 {
	key := mediafacts.NameKey(name)
	left := []rune(key)
	if len(left) < 5 || len(left) > 100 {
		return 0
	}
	best := 0.0
	for _, candidate := range names {
		other := mediafacts.NameKey(candidate)
		right := []rune(other)
		if key == other {
			return 1
		}
		if len(right) < 5 || len(right) > 100 {
			continue
		}
		// 数字、续作序号和直接增减尾词不靠编辑距离放宽，避免把相邻作品视为拼写错误。
		if strings.HasPrefix(key, other) || strings.HasPrefix(other, key) || mediaRecognitionDigits(left) != mediaRecognitionDigits(right) {
			continue
		}
		difference := len(left) - len(right)
		if difference < -2 || difference > 2 {
			continue
		}
		distance := mediaRecognitionEditDistance(left, right)
		if distance > 2 {
			continue
		}
		score := 1 - float64(distance)/float64(max(len(left), len(right)))
		if score >= 0.84 && score > best {
			best = score
		}
	}
	return best
}

func mediaRecognitionDigits(value []rune) string {
	var result strings.Builder
	lastDigit := false
	for _, token := range value {
		if unicode.IsDigit(token) {
			if !lastDigit {
				result.WriteByte('|')
			}
			result.WriteRune(token)
			lastDigit = true
			continue
		}
		lastDigit = false
	}
	return result.String()
}

func mediaRecognitionEditDistance(left, right []rune) int {
	previous, current := make([]int, len(right)+1), make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, token := range left {
		current[0] = i + 1
		for j, other := range right {
			cost := 0
			if token != other {
				cost = 1
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, previous[j]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}
