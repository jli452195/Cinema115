package service

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/model"
)

const animeInferenceMaxItems = 500

// AnimeEpisodeSuggestion 只描述 Bangumi 原编号到 TMDB 单季集号的常量偏移，
// 供编辑器解释建议，不自动更改订阅。
type AnimeEpisodeSuggestion struct {
	Status      string                       `json:"status"`
	Reason      string                       `json:"reason"`
	Season      int                          `json:"season"`
	Offset      int                          `json:"offset"`
	First       int                          `json:"first"`
	Last        int                          `json:"last"`
	TargetFirst int                          `json:"targetFirst"`
	TargetLast  int                          `json:"targetLast"`
	Matched     int                          `json:"matched"`
	Compared    int                          `json:"compared"`
	Pairs       []AnimeEpisodeSuggestionPair `json:"pairs"`
}

// AnimeEpisodeSuggestionPair 保存一组独立的章节对应证据。
type AnimeEpisodeSuggestionPair struct {
	BangumiNumber int    `json:"bangumiNumber"`
	TMDBEpisode   int    `json:"tmdbEpisode"`
	Date          string `json:"date,omitempty"`
	Evidence      string `json:"evidence"`
}

type animeEpisodePoint struct {
	number       int
	date         time.Time
	dateOK       bool
	titles       []string
	dateUnique   bool
	titleMatches map[int]bool
}

type animeEpisodeCandidate struct {
	season   int
	offset   int
	anchors  []AnimeEpisodeSuggestionPair
	compared int
	missing  int
	conflict bool
}

// InferAnimeEpisodes 只推导有独立日期或具体标题支持的单季常量偏移。
// 连载期间两站的资料都可能不全，因此不以两侧集数相等作为匹配证据。
func InferAnimeEpisodes(chapters []bangumi.Episode, seasons []model.AnimeNamingSeason) AnimeEpisodeSuggestion {
	out := AnimeEpisodeSuggestion{Status: "unresolved", Reason: "集数对应证据不足，保留手动设置", Pairs: []AnimeEpisodeSuggestionPair{}}
	points, first, last, ok := animeInferenceChapters(chapters)
	if !ok {
		out.Status, out.Reason = "conflict", "Bangumi 原编号含小数或重复值，无法按连续正片推导"
		return out
	}
	if len(points) < 2 {
		out.Reason = "Bangumi 正片章节不足，至少需要两个独立对应点"
		return out
	}
	if len(points) > animeInferenceMaxItems {
		out.Reason = "Bangumi 章节范围过大，暂不自动推导"
		return out
	}
	out.First, out.Last = first, last
	if len(seasons) == 0 {
		out.Reason = "TMDB 尚无可用季集资料"
		return out
	}

	candidates := make([]animeEpisodeCandidate, 0, 4)
	seenSeason := make(map[int]bool, len(seasons))
	seenEpisodeIDs := make(map[string]bool, len(points))
	seasonPoints := make(map[int][]animeEpisodePoint, len(seasons))
	for _, season := range seasons {
		if season.Number <= 0 || len(season.Episodes) == 0 {
			continue
		}
		if len(season.Episodes) > animeInferenceMaxItems {
			out.Reason = "TMDB 季集范围过大，暂不自动推导"
			return out
		}
		tmdbPoints, valid := animeInferenceTMDB(season.Episodes)
		if !valid || seenSeason[season.Number] {
			out.Status, out.Reason = "conflict", "TMDB 季集编号或身份存在重复，无法推导"
			return out
		}
		seenSeason[season.Number] = true
		for _, episode := range season.Episodes {
			if seenEpisodeIDs[episode.ID] {
				out.Status, out.Reason = "conflict", "TMDB 季集身份重复，无法推导"
				return out
			}
			seenEpisodeIDs[episode.ID] = true
		}
		seasonPoints[season.Number] = tmdbPoints
		candidates = append(candidates, animeInferSeason(points, season.Number, tmdbPoints)...)
	}
	if len(candidates) == 0 {
		out.Reason = "日期或具体集标题不足以唯一确定 Bangumi 与 TMDB 的集数偏移"
		return out
	}
	// 不把一个跨季条目的部分章节当作单季映射；多季都出现独立证据时留给人工处理。
	evidenceSeason := candidates[0].season
	for _, candidate := range candidates[1:] {
		if candidate.season != evidenceSeason {
			out.Reason = "Bangumi 章节在多个 TMDB 季中都有对应证据，请核对跨季或编排差异"
			return out
		}
	}
	validCandidates := make([]animeEpisodeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.conflict && animeHasCrossSeasonEvidence(candidate, points, seasonPoints) {
			out.Reason = "Bangumi 章节在其他 TMDB 季也有对应证据，请核对跨季或编排差异"
			return out
		}
		if !candidate.conflict {
			validCandidates = append(validCandidates, candidate)
		}
	}
	if len(validCandidates) == 0 {
		out.Status, out.Reason = "conflict", "Bangumi 与 TMDB 集数证据互相矛盾"
		return out
	}
	if len(validCandidates) != 1 {
		out.Reason = "多个季集方案都符合 Bangumi 资料，请手动选择"
		return out
	}
	candidate := validCandidates[0]
	out.Season = candidate.season
	out.Offset = candidate.offset
	out.Matched = len(candidate.anchors)
	out.Compared = candidate.compared
	out.Pairs = candidate.anchors
	out.TargetFirst = first + candidate.offset
	out.TargetLast = last + candidate.offset
	if out.TargetFirst < 1 || out.TargetLast < out.TargetFirst {
		out = AnimeEpisodeSuggestion{Status: "conflict", Reason: "推导出的 TMDB 集号超出有效范围", Pairs: []AnimeEpisodeSuggestionPair{}}
		return out
	}
	if candidate.missing > 0 {
		out.Status = "partial"
		out.Reason = "偏移有多个对应点支持；TMDB 当前季集资料未覆盖 Bangumi 全部范围"
		return out
	}
	out.Status = "verified"
	out.Reason = "多个独立日期或集标题对应一致，可作为映射建议"
	return out
}

// 任何章节若在其他已读取的季中存在独立日期或具体标题证据，就不能声称
// 当前单季方案唯一；跨季同日放送也留给人工确认。
func animeHasCrossSeasonEvidence(candidate animeEpisodeCandidate, chapters []animeEpisodePoint, seasons map[int][]animeEpisodePoint) bool {
	chapterTitles := animeTitleNumberIndex(chapters)
	for seasonNumber, episodes := range seasons {
		if seasonNumber == candidate.season {
			continue
		}
		for i := range episodes {
			episodes[i].dateUnique = !animeDateHasAmbiguousMatch(episodes[i], episodes)
		}
		titles := animeTitleNumberIndex(episodes)
		for _, chapter := range chapters {
			for _, episode := range episodes {
				dateMatch := chapter.dateOK && episode.dateOK &&
					animeDateDistance(chapter.date, episode.date) <= 24*time.Hour &&
					!animeDateHasAmbiguousMatch(chapter, chapters) &&
					episode.dateUnique
				titleMatch := false
				for _, title := range chapter.titles {
					matches := titles[title]
					if len(chapterTitles[title]) == 1 && len(matches) == 1 && matches[0] == episode.number {
						titleMatch = true
						break
					}
				}
				if dateMatch || titleMatch {
					return true
				}
			}
		}
	}
	return false
}

func animeInferenceChapters(chapters []bangumi.Episode) ([]animeEpisodePoint, int, int, bool) {
	points := make([]animeEpisodePoint, 0, len(chapters))
	seen := make(map[int]bool, len(chapters))
	first, last := math.MaxInt, 0
	for _, chapter := range chapters {
		if math.IsNaN(chapter.Number) || math.IsInf(chapter.Number, 0) || chapter.Number < 1 || chapter.Number > 99999 || math.Trunc(chapter.Number) != chapter.Number {
			return nil, 0, 0, false
		}
		number := int(chapter.Number)
		if seen[number] {
			return nil, 0, 0, false
		}
		seen[number] = true
		point := animeEpisodePoint{number: number, titles: animeInferenceTitles(chapter.Title, chapter.OriginalTitle)}
		if date, err := time.Parse("2006-01-02", chapter.Date); err == nil {
			point.date, point.dateOK = date, true
		}
		points = append(points, point)
		if number < first {
			first = number
		}
		if number > last {
			last = number
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].number < points[j].number })
	if len(points) == 0 {
		return points, 0, 0, true
	}
	return points, first, last, true
}

func animeInferenceTMDB(episodes []model.AnimeNamingEpisode) ([]animeEpisodePoint, bool) {
	points := make([]animeEpisodePoint, 0, len(episodes))
	seenNumbers := make(map[int]bool, len(episodes))
	seenIDs := make(map[string]bool, len(episodes))
	for _, episode := range episodes {
		if episode.Number < 1 || episode.ID == "" || seenNumbers[episode.Number] || seenIDs[episode.ID] {
			return nil, false
		}
		seenNumbers[episode.Number], seenIDs[episode.ID] = true, true
		point := animeEpisodePoint{number: episode.Number, titles: animeInferenceTitles(episode.Name)}
		if date, err := time.Parse("2006-01-02", episode.Date); err == nil {
			point.date, point.dateOK = date, true
		}
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].number < points[j].number })
	return points, true
}

func animeInferSeason(chapters []animeEpisodePoint, season int, episodes []animeEpisodePoint) []animeEpisodeCandidate {
	byOffset := make(map[int]animeEpisodeCandidate, 4)
	for i := range chapters {
		chapters[i].dateUnique = !animeDateHasAmbiguousMatch(chapters[i], chapters)
	}
	for i := range episodes {
		episodes[i].dateUnique = !animeDateHasAmbiguousMatch(episodes[i], episodes)
	}
	// 标题唯一性先建立索引，候选偏移的核验不重复扫描全量标题。
	chapterTitles := animeTitleNumberIndex(chapters)
	episodeTitles := animeTitleNumberIndex(episodes)
	for i := range chapters {
		chapters[i].titleMatches = make(map[int]bool, len(chapters[i].titles))
		for _, title := range chapters[i].titles {
			if len(chapterTitles[title]) == 1 && len(episodeTitles[title]) == 1 {
				chapters[i].titleMatches[episodeTitles[title][0]] = true
			}
		}
	}
	// 日期锚点要求一对一；重复播出日期不能唯一识别某一集。
	for _, chapter := range chapters {
		if !chapter.dateOK {
			continue
		}
		for _, episode := range episodes {
			if !episode.dateOK || animeDateDistance(chapter.date, episode.date) > 24*time.Hour {
				continue
			}
			if !chapter.dateUnique || !episode.dateUnique {
				continue
			}
			offset := episode.number - chapter.number
			candidate := byOffset[offset]
			candidate.season, candidate.offset = season, offset
			candidate.anchors = append(candidate.anchors, AnimeEpisodeSuggestionPair{BangumiNumber: chapter.number, TMDBEpisode: episode.number, Date: chapter.date.Format("2006-01-02"), Evidence: "date"})
			byOffset[offset] = candidate
		}
	}
	// 日期不够时，具体且唯一的标题可提供独立锚点。
	for _, chapter := range chapters {
		for _, episode := range episodes {
			if !animeTitlesEqual(chapter.titles, episode.titles) {
				continue
			}
			if !chapter.titleMatches[episode.number] {
				continue
			}
			offset := episode.number - chapter.number
			candidate := byOffset[offset]
			candidate.season, candidate.offset = season, offset
			candidate.anchors = append(candidate.anchors, AnimeEpisodeSuggestionPair{BangumiNumber: chapter.number, TMDBEpisode: episode.number, Evidence: "title"})
			byOffset[offset] = candidate
		}
	}

	results := make([]animeEpisodeCandidate, 0, len(byOffset))
	for _, candidate := range byOffset {
		candidate.anchors = animeUniqueAnchors(candidate.anchors)
		if !animeHasIndependentEvidence(candidate.anchors) {
			continue
		}
		candidate.conflict, candidate.compared, candidate.missing = animeCheckCandidate(candidate, chapters, episodes)
		results = append(results, candidate)
	}
	return results
}

func animeCheckCandidate(candidate animeEpisodeCandidate, chapters, episodes []animeEpisodePoint) (bool, int, int) {
	targetByNumber := make(map[int]animeEpisodePoint, len(episodes))
	for _, episode := range episodes {
		targetByNumber[episode.number] = episode
	}
	compared, missing := 0, 0
	for _, chapter := range chapters {
		targetNumber := chapter.number + candidate.offset
		if targetNumber < 1 {
			return true, compared, missing
		}
		// 已知章节若明确对应另一集，即使当前集尚未收录，也不能外推偏移。
		for _, other := range episodes {
			if other.number == targetNumber {
				continue
			}
			dateMatch := chapter.dateOK && other.dateOK && animeDateDistance(chapter.date, other.date) <= 24*time.Hour && chapter.dateUnique && other.dateUnique
			titleMatch := chapter.titleMatches[other.number]
			if dateMatch || titleMatch {
				return true, compared, missing
			}
		}
		target, exists := targetByNumber[targetNumber]
		if !exists {
			missing++
			continue
		}
		comparedThis := false
		if chapter.dateOK && target.dateOK {
			comparedThis = true
			if animeDateDistance(chapter.date, target.date) > 24*time.Hour {
				return true, compared, missing
			}
		}
		if animeTitlesEqual(chapter.titles, target.titles) {
			comparedThis = true
		}
		if comparedThis {
			compared++
		}
	}
	// 同一日期或标题不能重复充当两个证据点，锚点按双方集号去重。
	if len(candidate.anchors) < 2 {
		return true, compared, missing
	}
	return false, compared, missing
}

func animeUniqueAnchors(anchors []AnimeEpisodeSuggestionPair) []AnimeEpisodeSuggestionPair {
	seen := make(map[[2]int]int, len(anchors))
	out := make([]AnimeEpisodeSuggestionPair, 0, len(anchors))
	for _, anchor := range anchors {
		key := [2]int{anchor.BangumiNumber, anchor.TMDBEpisode}
		if index, exists := seen[key]; exists {
			if !strings.Contains(out[index].Evidence, anchor.Evidence) {
				out[index].Evidence += "," + anchor.Evidence
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, anchor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BangumiNumber < out[j].BangumiNumber })
	return out
}

func animeHasIndependentEvidence(anchors []AnimeEpisodeSuggestionPair) bool {
	dates, titles := 0, 0
	for _, anchor := range anchors {
		if strings.Contains(anchor.Evidence, "date") {
			dates++
		}
		if strings.Contains(anchor.Evidence, "title") {
			titles++
		}
	}
	return dates >= 2 || titles >= 2
}

func animeDateHasAmbiguousMatch(point animeEpisodePoint, points []animeEpisodePoint) bool {
	matches := 0
	for _, other := range points {
		if other.number != point.number && other.dateOK && animeDateDistance(point.date, other.date) <= 24*time.Hour {
			matches++
		}
	}
	return matches > 0
}

func animeDateDistance(a, b time.Time) time.Duration {
	distance := a.Sub(b)
	if distance < 0 {
		return -distance
	}
	return distance
}

func animeTitleNumberIndex(points []animeEpisodePoint) map[string][]int {
	index := make(map[string][]int, len(points))
	for _, point := range points {
		for _, title := range point.titles {
			index[title] = append(index[title], point.number)
		}
	}
	return index
}

func animeInferenceTitles(titles ...string) []string {
	result := make([]string, 0, len(titles))
	seen := make(map[string]bool, len(titles))
	for _, title := range titles {
		key := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, strings.TrimSpace(title))
		if key == "" || animeGenericEpisodeTitle(key) || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, key)
	}
	return result
}

func animeGenericEpisodeTitle(title string) bool {
	withoutNumbers := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) || strings.ContainsRune("零一二三四五六七八九十", r) {
			return -1
		}
		return r
	}, title)
	for _, generic := range []string{"", "episode", "ep", "chapter", "第集", "第話", "第话", "第回", "話", "话", "集", "未命名", "未定", "unknown", "untitled", "tba", "总集篇", "総集編", "recap", "special", "ova", "oad"} {
		if withoutNumbers == generic {
			return true
		}
	}
	return false
}

func animeTitlesEqual(a, b []string) bool {
	for _, left := range a {
		for _, right := range b {
			if left == right {
				return true
			}
		}
	}
	return false
}
