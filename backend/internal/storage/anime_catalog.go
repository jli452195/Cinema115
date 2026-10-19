package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"strm115/backend/internal/model"
)

const animeCatalogSchemaV45 = `
CREATE TABLE IF NOT EXISTS anime_catalog_links (
 provider TEXT NOT NULL CHECK(provider IN ('mikan','anibt','garden')),
 item_id TEXT NOT NULL,
 bangumi_id TEXT,
 observed_at INTEGER NOT NULL CHECK(observed_at>0),
 PRIMARY KEY(provider,item_id)
);
CREATE INDEX IF NOT EXISTS anime_sources_catalog ON anime_subscription_sources(provider,item_id,subscription_id) WHERE status<>'removed';
`

// Only server-observed site metadata may populate these links. A nil identity
// records a successful read with no unique association, replacing older links.
type AnimeCatalogLink struct {
	ItemID     string
	BangumiID  *string
	ObservedAt int64
}

type AnimeCatalogSubscription struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type AnimeCatalogStatus struct {
	ItemID        string                     `json:"itemId"`
	BangumiID     *string                    `json:"bangumiId"`
	Subscriptions []AnimeCatalogSubscription `json:"subscriptions"`
}

func validAnimeCatalogProvider(provider string) bool {
	return provider == "mikan" || provider == "anibt" || provider == "garden"
}

// Upstream timestamps make late cached reads unable to overwrite newer links.
// This cache is independent of subscription configuration and survives restart.
func (s *Store) CacheAnimeCatalogLinks(ctx context.Context, provider string, links []AnimeCatalogLink) error {
	if !validAnimeCatalogProvider(provider) || len(links) > 5000 {
		return ErrAnimeInvalid
	}
	for _, v := range links {
		if !model.ValidAnimeExternalID(v.ItemID) || v.ObservedAt <= 0 || v.BangumiID != nil && !model.ValidAnimeExternalID(*v.BangumiID) {
			return ErrAnimeInvalid
		}
	}
	if len(links) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin anime catalog links: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO anime_catalog_links(provider,item_id,bangumi_id,observed_at) VALUES(?,?,?,?)
ON CONFLICT(provider,item_id) DO UPDATE SET bangumi_id=excluded.bangumi_id,observed_at=excluded.observed_at
WHERE excluded.observed_at>anime_catalog_links.observed_at`)
	if err != nil {
		return fmt.Errorf("prepare anime catalog links: %w", err)
	}
	defer stmt.Close()
	for _, v := range links {
		if _, err := stmt.ExecContext(ctx, provider, v.ItemID, v.BangumiID, v.ObservedAt); err != nil {
			return fmt.Errorf("cache anime catalog link: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit anime catalog links: %w", err)
	}
	return nil
}

// One local query for the visible cards; no upstream or write side effects.
// Existing exact source associations also work before any external mapping is
// known. They never become evidence for a cross-site Bangumi association.
func (s *Store) AnimeCatalogStatuses(ctx context.Context, provider string, ids []string) ([]AnimeCatalogStatus, error) {
	if !validAnimeCatalogProvider(provider) || len(ids) == 0 || len(ids) > 200 {
		return nil, ErrAnimeInvalid
	}
	items := make([]AnimeCatalogStatus, 0, len(ids))
	positions := make(map[string]int, len(ids))
	args := make([]any, 0, len(ids)+2)
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		if !model.ValidAnimeExternalID(id) {
			return nil, ErrAnimeInvalid
		}
		if _, exists := positions[id]; exists {
			continue
		}
		positions[id] = len(items)
		items = append(items, AnimeCatalogStatus{ItemID: id, Subscriptions: []AnimeCatalogSubscription{}})
		values = append(values, "(?)")
		args = append(args, id)
	}
	args = append(args, provider, provider)
	rows, err := s.db.QueryContext(ctx, `WITH requested(item_id) AS (VALUES `+strings.Join(values, ",")+`),
links AS (SELECT r.item_id,m.bangumi_id FROM requested r LEFT JOIN anime_catalog_links m ON m.provider=? AND m.item_id=r.item_id),
matches AS (
 SELECT l.item_id,s.id,s.status FROM links l JOIN anime_entries e ON e.bangumi_id=l.bangumi_id
 JOIN anime_subscriptions s ON s.entry_id=e.id AND s.status<>'removed'
 UNION
 SELECT l.item_id,s.id,s.status FROM links l JOIN anime_subscription_sources src ON src.provider=? AND src.item_id=l.item_id AND src.status<>'removed'
 JOIN anime_subscriptions s ON s.id=src.subscription_id AND s.status<>'removed' WHERE l.bangumi_id IS NULL
)
SELECT l.item_id,l.bangumi_id,m.id,m.status FROM links l LEFT JOIN matches m ON m.item_id=l.item_id ORDER BY l.item_id,m.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query anime catalog status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var bangumiID, subID, status sql.NullString
		if err := rows.Scan(&id, &bangumiID, &subID, &status); err != nil {
			return nil, fmt.Errorf("read anime catalog status: %w", err)
		}
		v := &items[positions[id]]
		if bangumiID.Valid {
			b := bangumiID.String
			v.BangumiID = &b
		}
		if subID.Valid {
			v.Subscriptions = append(v.Subscriptions, AnimeCatalogSubscription{ID: subID.String, Status: status.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read anime catalog statuses: %w", err)
	}
	return items, nil
}
