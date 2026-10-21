package service

import (
	"context"

	"strm115/backend/internal/model"
)

func (s *AnimeAdminService) SaveRSSSettings(ctx context.Context, actor, key string, in model.AnimeRSSSettings) (model.AnimeRSSSettings, error) {
	k, err := animeRequestKey(actor, "rss-settings", key, in)
	if err != nil {
		return model.AnimeRSSSettings{}, err
	}
	out, err := s.Store.SaveAnimeRSSSettings(ctx, in, k)
	if err == nil {
		s.wakeAnimeDiscovery()
	}
	return out, err
}
