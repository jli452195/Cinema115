package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

const embyMissingPageSize = 24

var ErrEmbyMissingInput = errors.New("缺集检测查询参数无效")

type EmbyMissingQuery struct {
	Search string
	Offset int
}

type EmbyEpisodeRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

type EmbyMissingSeason struct {
	Season       int                `json:"season"`
	LastEpisode  int                `json:"lastEpisode"`
	OwnedCount   int                `json:"ownedCount"`
	MissingCount int                `json:"missingCount"`
	Ranges       []EmbyEpisodeRange `json:"ranges"`
}

type EmbyMissingSeries struct {
	embyID       string
	Key          string              `json:"key"`
	TMDBID       string              `json:"tmdbId"`
	Title        string              `json:"title"`
	PosterURL    string              `json:"posterUrl"`
	MissingCount int                 `json:"missingCount"`
	Seasons      []EmbyMissingSeason `json:"seasons"`
}

type EmbyMissingSummary struct {
	Series   int `json:"series"`
	Seasons  int `json:"seasons"`
	Episodes int `json:"episodes"`
}

type EmbyMissingNotice struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type EmbyMissingPage struct {
	Items         []EmbyMissingSeries `json:"items"`
	Ready         bool                `json:"ready"`
	Configured    bool                `json:"configured"`
	SyncedAt      string              `json:"syncedAt"`
	Notices       []EmbyMissingNotice `json:"notices"`
	CheckedSeries int                 `json:"checkedSeries"`
	ExcludedCount int                 `json:"excludedCount"`
	Summary       EmbyMissingSummary  `json:"summary"`
	Total         int                 `json:"total"`
	Offset        int                 `json:"offset"`
	NextOffset    int                 `json:"nextOffset"`
	HasMore       bool                `json:"hasMore"`
}

func (s *EmbyService) MissingEpisodes(ctx context.Context, query EmbyMissingQuery) (EmbyMissingPage, error) {
	page := EmbyMissingPage{Items: []EmbyMissingSeries{}, Notices: []EmbyMissingNotice{}}
	search := strings.TrimSpace(query.Search)
	if !validEmbyMissingQuery(query) {
		return page, ErrEmbyMissingInput
	}
	// Keep the configuration, status and published rows consistent with sync/event commits.
	s.mu.RLock()
	status, err := s.statusLocked(ctx)
	if err != nil {
		s.mu.RUnlock()
		return page, err
	}
	page.Configured = status.Configured && len(s.config.LibraryIDs) > 0
	page.Ready = page.Configured && status.Active != ""
	page.SyncedAt = status.SyncedAt
	exclusions, err := s.db.EmbyMissingExclusions(ctx, embyMissingService(s.config))
	if err != nil {
		s.mu.RUnlock()
		return page, err
	}
	page.ExcludedCount = len(exclusions)
	if !page.Ready {
		s.mu.RUnlock()
		return page, nil
	}
	scope := embyScope(s.config)
	incomplete, err := s.db.EmbyMissingIncompleteEpisodes(ctx, scope, status.Active)
	if err != nil {
		s.mu.RUnlock()
		return page, err
	}
	items, err := s.db.EmbyMissingItems(ctx, scope, status.Active)
	s.mu.RUnlock()
	if err != nil {
		return page, err
	}
	items = filterEmbyMissingExclusions(items, exclusions)
	page.Notices = embyMissingNotices(status, incomplete)
	groups, checked, err := buildEmbyMissing(ctx, items)
	if err != nil {
		return page, err
	}
	page.CheckedSeries = checked
	filtered := make([]EmbyMissingSeries, 0, len(groups))
	search = strings.ToLower(search)
	for _, group := range groups {
		if search != "" && !strings.Contains(strings.ToLower(group.Title), search) && !strings.Contains(group.TMDBID, search) {
			continue
		}
		filtered = append(filtered, group)
		page.Summary.Series++
		page.Summary.Seasons += len(group.Seasons)
		page.Summary.Episodes += group.MissingCount
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Title != filtered[j].Title {
			return filtered[i].Title < filtered[j].Title
		}
		return filtered[i].Key < filtered[j].Key
	})
	page.Total = len(filtered)
	page.Offset = query.Offset
	if page.Total > 0 && page.Offset >= page.Total {
		page.Offset = (page.Total - 1) / embyMissingPageSize * embyMissingPageSize
	}
	if page.Total == 0 {
		page.Offset = 0
		return page, nil
	}
	end := min(page.Offset+embyMissingPageSize, page.Total)
	page.Items = filtered[page.Offset:end]
	page.NextOffset = end
	page.HasMore = end < page.Total
	// Posters are optional presentation data. Read cached details only for the current page;
	// never contact TMDB or Emby to render a detection report.
	for i := range page.Items {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		item := &page.Items[i]
		if item.TMDBID != "" {
			data, _, err := s.db.TMDBCache(ctx, tmdbDetailCachePrefix+"tv:"+item.TMDBID)
			if err != nil {
				return page, err
			}
			if len(data) > 0 {
				if detail, err := decodeTMDB[tmdb.Detail](data); err == nil {
					item.PosterURL = detail.PosterURL
				}
			}
		}
		if item.PosterURL == "" {
			item.PosterURL = imagecache.EmbyPosterURL(scope, item.embyID)
		}
	}
	return page, nil
}

func embyMissingNotices(status EmbyStatus, incomplete int) []EmbyMissingNotice {
	notices := make([]EmbyMissingNotice, 0, 6)
	if status.Status == "queued" || status.Status == "running" {
		notices = append(notices, EmbyMissingNotice{Code: "sync_running", Level: "info", Message: "媒体库同步中，当前显示上次完整索引的检测结果。"})
	} else if status.Status == "failed" {
		notices = append(notices, EmbyMissingNotice{Code: "sync_failed", Level: "warning", Message: "最近同步失败，当前显示上次完整索引。"})
	}
	if !status.Connected {
		if status.ConnectionError != "" {
			notices = append(notices, EmbyMissingNotice{Code: "connection_failed", Level: "warning", Message: "Emby 连接异常，近期变动可能尚未反映在结果中。"})
		} else {
			notices = append(notices, EmbyMissingNotice{Code: "connection_unconfirmed", Level: "info", Message: "Emby 连接尚未确认；检测依据当前本地索引。"})
		}
	}
	// Metadata retries are a subset of the event queue, not additional failures.
	if count := status.Pending - status.MetadataPending; count > 0 {
		notices = append(notices, EmbyMissingNotice{Code: "events_pending", Level: "info", Count: count, Message: fmt.Sprintf("%d 条入库通知处理中，结果将在本地索引更新后反映。", count)})
	}
	if count := status.Failed - status.MetadataFailed; count > 0 {
		notices = append(notices, EmbyMissingNotice{Code: "events_failed", Level: "warning", Count: count, Message: fmt.Sprintf("%d 条入库通知处理失败，近期变动可能尚未完整更新。", count)})
	}
	if count := status.MetadataPending + status.MetadataFailed; count > 0 {
		notices = append(notices, EmbyMissingNotice{Code: "metadata_incomplete", Level: "info", Count: count, Message: fmt.Sprintf("%d 条资料补查未完成，可能涉及 TMDB 编号或分集信息；检测依据当前本地索引。", count)})
	}
	if incomplete > 0 {
		notices = append(notices, EmbyMissingNotice{Code: "episodes_incomplete", Level: "warning", Count: incomplete, Message: fmt.Sprintf("%d 条分集记录缺少剧集归属、季号或集号，未参与缺集检测。", incomplete)})
	}
	return notices
}

func buildEmbyMissing(ctx context.Context, items []model.EmbyItem) ([]EmbyMissingSeries, int, error) {
	type show struct {
		result  EmbyMissingSeries
		seasons map[int][]EmbyEpisodeRange
		nameID  string
	}
	series := make(map[string]model.EmbyItem)
	for _, item := range items {
		if item.Type == "Series" {
			series[item.ID] = item
		}
	}
	shows := make(map[string]*show, len(series))
	for i, item := range items {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}
		if item.Type != "Episode" || item.SeriesID == "" || item.Season <= 0 || item.Episode <= 0 {
			continue
		}
		parent := series[item.SeriesID]
		key, tmdbID := embyMissingIdentity(item.SeriesID, parent.TMDB)
		group := shows[key]
		if group == nil {
			group = &show{result: EmbyMissingSeries{Key: key, TMDBID: tmdbID}, seasons: make(map[int][]EmbyEpisodeRange)}
			shows[key] = group
		}
		if parent.ID != "" && (group.result.embyID == "" || parent.ID < group.result.embyID) {
			group.result.embyID = parent.ID
		}
		if name := strings.TrimSpace(parent.Name); name != "" && (group.nameID == "" || item.SeriesID < group.nameID) {
			group.result.Title, group.nameID = name, item.SeriesID
		}
		last := item.EndEpisode
		if last < item.Episode || last-item.Episode > 100 {
			last = item.Episode
		}
		group.seasons[item.Season] = append(group.seasons[item.Season], EmbyEpisodeRange{First: item.Episode, Last: last})
	}
	results := make([]EmbyMissingSeries, 0, len(shows))
	for key, group := range shows {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		for season, ranges := range group.seasons {
			missing := embyMissingSeason(season, ranges)
			if missing.MissingCount > 0 {
				group.result.Seasons = append(group.result.Seasons, missing)
				group.result.MissingCount += missing.MissingCount
			}
		}
		if group.result.MissingCount == 0 {
			continue
		}
		if group.result.Title == "" {
			group.result.Title = "未命名剧集（" + key + "）"
		}
		sort.Slice(group.result.Seasons, func(i, j int) bool { return group.result.Seasons[i].Season < group.result.Seasons[j].Season })
		results = append(results, group.result)
	}
	return results, len(shows), nil
}

func embyMissingIdentity(seriesID, binding string) (string, string) {
	if id, err := strconv.ParseInt(binding, 10, 64); err == nil && id > 0 && strconv.FormatInt(id, 10) == binding {
		return "tmdb:" + binding, binding
	}
	return "emby:" + seriesID, ""
}

// Compute the complement of sorted intervals without allocating 1..maxEpisode.
// This also handles duplicate versions, overlapping multi-episode files and long shows.
func embyMissingSeason(season int, ranges []EmbyEpisodeRange) EmbyMissingSeason {
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].First != ranges[j].First {
			return ranges[i].First < ranges[j].First
		}
		return ranges[i].Last < ranges[j].Last
	})
	result := EmbyMissingSeason{Season: season, Ranges: []EmbyEpisodeRange{}}
	for _, interval := range ranges {
		if interval.First > result.LastEpisode && interval.First-result.LastEpisode > 1 {
			result.Ranges = append(result.Ranges, EmbyEpisodeRange{First: result.LastEpisode + 1, Last: interval.First - 1})
			result.MissingCount += interval.First - result.LastEpisode - 1
		}
		result.LastEpisode = max(result.LastEpisode, interval.Last)
	}
	result.OwnedCount = result.LastEpisode - result.MissingCount
	return result
}
