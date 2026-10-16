package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"strm115/backend/internal/client/imagecache"
)

func (s *EmbyService) posterConfig(ctx context.Context, scope, id string) (EmbyConfig, error) {
	if imagecache.EmbyPosterURL(scope, id) == "" {
		return EmbyConfig{}, imagecache.ErrInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.config
	if c.APIKey == "" || len(c.LibraryIDs) == 0 || embyScope(c) != scope {
		return EmbyConfig{}, imagecache.ErrNotFound
	}
	exists, err := s.db.EmbyPublishedSeries(ctx, scope, id)
	if err != nil {
		return EmbyConfig{}, err
	}
	if !exists {
		return EmbyConfig{}, imagecache.ErrNotFound
	}
	return c, nil
}

// PosterRequest is used for cache misses and controlled background revalidation.
// The published Series ID is already known, so no item metadata is requested.
func (s *EmbyService) PosterRequest(ctx context.Context, scope, id string) (*http.Request, error) {
	c, err := s.posterConfig(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	address, err := url.JoinPath(c.URL, "emby", "Items", id, "Images", "Primary")
	if err != nil {
		return nil, fmt.Errorf("创建 Emby 海报地址: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"?maxWidth=160&maxHeight=240&quality=75&format=webp", nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Emby 海报请求: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.APIKey)
	req.Header.Set("Accept", "image/webp,image/jpeg,image/png")
	return req, nil
}

func (s *EmbyService) OpenPoster(ctx context.Context, scope, id string) (*imagecache.Asset, error) {
	if _, err := s.posterConfig(ctx, scope, id); err != nil {
		return nil, err
	}
	if s.Posters == nil {
		return nil, fmt.Errorf("Emby 海报缓存尚未初始化")
	}
	return s.Posters.Open(ctx, scope, id+".webp")
}
