package service

import (
	"context"

	"strm115/backend/internal/storage"
)

type AnimeCatalogLinks interface {
	CacheAnimeCatalogLinks(context.Context, string, []storage.AnimeCatalogLink) error
}

func cacheAnimeEntryLink(ctx context.Context, store AnimeCatalogLinks, provider string, result AnimeSourceEntryResult) error {
	if store == nil {
		return nil
	}
	var unique *string
	for _, id := range result.Entry.BangumiIDs {
		if unique != nil && *unique != id {
			unique = nil
			break
		}
		v := id
		unique = &v
	}
	return store.CacheAnimeCatalogLinks(ctx, provider, []storage.AnimeCatalogLink{{ItemID: result.Entry.ID, BangumiID: unique, ObservedAt: result.ObservedAt.UnixMilli()}})
}
