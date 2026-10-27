package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"strm115/backend/internal/naming"
	"strm115/backend/internal/storage"
)

var ErrAnimeNamingChanged = errors.New("anime naming metadata changed")

type AnimeNamingMetadata interface {
	Browse(context.Context, tmdb.Options) (*tmdb.Page, error)
	AnimeDetails(context.Context, int) (*tmdb.Detail, error)
	AnimeSeason(context.Context, int, int) (*tmdb.SeasonDetail, error)
	AnimeGroups(context.Context, int) ([]tmdb.EpisodeGroupSummary, error)
	AnimeGroup(context.Context, int, string) (*tmdb.EpisodeGroup, error)
}
type AnimeNamingService struct {
	Metadata  AnimeNamingMetadata
	Templates NamingTemplates
}

// These readers share the existing metadata cache, bypassing global numbering
// selection. They never call SaveNumbering or publish other-business events.
func (s *TMDBService) AnimeDetails(ctx context.Context, id int) (*tmdb.Detail, error) {
	if id < 1 {
		return nil, tmdb.ErrInput
	}
	v, info, err := cachedTMDB[tmdb.Detail](ctx, s, tmdbDetailCachePrefix+"tv:"+strconv.Itoa(id), 6*time.Hour, func(ctx context.Context) (any, error) { return s.source.Details(ctx, "tv", id) })
	if err == nil {
		v.CacheInfo = info
	}
	return v, err
}
func (s *TMDBService) AnimeSeason(ctx context.Context, id, n int) (*tmdb.SeasonDetail, error) {
	if id < 1 || n < 0 || n > 1000 {
		return nil, tmdb.ErrInput
	}
	v, info, err := cachedTMDB[tmdb.SeasonDetail](ctx, s, "season:"+strconv.Itoa(id)+":"+strconv.Itoa(n), time.Hour, func(ctx context.Context) (any, error) { return s.source.Season(ctx, id, n) })
	if err == nil {
		v.CacheInfo = info
	}
	return v, err
}
func (s *TMDBService) AnimeGroups(ctx context.Context, id int) ([]tmdb.EpisodeGroupSummary, error) {
	r, ok := s.source.(episodeGroupReader)
	if !ok {
		return nil, tmdb.ErrNotConfigured
	}
	v, _, err := cachedTMDB[[]tmdb.EpisodeGroupSummary](ctx, s, "anime:groups:"+strconv.Itoa(id), 6*time.Hour, func(ctx context.Context) (any, error) { return r.EpisodeGroups(ctx, id) })
	if err != nil {
		return nil, err
	}
	return *v, nil
}
func (s *TMDBService) AnimeGroup(ctx context.Context, id int, group string) (*tmdb.EpisodeGroup, error) {
	if !tmdb.ValidEpisodeGroupID(group) {
		return nil, tmdb.ErrInput
	}
	groups, err := s.AnimeGroups(ctx, id)
	if err != nil {
		return nil, err
	}
	member := false
	for _, g := range groups {
		if g.ID == group {
			member = true
			break
		}
	}
	if !member {
		return nil, tmdb.ErrNotFound
	}
	r, ok := s.source.(episodeGroupReader)
	if !ok {
		return nil, tmdb.ErrNotConfigured
	}
	v, _, err := cachedTMDB[tmdb.EpisodeGroup](ctx, s, "anime:group:"+strconv.Itoa(id)+":"+group, 6*time.Hour, func(ctx context.Context) (any, error) { return r.EpisodeGroup(ctx, id, group) })
	return v, err
}

var animeSeasonMarker = regexp.MustCompile(`(?i)(?:第\s*([一二三四五六七八九十百0-9]+)\s*[季期]|\b([0-9]+)(?:st|nd|rd|th)\s+season\b|\bseason\s+([0-9]+)\b|\bs([0-9]{1,2})\b)`)

func animeSearchName(s string) string {
	s = strings.TrimSpace(s)
	if loc := animeSeasonMarker.FindStringIndex(s); loc != nil && loc[0] > 0 {
		// A chapter suffix after the season (e.g. 第三季 反击篇) is not part
		// of TMDB's series title. Keep the original series prefix for search.
		return strings.TrimSpace(s[:loc[0]])
	}
	return s
}

func animeSeasonHint(names []string) int {
	hint := 0
	for _, name := range names {
		match := animeSeasonMarker.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		raw := ""
		for _, value := range match[1:] {
			if value != "" {
				raw = value
				break
			}
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			n, err = animeChineseSeason(raw)
		}
		if err != nil || n < 1 || n > 1000 {
			continue
		}
		if hint != 0 && hint != n {
			return 0
		}
		hint = n
	}
	return hint
}

func animeChineseSeason(raw string) (int, error) {
	values := map[rune]int{'一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	total, digit := 0, 0
	for _, r := range raw {
		if n, ok := values[r]; ok {
			digit = n
			continue
		}
		unit := 0
		switch r {
		case '十':
			unit = 10
		case '百':
			unit = 100
		default:
			return 0, tmdb.ErrInput
		}
		if digit == 0 {
			digit = 1
		}
		total += digit * unit
		digit = 0
	}
	return total + digit, nil
}
func animeNameKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, animeSearchName(s))
}

type AnimeNamingSearch struct {
	Items         []tmdb.Item `json:"items"`
	RecommendedID string      `json:"recommendedId"`
}

func (s *AnimeNamingService) Search(ctx context.Context, names ...string) (AnimeNamingSearch, error) {
	out := AnimeNamingSearch{Items: []tmdb.Item{}}
	if s == nil || s.Metadata == nil {
		return out, tmdb.ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	seen := map[int]bool{}
	queries := map[string]bool{}
	exact := map[int]bool{}
	for _, name := range names {
		query := animeSearchName(name)
		if query == "" || queries[query] {
			continue
		}
		queries[query] = true
		if len(queries) > 2 {
			break
		}
		page, err := s.Metadata.Browse(ctx, tmdb.Options{Kind: "tv", Query: query, Page: 1})
		if err != nil {
			return out, err
		}
		if page == nil {
			return out, tmdb.ErrInput
		}
		for _, item := range page.Items {
			if item.ID < 1 || item.Kind != "tv" {
				continue
			}
			if animeNameKey(query) == animeNameKey(item.Title) || animeNameKey(query) == animeNameKey(item.OriginalTitle) {
				exact[item.ID] = true
			}
			if !seen[item.ID] && len(out.Items) < 12 {
				out.Items = append(out.Items, item)
				seen[item.ID] = true
			}
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return exact[out.Items[i].ID] && !exact[out.Items[j].ID] })
	if len(exact) == 1 && len(out.Items) > 0 && exact[out.Items[0].ID] {
		out.RecommendedID = strconv.Itoa(out.Items[0].ID)
	}
	if len(out.Items) == 1 {
		out.RecommendedID = strconv.Itoa(out.Items[0].ID)
	}
	return out, nil
}

type AnimeNamingTargetInput struct {
	TMDBID    string `json:"tmdbId"`
	GroupID   string `json:"groupId"`
	Season    *int   `json:"season"`
	BangumiID string `json:"bangumiId"`
}

func (s *AnimeNamingService) Target(ctx context.Context, in AnimeNamingTargetInput, date string, names ...string) (model.AnimeNamingTarget, error) {
	out := model.AnimeNamingTarget{}
	id, err := strconv.Atoi(in.TMDBID)
	if !model.ValidAnimeExternalID(in.TMDBID) || err != nil || id < 1 || in.Season != nil && (*in.Season < 0 || *in.Season > 1000) || in.GroupID != "" && !tmdb.ValidEpisodeGroupID(in.GroupID) {
		return out, storage.ErrAnimeInvalid
	}
	if s == nil || s.Metadata == nil {
		return out, tmdb.ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	detail, err := s.Metadata.AnimeDetails(ctx, id)
	if err != nil {
		return out, err
	}
	if detail == nil || detail.ID != id || len(detail.Date) < 4 || detail.Title == "" {
		return out, tmdb.ErrInput
	}
	out = model.AnimeNamingTarget{TMDBID: in.TMDBID, Title: detail.Title, OriginalTitle: detail.OriginalTitle, Year: detail.Date[:4], GroupID: in.GroupID, GroupName: "TMDB 默认", Seasons: []model.AnimeNamingSeason{}}
	var group *tmdb.EpisodeGroup
	if in.GroupID != "" {
		group, err = s.Metadata.AnimeGroup(ctx, id, in.GroupID)
		if err != nil {
			return out, err
		}
		if group == nil {
			return out, tmdb.ErrNotFound
		}
		out.GroupName = group.Name
		for _, s := range group.Seasons {
			out.Seasons = append(out.Seasons, model.AnimeNamingSeason{Number: s.Number, Name: s.Name, EpisodeCount: len(s.Episodes)})
		}
	} else {
		for _, s := range detail.Seasons {
			out.Seasons = append(out.Seasons, model.AnimeNamingSeason{Number: s.Number, Name: s.Name, Date: s.Date, EpisodeCount: s.EpisodeCount})
		}
	}
	if len(out.Seasons) == 0 || len(out.Seasons) > 1001 {
		return out, tmdb.ErrNotFound
	}
	out.Season = out.Seasons[0].Number
	for _, season := range out.Seasons {
		if season.Number > 0 {
			out.Season = season.Number
			break
		}
	}
	if in.Season != nil {
		out.Season = *in.Season
	} else {
		// Bangumi may split a cour several months after a TMDB season started.
		// A clear season in the title is a better fallback than season one.
		hint := animeSeasonHint(names)
		for _, season := range out.Seasons {
			if season.Number == hint && hint > 0 {
				out.Season = hint
				break
			}
		}
		best := 15 * 24 * time.Hour
		for _, season := range out.Seasons {
			a, e1 := time.Parse("2006-01-02", date)
			b, e2 := time.Parse("2006-01-02", season.Date)
			if e1 == nil && e2 == nil && season.Number > 0 {
				distance := a.Sub(b)
				if distance < 0 {
					distance = -distance
				}
				if distance < best {
					best = distance
					out.Season = season.Number
				}
			}
		}
	}
	n := out.Season
	found := false
	for i := range out.Seasons {
		if out.Seasons[i].Number != n {
			continue
		}
		found = true
		var season *tmdb.SeasonDetail
		if group != nil {
			for j := range group.Seasons {
				if group.Seasons[j].Number == n {
					season = &group.Seasons[j]
					break
				}
			}
		} else {
			season, err = s.Metadata.AnimeSeason(ctx, id, n)
			if err != nil {
				return out, err
			}
		}
		if season == nil || len(season.Episodes) > 2000 {
			return out, tmdb.ErrInput
		}
		episodes := make([]model.AnimeNamingEpisode, 0, len(season.Episodes))
		for _, e := range season.Episodes {
			episodes = append(episodes, model.AnimeNamingEpisode{ID: strconv.Itoa(e.ID), Number: e.Number, Name: e.Name, Date: e.Date, SourceSeason: e.SourceSeason, SourceNumber: e.SourceNumber})
		}
		out.Seasons[i].Episodes = episodes
	}
	if !found {
		return out, tmdb.ErrNotFound
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return out, fmt.Errorf("encode anime naming snapshot: %w", err)
	}
	digest := sha256.Sum256(payload)
	out.Fingerprint = hex.EncodeToString(digest[:])
	if err := (model.AnimeNamingPlan{SchemaVersion: 1, Target: out}).Validate(); err != nil {
		return out, fmt.Errorf("invalid TMDB naming metadata: %w", tmdb.ErrInput)
	}
	// Optional titles do not participate in the mapping fingerprint.
	if s.Templates != nil {
		template, err := s.Templates.NamingTemplate(ctx, naming.TV)
		if err != nil {
			return out, err
		}
		out.EnglishTitle, err = optionalNamingEnglish(ctx, s.Metadata, template.Template, "tv", id)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
func (s *AnimeNamingService) Resolve(ctx context.Context, in model.AnimeNamingInput, old *model.AnimeNamingPlan) (*model.AnimeNamingPlan, error) {
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", storage.ErrAnimeInvalid, err)
	}
	// New configuration uses one selected season and a per-source offset.
	// The richer persisted rules remain readable by already frozen tasks.
	for _, mapping := range in.Sources {
		if len(mapping.Segments) != 1 || len(mapping.Overrides) != 0 || mapping.Segments[0].TargetSeason != in.Season || mapping.Segments[0].Last != nil {
			return nil, storage.ErrAnimeInvalid
		}
	}
	// Reusing a previously verified snapshot keeps local edits possible when
	// TMDB has disappeared. New seasons still require new metadata verification.
	if old != nil && old.Target.TMDBID == in.TMDBID && old.Target.GroupID == in.GroupID && old.Target.Season == in.Season && old.Target.Fingerprint == in.Fingerprint {
		for _, season := range old.Target.Seasons {
			if season.Number == in.Season && season.Episodes != nil {
				return &model.AnimeNamingPlan{SchemaVersion: 1, Target: old.Target, Sources: in.Sources}, nil
			}
		}
	}
	target, err := s.Target(ctx, AnimeNamingTargetInput{TMDBID: in.TMDBID, GroupID: in.GroupID, Season: &in.Season}, "")
	if err != nil {
		return nil, err
	}
	if target.Fingerprint != in.Fingerprint {
		return nil, ErrAnimeNamingChanged
	}
	return &model.AnimeNamingPlan{SchemaVersion: 1, Target: target, Sources: in.Sources}, nil
}
