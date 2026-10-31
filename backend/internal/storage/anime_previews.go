package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
)

type AnimePreviewRef struct {
	BatchID      string `json:"batchId"`
	SourceRef    string `json:"sourceRef"`
	PreviewID    string `json:"sourcePreviewId"`
	LoadRevision int64  `json:"loadRevision"`
}

// Page is server-observed metadata, never a browser DTO. A nil Page reuses the
// referenced source; otherwise Previous preserves choices across regeneration.
type AnimePreviewSeed struct {
	SourceRef string
	Page      *mikan.SourcePage
	Previous  *AnimePreviewRef
}

func validAnimeActor(actor string) bool {
	return strings.TrimSpace(actor) == actor && actor != "" && len(actor) <= 256
}

func validateAnimePreviewConfigTx(ctx context.Context, tx *sql.Tx, c preview.Config) error {
	var settings model.AnimeSettings
	if err := tx.QueryRowContext(ctx, `SELECT revision,account_id,download_cid,download_path,organize_cid,organize_path FROM anime_settings WHERE id=1`).Scan(&settings.Revision, &settings.AccountID, &settings.DownloadCID, &settings.DownloadPath, &settings.OrganizeCID, &settings.OrganizePath); err != nil {
		return fmt.Errorf("read preview settings: %w", err)
	}
	if !reflect.DeepEqual(settings, c.Settings) {
		return preview.ErrConflict
	}
	if c.Chapter.ID != "" {
		entry, err := scanAnimeEntry(tx.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, c.Chapter.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return preview.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("read preview chapter: %w", err)
		}
		if entry.Revision != c.Chapter.Revision || entry.EditionKey != c.Chapter.EditionKey || !sameAnimeString(entry.BangumiID, c.Chapter.BangumiID) {
			return preview.ErrConflict
		}
	}
	if c.SubscriptionID != "" {
		sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, c.SubscriptionID))
		if errors.Is(err, sql.ErrNoRows) {
			return preview.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("read preview subscription: %w", err)
		}
		if sub.Revision != c.Revision || sub.EntryID != c.Chapter.ID || sub.Status == "removed" {
			return preview.ErrConflict
		}
	}
	for _, config := range c.Sources {
		if config.ID == "" {
			continue
		}
		source, err := scanAnimeSource(tx.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=?`, config.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return preview.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("read preview source: %w", err)
		}
		if source.SubscriptionID != c.SubscriptionID || source.Revision != config.ExpectedRevision || source.Status == "removed" ||
			!source.AcceptsIdentity(config.Provider, config.ItemID, config.GroupID) {
			return preview.ErrConflict
		}
	}
	return nil
}

// CheckAnimePreviewConfig performs local version checks before upstream I/O;
// every mutation repeats them inside its final transaction.
func (s *Store) CheckAnimePreviewConfig(ctx context.Context, c preview.Config) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin preview checks: %w", err)
	}
	defer tx.Rollback()
	if err := validateAnimePreviewConfigTx(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit()
}

func readAnimePreviewTx(ctx context.Context, tx *sql.Tx, actor, id string) (preview.Batch, error) {
	var b preview.Batch
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT id,actor_key,config_fingerprint,created_at,snapshot_json FROM anime_preview_batches WHERE id=? AND actor_key=?`, id, actor).Scan(&b.ID, &b.Actor, &b.Fingerprint, &b.CreatedAt, &raw)
	if err != nil {
		return b, fmt.Errorf("read anime preview: %w", err)
	}
	if err := decodeAnimeJSON(raw, &b.Config); err != nil {
		return b, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT snapshot_json FROM anime_source_previews WHERE batch_id=? ORDER BY source_ref`, id)
	if err != nil {
		return b, fmt.Errorf("read preview sources: %w", err)
	}
	b.Sources = make([]preview.Source, 0, model.AnimeMaxSources)
	for rows.Next() {
		var source preview.Source
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return b, err
		}
		if err := decodeAnimeJSON(raw, &source); err != nil {
			rows.Close()
			return b, err
		}
		b.Sources = append(b.Sources, source)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return b, err
	}
	if err := rows.Close(); err != nil {
		return b, err
	}
	for i := range b.Sources {
		source := &b.Sources[i]
		items, err := tx.QueryContext(ctx, `SELECT snapshot_json FROM anime_preview_items WHERE preview_id=? ORDER BY ordinal`, source.ID)
		if err != nil {
			return b, fmt.Errorf("read preview items: %w", err)
		}
		source.Items = make([]preview.Item, 0, len(source.Page.Releases))
		for items.Next() {
			var item preview.Item
			if err := items.Scan(&raw); err != nil {
				items.Close()
				return b, err
			}
			if err := decodeAnimeJSON(raw, &item); err != nil {
				items.Close()
				return b, err
			}
			source.Items = append(source.Items, item)
		}
		if err := items.Err(); err != nil {
			items.Close()
			return b, err
		}
		if err := items.Close(); err != nil {
			return b, err
		}
	}
	b.OrderSources()
	return b, nil
}

func (s *Store) GetAnimePreview(ctx context.Context, actor, id string) (preview.Batch, error) {
	if !validAnimeActor(actor) || !model.ValidAnimeID(id) {
		return preview.Batch{}, preview.ErrInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return preview.Batch{}, fmt.Errorf("begin read preview: %w", err)
	}
	defer tx.Rollback()
	b, err := readAnimePreviewTx(ctx, tx, actor, id)
	if err != nil {
		return b, err
	}
	if err := tx.Commit(); err != nil {
		return b, fmt.Errorf("finish read preview: %w", err)
	}
	return b, nil
}

func previewReferencedSource(b preview.Batch, ref AnimePreviewRef) (preview.Source, error) {
	if b.ID != ref.BatchID {
		return preview.Source{}, preview.ErrInput
	}
	for _, source := range b.Sources {
		if source.Ref == ref.SourceRef && source.ID == ref.PreviewID {
			if source.LoadRevision != ref.LoadRevision {
				return source, preview.ErrConflict
			}
			return source, nil
		}
	}
	return preview.Source{}, preview.ErrInput
}

func attachAnimePreviewIDsTx(ctx context.Context, tx *sql.Tx, source *preview.Source) error {
	releases := make([]model.AnimeRelease, len(source.Items))
	for i, item := range source.Items {
		releases[i] = item.Release
		releases[i].ID = ""
	}
	saved, err := saveAnimeReleasesTx(ctx, tx, releases)
	if err != nil {
		return err
	}
	for i := range saved {
		// Keep this snapshot's observation even if the global release already
		// has a newer observation. Only the stable identity is shared.
		source.Items[i].Release.ID = saved[i].ID
	}
	return nil
}

func writeAnimePreviewSourceTx(ctx context.Context, tx *sql.Tx, batchID string, source preview.Source, insert bool) error {
	snapshot := source
	snapshot.Items = nil
	raw, err := animeJSON(snapshot)
	if err != nil {
		return err
	}
	take := source.Page.RequestedTake
	if take == 0 {
		take = max(1, source.Page.RawCount)
	} // RSS does not request a take.
	var sourceID any
	if source.SourceID != "" {
		sourceID = source.SourceID
	}
	if insert {
		_, err = tx.ExecContext(ctx, `INSERT INTO anime_source_previews(id,batch_id,source_ref,source_id,source_revision,fingerprint,load_revision,t0,generated_at,expires_at,snapshot_observed_at,raw_count,requested_take,completeness,stop_reason,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, source.ID, batchID, source.Ref, sourceID, source.SourceRevision, source.Fingerprint, source.LoadRevision, source.T0, source.GeneratedAt, source.ExpiresAt, source.Page.ObservedAt.UnixMilli(), source.Page.RawCount, take, source.Page.Completeness, source.Page.StopReason, raw)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE anime_source_previews SET load_revision=?,snapshot_observed_at=?,raw_count=?,requested_take=?,completeness=?,stop_reason=?,snapshot_json=? WHERE id=? AND batch_id=?`, source.LoadRevision, source.Page.ObservedAt.UnixMilli(), source.Page.RawCount, take, source.Page.Completeness, source.Page.StopReason, raw, source.ID, batchID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM anime_preview_items WHERE preview_id=?`, source.ID)
		}
	}
	if err != nil {
		return animeDBError("write preview source", err)
	}
	for ordinal, item := range source.Items {
		raw, err := animeJSON(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_preview_items(preview_id,release_id,ordinal,selectable,default_selected,decision,reason_code,snapshot_json) VALUES(?,?,?,?,?,?,?,?)`, source.ID, item.Release.ID, ordinal, item.Selectable, item.DefaultSelected, item.Decision, item.ReasonCode, raw); err != nil {
			return animeDBError("write preview item", err)
		}
	}
	return nil
}

func (s *Store) CreateAnimePreview(ctx context.Context, actor string, c preview.Config, seeds []AnimePreviewSeed, now int64) (preview.Batch, error) {
	var b preview.Batch
	if !validAnimeActor(actor) || now <= 0 || len(seeds) < 1 || len(seeds) > model.AnimeMaxSources {
		return b, preview.ErrInput
	}
	c, err := preview.Normalize(c)
	if err != nil {
		return b, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return b, fmt.Errorf("begin create preview: %w", err)
	}
	defer tx.Rollback()
	if err := validateAnimePreviewConfigTx(ctx, tx, c); err != nil {
		return b, err
	}
	b = preview.Batch{Actor: actor, Config: c, CreatedAt: now, Sources: make([]preview.Source, 0, len(seeds))}
	b.ID, err = newAnimeID()
	if err != nil {
		return b, err
	}
	b.Fingerprint, err = preview.ConfigFingerprint(c)
	if err != nil {
		return b, err
	}
	seen := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		if seen[seed.SourceRef] {
			return b, preview.ErrInput
		}
		seen[seed.SourceRef] = true
		var old, source preview.Source
		if seed.Previous != nil {
			previous, err := readAnimePreviewTx(ctx, tx, actor, seed.Previous.BatchID)
			if err != nil {
				return b, err
			}
			if !preview.SameScope(c, previous.Config) {
				return b, preview.ErrInput
			}
			old, err = previewReferencedSource(previous, *seed.Previous)
			if err != nil {
				return b, err
			}
			if old.Ref != seed.SourceRef {
				return b, preview.ErrInput
			}
			// A different feed is a new source, never an inherited T0/choice.
			var feed mikan.Source
			for _, config := range c.Sources {
				if config.Ref == seed.SourceRef && config.ItemID != nil && config.GroupID != nil {
					feed = animefeed.NewSource(config.Provider, *config.ItemID, *config.GroupID)
				}
			}
			if old.Page.Source != feed {
				return b, preview.ErrInput
			}
		}
		if seed.Page == nil {
			if seed.Previous == nil {
				return b, preview.ErrInput
			}
			fp, err := preview.SourceFingerprint(c, seed.SourceRef)
			if err != nil {
				return b, err
			}
			if fp != old.Fingerprint {
				return b, preview.ErrConflict
			}
			if !old.ValidAt(now) {
				return b, preview.ErrExpired
			}
			source = old // Preserve absolute expiry, observedAt, T0 and selections.
		} else {
			source, err = preview.Build(c, seed.SourceRef, *seed.Page, now)
			if err != nil {
				return b, err
			}
			if err := attachAnimePreviewIDsTx(ctx, tx, &source); err != nil {
				return b, err
			}
			if seed.Previous == nil {
				source.InitializeSelections()
			} else {
				source.Reconcile(old)
			}
		}
		source.ID, err = newAnimeID()
		if err != nil {
			return b, err
		}
		b.Sources = append(b.Sources, source)
	}
	b.OrderSources()
	if b.Budget().Remaining < 0 {
		return b, &mikan.LimitError{Kind: "rows"}
	}
	raw, err := animeJSON(c)
	if err != nil {
		return b, err
	}
	var subID any
	if c.SubscriptionID != "" {
		subID = c.SubscriptionID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO anime_preview_batches(id,actor_key,subscription_id,expected_revision,settings_revision,config_fingerprint,created_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?)`, b.ID, actor, subID, c.Revision, c.Settings.Revision, b.Fingerprint, now, raw); err != nil {
		return b, animeDBError("create preview batch", err)
	}
	for _, source := range b.Sources {
		if err := writeAnimePreviewSourceTx(ctx, tx, b.ID, source, true); err != nil {
			return b, err
		}
	}
	if err := tx.Commit(); err != nil {
		return b, animeDBError("commit preview batch", err)
	}
	return b, nil
}

// SelectAnimePreview owns the selection CAS transaction across admin windows.
func (s *Store) SelectAnimePreview(ctx context.Context, actor string, ref AnimePreviewRef, selected, dismissed []string, now int64) (preview.Batch, error) {
	var b preview.Batch
	if !validAnimeActor(actor) || !model.ValidAnimeID(ref.BatchID) || ref.LoadRevision < 1 || now <= 0 {
		return b, preview.ErrInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return b, fmt.Errorf("begin update preview: %w", err)
	}
	defer tx.Rollback()
	b, err = readAnimePreviewTx(ctx, tx, actor, ref.BatchID)
	if err != nil {
		return b, err
	}
	old, err := previewReferencedSource(b, ref)
	if err != nil {
		return b, err
	}
	if !old.ValidAt(now) {
		return b, preview.ErrExpired
	}
	fp, err := preview.SourceFingerprint(b.Config, old.Ref)
	if err != nil {
		return b, err
	}
	if old.Fingerprint != fp {
		return b, preview.ErrConflict
	}
	if old.LoadRevision >= model.AnimeMaxRevision {
		return b, preview.ErrConflict
	}
	if err := validateAnimePreviewConfigTx(ctx, tx, b.Config); err != nil {
		return b, err
	}
	source := old
	if err := source.Select(selected, dismissed, now); err != nil {
		return b, err
	}
	source.LoadRevision = old.LoadRevision + 1
	for i := range b.Sources {
		if b.Sources[i].ID == old.ID {
			b.Sources[i] = source
		}
	}
	if b.Budget().Remaining < 0 {
		return b, &mikan.LimitError{Kind: "rows"}
	}
	result, err := tx.ExecContext(ctx, `UPDATE anime_source_previews SET load_revision=? WHERE id=? AND batch_id=? AND load_revision=? AND expires_at>?`, source.LoadRevision, source.ID, b.ID, ref.LoadRevision, now)
	if err != nil {
		return b, fmt.Errorf("compare preview revision: %w", err)
	}
	if err := animeChanged(result); err != nil {
		return b, preview.ErrConflict
	}
	if err := writeAnimePreviewSourceTx(ctx, tx, b.ID, source, false); err != nil {
		return b, err
	}
	if err := tx.Commit(); err != nil {
		return b, animeDBError("commit update preview", err)
	}
	return b, nil
}
