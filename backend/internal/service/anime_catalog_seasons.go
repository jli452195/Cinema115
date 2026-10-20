package service

import (
	"context"
	"sort"
	"time"

	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/mikan"
)

type AnimeCatalogSeasons struct {
	Provider  string   `json:"provider"`
	Seasons   []string `json:"seasons"`
	FetchedAt int64    `json:"fetchedAt"`
	ExpiresAt int64    `json:"expiresAt"`
}

func (s *MikanService) AnimeCatalogSeasons(ctx context.Context, refresh bool) (AnimeCatalogSeasons, error) {
	reader, ok := s.Client.(interface {
		CatalogSeasons(context.Context) ([]string, error)
	})
	if !ok {
		return AnimeCatalogSeasons{}, mikan.ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	read, _, err := s.cachedRead(ctx, "catalog-seasons", refresh, func(ctx context.Context) (mikan.Result, error) {
		seasons, err := reader.CatalogSeasons(ctx)
		if err != nil {
			return mikan.Result{}, err
		}
		return mikan.Result{Catalog: &mikan.Catalog{Seasons: seasons}}, nil
	})
	if err != nil {
		return AnimeCatalogSeasons{}, err
	}
	return AnimeCatalogSeasons{Provider: "mikan", Seasons: append([]string{}, read.result.Catalog.Seasons...), FetchedAt: read.fetched.UnixMilli(), ExpiresAt: read.expires.UnixMilli()}, nil
}

func (s *AnibtService) CatalogSeasons(ctx context.Context, refresh bool) (AnimeCatalogSeasons, error) {
	now := time.Now()
	season := now.Format("2006-") + []string{"01", "04", "07", "10"}[(int(now.Month())-1)/3]
	path, err := anibt.CatalogPath(season, "")
	if err != nil {
		return AnimeCatalogSeasons{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	read, _, err := s.read(ctx, path, 10*time.Minute, refresh, nil, func(body []byte) error { _, err := anibt.ParseCatalog(body, season); return err })
	if err != nil {
		return AnimeCatalogSeasons{}, err
	}
	catalog, err := anibt.ParseCatalog(read.body, season)
	if err != nil {
		return AnimeCatalogSeasons{}, err
	}
	if len(catalog.AvailableSeasons) == 0 {
		return AnimeCatalogSeasons{}, anibt.ErrInvalid
	}
	seasons := append([]string{}, catalog.AvailableSeasons...)
	sort.Sort(sort.Reverse(sort.StringSlice(seasons)))
	return AnimeCatalogSeasons{Provider: "anibt", Seasons: seasons, FetchedAt: read.observed.UnixMilli(), ExpiresAt: read.expires.UnixMilli()}, nil
}

func (s *GardenService) CatalogSeasons(ctx context.Context, refresh bool) (AnimeCatalogSeasons, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	read, _, err := s.read(ctx, "/bgm/calendars", time.Hour, refresh, nil, func(body []byte) error { _, err := garden.ParseCatalogSeasons(body); return err })
	if err != nil {
		return AnimeCatalogSeasons{}, err
	}
	seasons, err := garden.ParseCatalogSeasons(read.body)
	if err != nil {
		return AnimeCatalogSeasons{}, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(seasons)))
	return AnimeCatalogSeasons{Provider: "garden", Seasons: seasons, FetchedAt: read.observed.UnixMilli(), ExpiresAt: read.expires.UnixMilli()}, nil
}
