package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

const recentUpdatesPageSize = 36

var ErrRecentUpdatesInput = errors.New("最近更新查询参数无效")

type RecentUpdatesQuery struct {
	Range  string
	Date   string
	Cursor string
}

type RecentUpdateItem struct {
	Kind            string  `json:"kind"`
	TMDBID          int     `json:"tmdbId"`
	Title           string  `json:"title"`
	PosterURL       string  `json:"posterUrl"`
	ReleaseDate     string  `json:"releaseDate"`
	Rating          float64 `json:"rating"`
	Votes           int     `json:"votes"`
	Date            string  `json:"date"`
	UpdatedAt       string  `json:"updatedAt"`
	Season          int     `json:"season"`
	EpisodeLabel    string  `json:"episodeLabel,omitempty"`
	EpisodeCount    int     `json:"episodeCount,omitempty"`
	MetadataMissing bool    `json:"metadataMissing,omitempty"`
}

type RecentUpdatesPage struct {
	Items      []RecentUpdateItem `json:"items"`
	Ready      bool               `json:"ready"`
	Status     string             `json:"status"`
	HasMore    bool               `json:"hasMore"`
	NextCursor string             `json:"nextCursor,omitempty"`
	From       string             `json:"from"`
	To         string             `json:"to"`
}

type RecentUpdateSource interface {
	RecentUpdateGroups(context.Context, time.Time, time.Time, int, int) ([]model.MediaUpdateGroup, string, error)
}

type RecentUpdateMetadata interface {
	Details(context.Context, string, int) (*tmdb.Detail, error)
}

type RecentUpdatesService struct {
	Source   RecentUpdateSource
	Metadata RecentUpdateMetadata
	Now      func() time.Time
}

type recentUpdateKey struct {
	kind string
	id   int
}

func (s *RecentUpdatesService) List(ctx context.Context, query RecentUpdatesQuery) (RecentUpdatesPage, error) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	today := now.In(location)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	start, end, err := recentUpdatePeriod(today, query)
	if err != nil {
		return RecentUpdatesPage{}, err
	}
	offset := 0
	if query.Cursor != "" {
		offset, err = strconv.Atoi(query.Cursor)
		if err != nil || offset < 0 || offset > 1_000_000 || strconv.Itoa(offset) != query.Cursor {
			return RecentUpdatesPage{}, ErrRecentUpdatesInput
		}
	}
	page := RecentUpdatesPage{
		Items: make([]RecentUpdateItem, 0, recentUpdatesPageSize),
		From:  start.In(location).Format("2006-01-02"),
		To:    end.Add(-time.Nanosecond).In(location).Format("2006-01-02"),
	}
	groups, status, err := s.Source.RecentUpdateGroups(ctx, start.UTC(), end.UTC(), recentUpdatesPageSize+1, offset)
	if err != nil {
		return RecentUpdatesPage{}, err
	}
	page.Status = status
	page.Ready = status == "ready"
	if len(groups) > recentUpdatesPageSize {
		page.HasMore = true
		groups = groups[:recentUpdatesPageSize]
		page.NextCursor = strconv.Itoa(offset + len(groups))
	}
	details := s.loadDetails(ctx, groups)
	for _, group := range groups {
		item := RecentUpdateItem{
			Kind: group.Kind, TMDBID: group.TMDBID, Title: group.FallbackTitle,
			Date: group.Date, UpdatedAt: time.Unix(group.UpdatedAt, 0).UTC().Format(time.RFC3339),
			Season: group.Season, MetadataMissing: true,
		}
		if detail := details[recentUpdateKey{kind: group.Kind, id: group.TMDBID}]; detail != nil {
			item.Title = detail.Title
			item.PosterURL = detail.PosterURL
			item.ReleaseDate = detail.Date
			item.Rating = detail.Rating
			item.Votes = detail.Votes
			item.MetadataMissing = false
			if group.Kind == "tv" {
				for _, season := range detail.Seasons {
					if season.Number == group.Season && season.PosterURL != "" {
						item.PosterURL = season.PosterURL
						break
					}
				}
			}
		}
		if group.Kind == "tv" {
			item.Title = strings.TrimSpace(item.Title) + "（" + seasonDisplayName(group.Season) + "）"
			item.EpisodeLabel, item.EpisodeCount = episodeUpdateLabel(group.Season, group.Episodes)
		}
		if item.Title == "" {
			item.Title = "未命名影视"
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

func recentUpdatePeriod(today time.Time, query RecentUpdatesQuery) (time.Time, time.Time, error) {
	if query.Date != "" {
		value, err := time.ParseInLocation("2006-01-02", query.Date, today.Location())
		if err != nil || value.Format("2006-01-02") != query.Date {
			return time.Time{}, time.Time{}, ErrRecentUpdatesInput
		}
		return value, value.AddDate(0, 0, 1), nil
	}
	switch query.Range {
	case "", "today":
		return today, today.AddDate(0, 0, 1), nil
	case "yesterday":
		return today.AddDate(0, 0, -1), today, nil
	case "7d":
		return today.AddDate(0, 0, -6), today.AddDate(0, 0, 1), nil
	default:
		return time.Time{}, time.Time{}, ErrRecentUpdatesInput
	}
}

func (s *RecentUpdatesService) loadDetails(ctx context.Context, groups []model.MediaUpdateGroup) map[recentUpdateKey]*tmdb.Detail {
	keys := make(map[recentUpdateKey]struct{}, len(groups))
	for _, item := range groups {
		keys[recentUpdateKey{kind: item.Kind, id: item.TMDBID}] = struct{}{}
	}
	result := make(map[recentUpdateKey]*tmdb.Detail, len(keys))
	if s.Metadata == nil {
		return result
	}
	// Titles from the local media index remain usable if optional TMDB metadata
	// is slow. Read all cached cards before starting any bounded network work.
	metadataCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if cached, ok := s.Metadata.(interface {
		CachedDetails(context.Context, string, int) (*tmdb.Detail, error)
	}); ok {
		for key := range keys {
			if metadataCtx.Err() != nil {
				break
			}
			if detail, err := cached.CachedDetails(metadataCtx, key.kind, key.id); err == nil && detail != nil {
				result[key] = detail
				delete(keys, key)
			}
		}
	}
	var mu sync.Mutex
	work, workCtx := errgroup.WithContext(metadataCtx)
	work.SetLimit(4)
	for key := range keys {
		if workCtx.Err() != nil {
			break
		}
		key := key
		work.Go(func() error {
			if workCtx.Err() != nil {
				return nil
			}
			detail, err := s.Metadata.Details(workCtx, key.kind, key.id)
			if err == nil && detail != nil {
				mu.Lock()
				result[key] = detail
				mu.Unlock()
			}
			return nil
		})
	}
	_ = work.Wait()
	return result
}

func seasonDisplayName(number int) string {
	if number == 0 {
		return "特别篇"
	}
	return fmt.Sprintf("第 %d 季", number)
}

func episodeUpdateLabel(season int, ranges []model.MediaUpdateEpisode) (string, int) {
	episodes := make(map[int]struct{}, len(ranges))
	for _, item := range ranges {
		last := item.Last
		if last < item.First || last-item.First > 100 {
			last = item.First
		}
		for episode := item.First; episode <= last; episode++ {
			episodes[episode] = struct{}{}
		}
	}
	values := make([]int, 0, len(episodes))
	for episode := range episodes {
		values = append(values, episode)
	}
	sort.Ints(values)
	prefix := fmt.Sprintf("S%02d", season)
	if len(values) == 0 {
		return prefix, 0
	}
	continuous := true
	for i := 1; i < len(values); i++ {
		continuous = continuous && values[i] == values[i-1]+1
	}
	if continuous {
		if len(values) == 1 {
			return fmt.Sprintf("%s E%02d", prefix, values[0]), 1
		}
		return fmt.Sprintf("%s E%02d-E%02d", prefix, values[0], values[len(values)-1]), len(values)
	}
	if len(values) > 6 {
		return fmt.Sprintf("%s 更新%d集", prefix, len(values)), len(values)
	}
	parts := make([]string, 0, len(values))
	for _, episode := range values {
		parts = append(parts, fmt.Sprintf("E%02d", episode))
	}
	return prefix + " " + strings.Join(parts, ", "), len(values)
}
