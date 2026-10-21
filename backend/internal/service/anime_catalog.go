package service

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/client/mikan"
)

type AnimeProvider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

func AnimeProviders() []AnimeProvider {
	return []AnimeProvider{{ID: "anibt", Name: "AniBT", Available: true}, {ID: "mikan", Name: "蜜柑", Available: true}, {ID: "garden", Name: "Anime Garden", Available: true}}
}

type AnimeMikanCatalog struct {
	mikan.Catalog
	Provider  string `json:"provider"`
	FetchedAt int64  `json:"fetchedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

var animeCatalogTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

func animeCatalogSeason(now time.Time) string {
	now = now.In(animeCatalogTimeZone)
	return now.Format("2006-") + [...]string{"01", "04", "07", "10"}[(int(now.Month())-1)/3]
}

// Catalogue freshness is independent of RSS polling. Future seasons keep the
// current-season TTL because their listings may still be changing frequently.
func animeCatalogTTL(season, keyword string, now time.Time) time.Duration {
	if keyword != "" {
		return 10 * time.Minute
	}
	if season != "" && season < animeCatalogSeason(now) {
		return 24 * time.Hour
	}
	return time.Hour
}

// Calendar and scoped search share Mikan's gate, cooldown, bounded cache and
// request coalescing with the existing RSS/group reads. No metadata-site calls.
func (s *MikanService) AnimeCatalog(ctx context.Context, season, keyword string, refresh bool) (AnimeMikanCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var out AnimeMikanCatalog
	if keyword == "" && !mikan.ValidCatalogSeason(season) {
		return out, mikan.ErrInput
	}
	if keyword != "" {
		var err error
		keyword, err = mikan.NormalizeQuery(keyword)
		if err != nil {
			return out, err
		}
		season = ""
	}
	reader, ok := s.Client.(interface {
		Catalog(context.Context, string, string) (mikan.Catalog, error)
	})
	if !ok {
		return out, mikan.ErrInput
	}
	entry, _, err := s.cachedReadTTL(ctx, "catalog\x00"+season+"\x00"+keyword, refresh, animeCatalogTTL(season, keyword, time.Now()), func(ctx context.Context) (mikan.Result, error) {
		catalog, err := reader.Catalog(ctx, season, keyword)
		if err != nil {
			return mikan.Result{}, err
		}
		return mikan.Result{Catalog: &catalog}, nil
	})
	if err != nil {
		return out, fmt.Errorf("read Mikan catalog: %w", err)
	}
	copy := cloneMikan(entry.result)
	if copy.Catalog == nil {
		return out, mikan.ErrParse
	}
	out.Catalog, out.Provider = *copy.Catalog, "mikan"
	out.FetchedAt, out.ExpiresAt = entry.fetched.UnixMilli(), entry.expires.UnixMilli()
	return out, nil
}
