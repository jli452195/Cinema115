package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/storage"
)

type classificationCacheTMDB struct {
	cacheTMDBFake
	origin []string
}

func (f *classificationCacheTMDB) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	f.detailCalls.Add(1)
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind}, CountryCodes: []string{"US"}, OriginCountryCodes: f.origin, OriginalLanguage: "ja", Genres: []tmdb.Genre{{ID: 16}}}, nil
}

func TestTMDBDetailUpgradesClassificationCacheOnce(t *testing.T) {
	for _, hasOrigin := range []bool{true, false} {
		name := "原产地正常"
		if !hasOrigin {
			name = "上游确实缺少原产地"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "metadata.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			legacy, err := encodeTMDB(&tmdb.Detail{Item: tmdb.Item{ID: 53296, Kind: "tv"}, CountryCodes: []string{"JP"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.SaveTMDBCache(ctx, "detail:v3:tv:53296", legacy, time.Now().Unix()); err != nil {
				t.Fatal(err)
			}
			source := &classificationCacheTMDB{}
			if hasOrigin {
				source.origin = []string{"JP"}
			}
			for range 3 {
				// 新服务实例也复用持久化详情，不按每集或空字段反复刷新。
				detail, err := NewTMDBService(source, db).Details(ctx, "tv", 53296)
				if err != nil || detail == nil || detail.OriginalLanguage != "ja" {
					t.Fatal("旧详情没有按需更新", detail, err)
				}
				facts := mediaClassificationFacts(detail)
				if hasOrigin && (len(facts.Countries) != 1 || facts.Countries[0] != "JP") || !hasOrigin && len(facts.Countries) != 0 {
					t.Fatal("剧集须使用上游原产地，缺失时不以制作地区补全", facts)
				}
			}
			if source.detailCalls.Load() != 1 {
				t.Fatal("同一作品重复请求详情", source.detailCalls.Load())
			}
		})
	}
}
