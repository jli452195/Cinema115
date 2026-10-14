package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

var ErrResourceMatchUnavailable = errors.New("缺集信息暂时不可用")

type ResourceMatchMetadata interface {
	Details(context.Context, string, int) (*tmdb.Detail, error)
	Schedule(context.Context, int, int) (*tmdb.Schedule, error)
}

type ResourceMatchLibrary interface {
	Matches(context.Context, []model.EmbyMatch, bool) ([]model.EmbyMatch, error)
}

type EpisodeRef struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Label   string `json:"label"`
}

type MediaIdentity struct {
	Numbering *tmdb.NumberingInfo `json:"numbering,omitempty"`
	Kind      string              `json:"kind"`
	TMDBID    int                 `json:"tmdbId"`
	Title     string              `json:"title"`
	Year      string              `json:"year"`
}

type ResourceCandidate struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	TMDBID string `json:"tmdbId,omitempty"`
}

type EpisodeSpan struct {
	StartSeason  int  `json:"startSeason"`
	StartEpisode int  `json:"startEpisode,omitempty"`
	EndSeason    int  `json:"endSeason"`
	EndEpisode   int  `json:"endEpisode,omitempty"`
	WholeSeason  bool `json:"wholeSeason"`
}

type ParsedResource struct {
	Kind     string        `json:"kind,omitempty"`
	Title    string        `json:"title"`
	Year     string        `json:"year,omitempty"`
	TMDBID   int           `json:"tmdbId,omitempty"`
	Coverage string        `json:"coverage"`
	Spans    []EpisodeSpan `json:"spans,omitempty"`
}

type ResourceMatchDecision struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	Matched        bool           `json:"matched"`
	Identity       string         `json:"identity,omitempty"`
	Coverage       string         `json:"coverage,omitempty"`
	MatchedMissing []EpisodeRef   `json:"matchedMissing"`
	Parsed         ParsedResource `json:"parsed"`
	Reason         string         `json:"reason"`
}

type ResourceMatchBatch struct {
	Status  string                  `json:"status"`
	Target  MediaIdentity           `json:"target"`
	Missing []EpisodeRef            `json:"missing"`
	Results []ResourceMatchDecision `json:"results"`
}

type ResourceMatcher interface {
	Match(context.Context, string, int, []ResourceCandidate) (ResourceMatchBatch, error)
}

type ResourceMatchService struct {
	Metadata ResourceMatchMetadata
	Library  ResourceMatchLibrary
	now      func() time.Time
}

func NewResourceMatchService(metadata ResourceMatchMetadata, library ResourceMatchLibrary) *ResourceMatchService {
	return &ResourceMatchService{Metadata: metadata, Library: library, now: time.Now}
}

var resourceYearPattern = regexp.MustCompile(`[（(]((?:19|20)\d{2})[）)]`)
var resourceTMDBPattern = regexp.MustCompile(`(?i)\{tmdb\s*[-=:：]\s*(\d+)\}`)
var resourceCoverageStartPattern = regexp.MustCompile(`(?i)(?:^|\s)S\d{1,4}`)
var seasonRangePattern = regexp.MustCompile(`(?i)S(\d{1,4})\s*-\s*S(\d{1,4})`)
var episodeRangePattern = regexp.MustCompile(`(?i)S(\d{1,4})E(\d{1,4})\s*-\s*(?:S(\d{1,4}))?E(\d{1,4})`)
var singleEpisodePattern = regexp.MustCompile(`(?i)S(\d{1,4})E(\d{1,4})`)
var singleSeasonPattern = regexp.MustCompile(`(?i)(?:^|[^A-Z0-9])S(\d{1,4})(?:$|[^A-Z0-9])`)
var chineseSeasonRangePattern = regexp.MustCompile(`第\s*(\d{1,4})\s*[-至到]\s*(\d{1,4})\s*季`)
var chineseSeasonPattern = regexp.MustCompile(`第\s*(\d{1,4})\s*季`)

func resourceTitleTags(value string) (string, []string) {
	value = strings.TrimSpace(value)
	tags := make([]string, 0, 2)
	for value != "" {
		opening, closing := "", ""
		switch {
		case strings.HasPrefix(value, "["):
			opening, closing = "[", "]"
		case strings.HasPrefix(value, "【"):
			opening, closing = "【", "】"
		default:
			return value, tags
		}
		index := strings.Index(value, closing)
		if index < 0 {
			return value, tags
		}
		tags = append(tags, strings.TrimSpace(value[len(opening):index]))
		value = strings.TrimSpace(value[index+len(closing):])
	}
	return value, tags
}

func normalizeResourceTitle(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
}

func resourceKind(tags []string, raw string) string {
	labels := strings.ToLower(strings.Join(tags, " "))
	for _, marker := range []string{"动画电影", "动漫电影", "韩电影", "日韩电影", "外语电影", "电影"} {
		if strings.Contains(labels, marker) {
			return "movie"
		}
	}
	for _, marker := range []string{"日番", "日剧", "欧美剧", "日韩剧", "韩剧", "国产剧", "电视剧", "剧集", "动漫", "动画"} {
		if strings.Contains(labels, marker) {
			return "tv"
		}
	}
	if resourceCoverageStartPattern.MatchString(raw) {
		return "tv"
	}
	return ""
}

func atoi(value string) int {
	n, _ := strconv.Atoi(value)
	return n
}

// ParseResourceTitle 只解析标题声明，不读取 TMDB、Emby 或分享文件内容。
func ParseResourceTitle(candidate ResourceCandidate) ParsedResource {
	raw, tags := resourceTitleTags(candidate.Title)
	parsed := ParsedResource{Kind: resourceKind(tags, raw), Coverage: "all", Spans: []EpisodeSpan{}}
	if match := resourceYearPattern.FindStringSubmatch(raw); len(match) > 1 {
		parsed.Year = match[1]
	}
	if match := resourceTMDBPattern.FindStringSubmatch(raw); len(match) > 1 {
		parsed.TMDBID = atoi(match[1])
	}
	if parsed.TMDBID == 0 {
		parsed.TMDBID = atoi(candidate.TMDBID)
	}
	end := len(raw)
	for _, location := range [][]int{resourceYearPattern.FindStringIndex(raw), resourceTMDBPattern.FindStringIndex(raw), resourceCoverageStartPattern.FindStringIndex(raw)} {
		if len(location) == 2 && location[0] < end {
			end = location[0]
		}
	}
	parsed.Title = strings.TrimSpace(raw[:end])
	for _, match := range seasonRangePattern.FindAllStringSubmatch(raw, -1) {
		start, finish := atoi(match[1]), atoi(match[2])
		if finish >= start {
			parsed.Spans = append(parsed.Spans, EpisodeSpan{StartSeason: start, EndSeason: finish, WholeSeason: true})
		}
	}
	for _, match := range episodeRangePattern.FindAllStringSubmatch(raw, -1) {
		startSeason, endSeason := atoi(match[1]), atoi(match[3])
		if endSeason == 0 && match[3] == "" {
			endSeason = startSeason
		}
		span := EpisodeSpan{StartSeason: startSeason, StartEpisode: atoi(match[2]), EndSeason: endSeason, EndEpisode: atoi(match[4])}
		if episodePosition(span.StartSeason, span.StartEpisode) <= episodePosition(span.EndSeason, span.EndEpisode) {
			parsed.Spans = append(parsed.Spans, span)
		}
	}
	for _, match := range singleEpisodePattern.FindAllStringSubmatch(raw, -1) {
		season, episode := atoi(match[1]), atoi(match[2])
		parsed.Spans = append(parsed.Spans, EpisodeSpan{StartSeason: season, StartEpisode: episode, EndSeason: season, EndEpisode: episode})
	}
	for _, match := range singleSeasonPattern.FindAllStringSubmatch(raw, -1) {
		season := atoi(match[1])
		parsed.Spans = append(parsed.Spans, EpisodeSpan{StartSeason: season, EndSeason: season, WholeSeason: true})
	}
	for _, match := range chineseSeasonRangePattern.FindAllStringSubmatch(raw, -1) {
		start, finish := atoi(match[1]), atoi(match[2])
		if finish >= start {
			parsed.Spans = append(parsed.Spans, EpisodeSpan{StartSeason: start, EndSeason: finish, WholeSeason: true})
		}
	}
	for _, match := range chineseSeasonPattern.FindAllStringSubmatch(raw, -1) {
		season := atoi(match[1])
		parsed.Spans = append(parsed.Spans, EpisodeSpan{StartSeason: season, EndSeason: season, WholeSeason: true})
	}
	parsed.Spans = normalizeEpisodeSpans(parsed.Spans)
	if len(parsed.Spans) > 0 {
		parsed.Coverage = "explicit"
	}
	return parsed
}

func normalizeEpisodeSpans(spans []EpisodeSpan) []EpisodeSpan {
	result := make([]EpisodeSpan, 0, len(spans))
	for i, span := range spans {
		redundant := false
		for j, other := range spans {
			if i == j || !other.contains(span) {
				continue
			}
			if other != span || j < i {
				redundant = true
				break
			}
		}
		if !redundant {
			result = append(result, span)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.StartSeason != right.StartSeason {
			return left.StartSeason < right.StartSeason
		}
		if left.WholeSeason != right.WholeSeason {
			return left.WholeSeason
		}
		if left.StartEpisode != right.StartEpisode {
			return left.StartEpisode < right.StartEpisode
		}
		if left.EndSeason != right.EndSeason {
			return left.EndSeason < right.EndSeason
		}
		return left.EndEpisode < right.EndEpisode
	})
	return result
}

func episodePosition(season, episode int) int64 {
	return int64(season)<<32 | int64(uint32(episode))
}

func (span EpisodeSpan) covers(episode EpisodeRef) bool {
	if span.WholeSeason {
		return episode.Season >= span.StartSeason && episode.Season <= span.EndSeason
	}
	position := episodePosition(episode.Season, episode.Episode)
	return position >= episodePosition(span.StartSeason, span.StartEpisode) && position <= episodePosition(span.EndSeason, span.EndEpisode)
}

func (span EpisodeSpan) contains(other EpisodeSpan) bool {
	if span.WholeSeason {
		return other.StartSeason >= span.StartSeason && other.EndSeason <= span.EndSeason
	}
	if other.WholeSeason {
		return false
	}
	return episodePosition(other.StartSeason, other.StartEpisode) >= episodePosition(span.StartSeason, span.StartEpisode) &&
		episodePosition(other.EndSeason, other.EndEpisode) <= episodePosition(span.EndSeason, span.EndEpisode)
}

func newDecision(candidate ResourceCandidate, parsed ParsedResource, status, reason string) ResourceMatchDecision {
	return ResourceMatchDecision{ID: candidate.ID, Status: status, Coverage: parsed.Coverage, Parsed: parsed, MatchedMissing: []EpisodeRef{}, Reason: reason}
}

// MatchResource 使用已经固定的缺集快照判断单个候选，不执行网络或数据库 I/O。
func MatchResource(target MediaIdentity, missing []EpisodeRef, candidate ResourceCandidate) ResourceMatchDecision {
	parsed := ParseResourceTitle(candidate)
	decision := newDecision(candidate, parsed, "not_matched", "资源与当前影视不匹配")
	if parsed.Kind != "" && parsed.Kind != target.Kind {
		decision.Reason = "资源类型与当前影视冲突"
		return decision
	}
	switch {
	case parsed.TMDBID > 0:
		if parsed.TMDBID != target.TMDBID {
			decision.Reason = "TMDB ID 不一致"
			return decision
		}
		decision.Identity = "tmdb"
	case normalizeResourceTitle(parsed.Title) != "" && normalizeResourceTitle(parsed.Title) == normalizeResourceTitle(target.Title) && parsed.Year != "" && parsed.Year == target.Year:
		decision.Identity = "title_year"
	default:
		decision.Reason = "需要匹配的 TMDB ID，或相同中文名与年份"
		return decision
	}
	if target.Kind == "movie" {
		decision.Status = "matched"
		decision.Matched = true
		decision.Reason = "资源与当前电影匹配"
		return decision
	}
	if len(missing) == 0 {
		decision.Reason = "当前没有缺集"
		return decision
	}
	if parsed.Coverage == "all" {
		decision.MatchedMissing = append(decision.MatchedMissing, missing...)
	} else {
		for _, episode := range missing {
			for _, span := range parsed.Spans {
				if span.covers(episode) {
					decision.MatchedMissing = append(decision.MatchedMissing, episode)
					break
				}
			}
		}
	}
	if len(decision.MatchedMissing) == 0 {
		decision.Reason = "资源声明的季集范围未覆盖当前缺集"
		return decision
	}
	decision.Status = "matched"
	decision.Matched = true
	if parsed.Coverage == "all" {
		decision.Reason = "资源未声明季集范围，按整部资源处理"
	} else {
		decision.Reason = "资源声明的季集范围命中当前缺集"
	}
	return decision
}

func episodeLabel(season, episode int) string {
	return fmt.Sprintf("S%02dE%02d", season, episode)
}

func (s *ResourceMatchService) mediaSnapshot(ctx context.Context, kind string, id int) (MediaIdentity, []EpisodeRef, bool, error) {
	if s == nil || s.Metadata == nil || s.Library == nil || (kind != "movie" && kind != "tv") || id < 1 {
		return MediaIdentity{}, nil, false, ErrResourceMatchUnavailable
	}
	ctx = context.WithValue(ctx, tmdbPrewarmKey{}, true)
	if kind == "tv" {
		var err error
		ctx, err = pinNumbering(ctx, s.Metadata, id)
		if err != nil {
			return MediaIdentity{}, nil, false, err
		}
	}
	detail, err := s.Metadata.Details(ctx, kind, id)
	if err != nil || detail == nil || detail.ID != id || detail.Kind != kind || detail.Title == "" || detail.CacheStale {
		return MediaIdentity{}, nil, false, fmt.Errorf("%w: TMDB 影视详情未就绪", ErrResourceMatchUnavailable)
	}
	target := MediaIdentity{Kind: kind, TMDBID: id, Title: detail.Title}
	target.Numbering = detail.Numbering
	if len(detail.Date) >= 4 {
		target.Year = detail.Date[:4]
	}
	matches, err := s.Library.Matches(ctx, []model.EmbyMatch{{Kind: kind, TMDB: strconv.Itoa(id)}}, true)
	if err != nil || len(matches) != 1 || (matches[0].State != "present" && matches[0].State != "absent") {
		return target, nil, false, fmt.Errorf("%w: Emby 入库索引未就绪", ErrResourceMatchUnavailable)
	}
	if kind == "movie" {
		return target, []EpisodeRef{}, matches[0].State == "absent", nil
	}
	owned := make(map[[2]int]bool, len(matches[0].Items))
	for _, item := range matches[0].Items {
		if item.Type != "Episode" || item.Season <= 0 || item.Episode <= 0 {
			continue
		}
		end := item.EndEpisode
		if end < item.Episode || end-item.Episode > 100 {
			end = item.Episode
		}
		for episode := item.Episode; episode <= end; episode++ {
			owned[[2]int{item.Season, episode}] = true
		}
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	today := now().UTC().Format("2006-01-02")
	missing := make([]EpisodeRef, 0)
	seen := make(map[[2]int]bool)
	regularSeasons := 0
	for _, season := range detail.Seasons {
		if season.Number <= 0 {
			continue
		}
		regularSeasons++
		if date, parseErr := time.Parse("2006-01-02", season.Date); parseErr == nil && date.Format("2006-01-02") > today {
			continue
		}
		if season.EpisodeCount < 1 {
			return target, nil, false, fmt.Errorf("%w: 第 %d 季分集数量无效", ErrResourceMatchUnavailable, season.Number)
		}
		schedule, err := s.Metadata.Schedule(ctx, id, season.Number)
		if err != nil || schedule == nil || schedule.Number != season.Number || schedule.CacheStale || len(schedule.Episodes) != season.EpisodeCount {
			return target, nil, false, fmt.Errorf("%w: 第 %d 季播出信息未就绪", ErrResourceMatchUnavailable, season.Number)
		}
		for _, episode := range schedule.Episodes {
			key := [2]int{season.Number, episode.Number}
			if episode.Number <= 0 || seen[key] {
				return target, nil, false, fmt.Errorf("%w: 第 %d 季分集编号无效", ErrResourceMatchUnavailable, season.Number)
			}
			seen[key] = true
			if !validAiredDate(episode.Date, today) || owned[key] {
				continue
			}
			missing = append(missing, EpisodeRef{Season: season.Number, Episode: episode.Number, Label: episodeLabel(season.Number, episode.Number)})
		}
	}
	if regularSeasons == 0 {
		return target, nil, false, fmt.Errorf("%w: TMDB 正片季信息未就绪", ErrResourceMatchUnavailable)
	}
	sort.Slice(missing, func(i, j int) bool {
		return episodePosition(missing[i].Season, missing[i].Episode) < episodePosition(missing[j].Season, missing[j].Episode)
	})
	return target, missing, len(missing) > 0, nil
}

func validAiredDate(value, today string) bool {
	if len(value) != len("2006-01-02") || value > today {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func (s *ResourceMatchService) Match(ctx context.Context, kind string, id int, candidates []ResourceCandidate) (ResourceMatchBatch, error) {
	batch := ResourceMatchBatch{Status: "unavailable", Missing: []EpisodeRef{}, Results: make([]ResourceMatchDecision, 0, len(candidates))}
	target, missing, needed, err := s.mediaSnapshot(ctx, kind, id)
	batch.Target = target
	if err != nil {
		for _, candidate := range candidates {
			parsed := ParseResourceTitle(candidate)
			batch.Results = append(batch.Results, newDecision(candidate, parsed, "unavailable", "缺集信息暂时不可用"))
		}
		return batch, err
	}
	batch.Status = "available"
	batch.Missing = missing
	for _, candidate := range candidates {
		if !needed {
			parsed := ParseResourceTitle(candidate)
			reason := "当前没有缺集"
			if kind == "movie" {
				reason = "当前电影已入库"
			}
			batch.Results = append(batch.Results, newDecision(candidate, parsed, "not_matched", reason))
			continue
		}
		batch.Results = append(batch.Results, MatchResource(target, missing, candidate))
	}
	return batch, nil
}
