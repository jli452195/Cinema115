package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"strm115/backend/internal/model"
)

var ErrEmbyMissingChanged = errors.New("条目或检测范围已变化，请返回列表刷新后重新操作")

type EmbyMissingPreview struct {
	Scope        string                    `json:"scope"`
	Key          string                    `json:"key"`
	Revision     string                    `json:"revision"`
	Title        string                    `json:"title"`
	MissingCount int                       `json:"missingCount"`
	Sources      []model.EmbyMissingSource `json:"sources"`
}

type EmbyMissingExcludeInput struct {
	Scope    string `json:"scope"`
	Key      string `json:"key"`
	Revision string `json:"revision"`
}

type EmbyMissingRestoreInput struct {
	Service string `json:"service"`
	ID      string `json:"id"`
}

type EmbyMissingExcludedPage struct {
	Service    string                       `json:"service"`
	Items      []model.EmbyMissingExclusion `json:"items"`
	Count      int                          `json:"count"`
	Total      int                          `json:"total"`
	Offset     int                          `json:"offset"`
	NextOffset int                          `json:"nextOffset"`
	HasMore    bool                         `json:"hasMore"`
}

// Library selection is a report scope, not part of the service's durable identity.
func embyMissingService(c EmbyConfig) string {
	c.LibraryIDs = nil
	return embyScope(c)
}

func filterEmbyMissingExclusions(items []model.EmbyItem, exclusions []model.EmbyMissingExclusion) []model.EmbyItem {
	if len(exclusions) == 0 {
		return items
	}
	bindings := make(map[string]string)
	for _, item := range items {
		if item.Type == "Series" {
			bindings[item.ID] = item.TMDB
		}
	}
	blocked := make(map[string]bool, len(exclusions))
	for _, item := range exclusions {
		if bindings[item.SeriesID] == item.TMDBID {
			blocked[item.SeriesID] = true
		}
	}
	filtered := make([]model.EmbyItem, 0, len(items))
	for _, item := range items {
		if item.Type == "Series" && blocked[item.ID] || item.Type == "Episode" && blocked[item.SeriesID] {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func validEmbyMissingQuery(query EmbyMissingQuery) bool {
	return query.Offset >= 0 && query.Offset <= 1_000_000 && utf8.RuneCountInString(strings.TrimSpace(query.Search)) <= 100
}

func (s *EmbyService) MissingExclusions(ctx context.Context, query EmbyMissingQuery) (EmbyMissingExcludedPage, error) {
	page := EmbyMissingExcludedPage{Items: []model.EmbyMissingExclusion{}}
	if !validEmbyMissingQuery(query) {
		return page, ErrEmbyMissingInput
	}
	s.mu.RLock()
	page.Service = embyMissingService(s.config)
	items, err := s.db.EmbyMissingExclusions(ctx, page.Service)
	s.mu.RUnlock()
	if err != nil {
		return page, err
	}
	page.Count = len(items)
	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := make([]model.EmbyMissingExclusion, 0, len(items))
	for _, item := range items {
		if search == "" || strings.Contains(strings.ToLower(item.Title), search) || strings.Contains(item.TMDBID, search) {
			filtered = append(filtered, item)
		}
	}
	page.Total, page.Offset = len(filtered), query.Offset
	if page.Total == 0 {
		page.Offset = 0
		return page, nil
	}
	if page.Offset >= page.Total {
		page.Offset = (page.Total - 1) / embyMissingPageSize * embyMissingPageSize
	}
	page.NextOffset = min(page.Offset+embyMissingPageSize, page.Total)
	page.HasMore = page.NextOffset < page.Total
	page.Items = filtered[page.Offset:page.NextOffset]
	return page, nil
}

func (s *EmbyService) MissingExclusionPreview(ctx context.Context, key string) (EmbyMissingPreview, error) {
	if key == "" || len(key) > 512 {
		return EmbyMissingPreview{}, ErrEmbyMissingInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.missingExclusionPreviewLocked(ctx, key)
}

func (s *EmbyService) missingExclusionPreviewLocked(ctx context.Context, key string) (EmbyMissingPreview, error) {
	preview := EmbyMissingPreview{Scope: embyScope(s.config), Key: key, Sources: []model.EmbyMissingSource{}}
	status, err := s.statusLocked(ctx)
	if err != nil {
		return preview, err
	}
	if !status.Configured || len(s.config.LibraryIDs) == 0 || status.Active == "" {
		return preview, ErrEmbyMissingChanged
	}
	items, err := s.db.EmbyMissingItems(ctx, preview.Scope, status.Active)
	if err != nil {
		return preview, err
	}
	exclusions, err := s.db.EmbyMissingExclusions(ctx, embyMissingService(s.config))
	if err != nil {
		return preview, err
	}
	items = filterEmbyMissingExclusions(items, exclusions)
	groups, _, err := buildEmbyMissing(ctx, items)
	if err != nil {
		return preview, err
	}
	for _, group := range groups {
		if group.Key == key {
			preview.Title, preview.MissingCount = group.Title, group.MissingCount
			break
		}
	}
	if preview.MissingCount == 0 {
		return preview, ErrEmbyMissingChanged
	}
	parents := make(map[string]model.EmbyItem)
	for _, item := range items {
		if item.Type == "Series" {
			parents[item.ID] = item
		}
	}
	sources := make(map[string]*model.EmbyMissingSource)
	for _, item := range items {
		if item.Type != "Episode" || item.SeriesID == "" || item.Season <= 0 || item.Episode <= 0 {
			continue
		}
		parent := parents[item.SeriesID]
		identity, _ := embyMissingIdentity(item.SeriesID, parent.TMDB)
		if identity != key {
			continue
		}
		source := sources[item.SeriesID]
		if source == nil {
			title, library := parent.Name, parent.LibraryID
			if strings.TrimSpace(title) == "" {
				title = preview.Title
			}
			if library == "" {
				library = item.LibraryID
			}
			source = &model.EmbyMissingSource{SeriesID: item.SeriesID, TMDBID: parent.TMDB, Title: title, LibraryID: library}
			sources[item.SeriesID] = source
		}
		source.EpisodeCount++
	}
	for _, source := range sources {
		preview.Sources = append(preview.Sources, *source)
	}
	sort.Slice(preview.Sources, func(i, j int) bool { return preview.Sources[i].SeriesID < preview.Sources[j].SeriesID })
	// Recheck all exact sources and episode evidence at submission. A stale dialog
	// must never exclude a corrected binding, newly merged version or changed scope.
	h := sha256.New()
	encoder := json.NewEncoder(h)
	if err := encoder.Encode(preview); err != nil {
		return preview, fmt.Errorf("生成缺集排除预览: %w", err)
	}
	for _, item := range items {
		if item.Type == "Series" && sources[item.ID] != nil || item.Type == "Episode" && sources[item.SeriesID] != nil {
			if err := encoder.Encode(item); err != nil {
				return preview, fmt.Errorf("生成缺集来源校验: %w", err)
			}
		}
	}
	preview.Revision = hex.EncodeToString(h.Sum(nil))
	return preview, nil
}

func (s *EmbyService) ExcludeMissing(ctx context.Context, input EmbyMissingExcludeInput) (int, error) {
	if len(input.Scope) != 64 || len(input.Revision) != 64 || input.Key == "" || len(input.Key) > 512 {
		return 0, ErrEmbyMissingInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Scope != embyScope(s.config) {
		return 0, ErrEmbyMissingChanged
	}
	preview, err := s.missingExclusionPreviewLocked(ctx, input.Key)
	if err != nil {
		return 0, err
	}
	if preview.Revision != input.Revision {
		return 0, ErrEmbyMissingChanged
	}
	items := make([]model.EmbyMissingExclusion, 0, len(preview.Sources))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, source := range preview.Sources {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return 0, fmt.Errorf("生成缺集排除编号: %w", err)
		}
		items = append(items, model.EmbyMissingExclusion{EmbyMissingSource: source, ID: hex.EncodeToString(nonce[:]), ExcludedAt: now})
	}
	if err := s.db.SaveEmbyMissingExclusions(ctx, embyMissingService(s.config), items); err != nil {
		return 0, err
	}
	return len(items), nil
}

func (s *EmbyService) RestoreMissing(ctx context.Context, input EmbyMissingRestoreInput) error {
	if len(input.Service) != 64 || len(input.ID) != 32 {
		return ErrEmbyMissingInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Service != embyMissingService(s.config) {
		return ErrEmbyMissingChanged
	}
	removed, err := s.db.RestoreEmbyMissingExclusion(ctx, input.Service, input.ID)
	if err != nil {
		return err
	}
	if !removed {
		return ErrEmbyMissingChanged
	}
	return nil
}
