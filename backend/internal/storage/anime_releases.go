package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"strm115/backend/internal/model"
)

const animeReleaseColumns = `id,provider,post_key,original_guid,title,published_raw,published_at,time_version,parse_json,parser_version,content_fingerprint,observed_at`

func scanAnimeRelease(row animeScanner) (model.AnimeRelease, error) {
	var r model.AnimeRelease
	var raw string
	if err := row.Scan(&r.ID, &r.Provider, &r.PostKey, &r.OriginalGUID, &r.Title, &r.PublishedRaw, &r.PublishedAt, &r.TimeVersion, &raw, &r.ParserVersion, &r.ContentFingerprint, &r.ObservedAt); err != nil {
		return r, err
	}
	if err := decodeAnimeJSON(raw, &r.Parse); err != nil {
		return r, err
	}
	return r, nil
}

// SaveAnimeReleases saves observations atomically by provider/post identity.
// A colliding GUID or episode number has no effect on another release. Stale
// observations cannot overwrite newer content or downgrade parser versions.
func (s *Store) SaveAnimeReleases(ctx context.Context, releases []model.AnimeRelease) ([]model.AnimeRelease, error) {
	if len(releases) > 2000 {
		return nil, ErrAnimeInvalid
	}
	for _, r := range releases {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAnimeInvalid, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin anime releases: %w", err)
	}
	defer tx.Rollback()
	out, err := saveAnimeReleasesTx(ctx, tx, releases)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, animeDBError("commit anime releases", err)
	}
	return out, nil
}

// The preview and its release identities must commit together. The caller
// owns the transaction; this helper never reacquires Store's single connection.
func saveAnimeReleasesTx(ctx context.Context, tx *sql.Tx, releases []model.AnimeRelease) ([]model.AnimeRelease, error) {
	out := make([]model.AnimeRelease, 0, len(releases))
	for _, r := range releases {
		old, err := scanAnimeRelease(tx.QueryRowContext(ctx, `SELECT `+animeReleaseColumns+` FROM anime_releases WHERE provider=? AND post_key=?`, r.Provider, r.PostKey))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("find anime release: %w", err)
		}
		if err == nil {
			if r.ID != "" && r.ID != old.ID {
				return nil, ErrAnimeIdentity
			}
			if r.ObservedAt < old.ObservedAt || r.ParserVersion < old.ParserVersion || r.TimeVersion < old.TimeVersion {
				out = append(out, old)
				continue
			}
			r.ID = old.ID
		} else {
			if r.ID != "" {
				return nil, ErrAnimeIdentity
			}
			r.ID, err = newAnimeID()
			if err != nil {
				return nil, err
			}
		}
		raw, err := animeJSON(r.Parse)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO anime_releases(`+animeReleaseColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(provider,post_key) DO UPDATE SET original_guid=excluded.original_guid,title=excluded.title,published_raw=excluded.published_raw,published_at=excluded.published_at,time_version=excluded.time_version,parse_json=excluded.parse_json,parser_version=excluded.parser_version,content_fingerprint=excluded.content_fingerprint,observed_at=excluded.observed_at`, r.ID, r.Provider, r.PostKey, r.OriginalGUID, r.Title, r.PublishedRaw, r.PublishedAt, r.TimeVersion, raw, r.ParserVersion, r.ContentFingerprint, r.ObservedAt)
		if err != nil {
			return nil, animeDBError("save anime release", err)
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) GetAnimeRelease(ctx context.Context, id string) (model.AnimeRelease, error) {
	if !model.ValidAnimeID(id) {
		return model.AnimeRelease{}, ErrAnimeInvalid
	}
	r, err := scanAnimeRelease(s.db.QueryRowContext(ctx, `SELECT `+animeReleaseColumns+` FROM anime_releases WHERE id=?`, id))
	if err != nil {
		return r, fmt.Errorf("get anime release: %w", err)
	}
	return r, nil
}

func animeP1Disposition(disposition string) bool {
	switch disposition {
	case "seen", "history_skipped", "filtered", "needs_confirmation", "dismissed":
		return true
	}
	return false
}

// SaveAnimeReleaseDecisions persists non-executing P1 explanations against a
// fixed source revision and release fingerprint. Explicit dismissal survives
// later observations/rule changes; restoring it needs a future explicit action.
func (s *Store) SaveAnimeReleaseDecisions(ctx context.Context, sourceID string, expectedRevision int64, decisions []model.AnimeReleaseDecision) error {
	if !model.ValidAnimeID(sourceID) || expectedRevision < 1 || expectedRevision > model.AnimeMaxRevision || len(decisions) > 2000 {
		return ErrAnimeInvalid
	}
	for _, d := range decisions {
		if d.Disposition == "selected" || d.Disposition == "linked" {
			return ErrAnimeCapability
		}
		if d.SourceID != sourceID || d.SourceRevision != expectedRevision || !model.ValidAnimeID(d.ReleaseID) || !animeP1Disposition(d.Disposition) || d.ReasonCode != nil && !model.ValidAnimeID(*d.ReasonCode) {
			return ErrAnimeInvalid
		}
		if d.Snapshot.SchemaVersion != model.AnimeSchemaVersion {
			return ErrAnimeInvalid
		}
		if err := d.Snapshot.Binding.Validate(false); err != nil {
			return fmt.Errorf("%w: %v", ErrAnimeInvalid, err)
		}
		if err := d.Snapshot.Rules.Validate(false); err != nil {
			return fmt.Errorf("%w: %v", ErrAnimeInvalid, err)
		}
		if err := d.Snapshot.Parse.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrAnimeInvalid, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin anime decisions: %w", err)
	}
	defer tx.Rollback()
	source, err := scanAnimeSource(tx.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=?`, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAnimeConflict
	}
	if err != nil {
		return fmt.Errorf("read anime decision source: %w", err)
	}
	if source.Revision != expectedRevision || source.Status == "removed" {
		return ErrAnimeConflict
	}
	now := time.Now().UTC().UnixMilli()
	for _, d := range decisions {
		r, err := scanAnimeRelease(tx.QueryRowContext(ctx, `SELECT `+animeReleaseColumns+` FROM anime_releases WHERE id=?`, d.ReleaseID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAnimeInvalid
		}
		if err != nil {
			return fmt.Errorf("read anime decision release: %w", err)
		}
		if r.ContentFingerprint != d.ReleaseFingerprint {
			return ErrAnimeConflict
		}
		if !reflect.DeepEqual(d.Snapshot.Binding, source.Binding) || !reflect.DeepEqual(d.Snapshot.Rules, source.Rules) || !reflect.DeepEqual(d.Snapshot.Parse, r.Parse) || d.Snapshot.Title != r.Title {
			return ErrAnimeInvalid
		}
		raw, err := animeJSON(d.Snapshot)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO anime_release_decisions(source_id,release_id,disposition,reason_code,source_revision,release_fingerprint,snapshot_json,generation,updated_at) VALUES(?,?,?,?,?,?,?,1,?) ON CONFLICT(source_id,release_id) DO UPDATE SET disposition=excluded.disposition,reason_code=excluded.reason_code,source_revision=excluded.source_revision,release_fingerprint=excluded.release_fingerprint,snapshot_json=excluded.snapshot_json,generation=anime_release_decisions.generation+1,updated_at=excluded.updated_at WHERE anime_release_decisions.disposition NOT IN ('dismissed','selected','linked') AND anime_release_decisions.generation<?`, sourceID, d.ReleaseID, d.Disposition, d.ReasonCode, expectedRevision, d.ReleaseFingerprint, raw, now, model.AnimeMaxRevision)
		if err != nil {
			return animeDBError("save anime release decision", err)
		}
		if err := animeChanged(result); err != nil {
			if !errors.Is(err, ErrAnimeConflict) {
				return err
			}
			var existing string
			if err := tx.QueryRowContext(ctx, `SELECT disposition FROM anime_release_decisions WHERE source_id=? AND release_id=?`, sourceID, d.ReleaseID).Scan(&existing); err != nil {
				return fmt.Errorf("check preserved anime decision: %w", err)
			}
			if existing != "dismissed" {
				return ErrAnimeConflict
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return animeDBError("commit anime decisions", err)
	}
	return nil
}

type AnimeReleaseFilter struct {
	SourceID, Disposition string
	Page, PageSize        int
}
type AnimeReleasePage struct {
	Items                 []model.AnimeReleaseRecord
	Total, Page, PageSize int
}

func (s *Store) ListAnimeReleases(ctx context.Context, f AnimeReleaseFilter) (AnimeReleasePage, error) {
	var out AnimeReleasePage
	page, size, err := animePage(f.Page, f.PageSize)
	if err != nil {
		return out, err
	}
	if !model.ValidAnimeID(f.SourceID) || f.Disposition != "" && !animeP1Disposition(f.Disposition) && f.Disposition != "selected" && f.Disposition != "linked" {
		return out, ErrAnimeInvalid
	}
	out.Page, out.PageSize = page, size
	out.Items = make([]model.AnimeReleaseRecord, 0, size)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, fmt.Errorf("begin anime release list: %w", err)
	}
	defer tx.Rollback()
	where := ` WHERE d.source_id=? AND (?='' OR d.disposition=?)`
	args := []any{f.SourceID, f.Disposition, f.Disposition}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM anime_release_decisions d`+where, args...).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("count anime releases: %w", err)
	}
	// Collect decisions first and close rows before reading releases on the
	// single connection. History stays queryable after source soft removal.
	rows, err := tx.QueryContext(ctx, `SELECT d.source_id,d.release_id,d.disposition,d.reason_code,d.source_revision,d.release_fingerprint,d.snapshot_json,d.generation,d.updated_at FROM anime_release_decisions d`+where+` ORDER BY d.updated_at DESC,d.release_id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return out, fmt.Errorf("query anime release decisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rec model.AnimeReleaseRecord
		d := &rec.Decision
		var raw string
		if err := rows.Scan(&d.SourceID, &d.ReleaseID, &d.Disposition, &d.ReasonCode, &d.SourceRevision, &d.ReleaseFingerprint, &raw, &d.Generation, &d.UpdatedAt); err != nil {
			return out, fmt.Errorf("read anime release decision: %w", err)
		}
		if err := decodeAnimeJSON(raw, &d.Snapshot); err != nil {
			return out, fmt.Errorf("decode anime decision snapshot: %w", err)
		}
		out.Items = append(out.Items, rec)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("read anime release list: %w", err)
	}
	if err := rows.Close(); err != nil {
		return out, fmt.Errorf("close anime release list: %w", err)
	}
	for i := range out.Items {
		out.Items[i].Release, err = scanAnimeRelease(tx.QueryRowContext(ctx, `SELECT `+animeReleaseColumns+` FROM anime_releases WHERE id=?`, out.Items[i].Decision.ReleaseID))
		if err != nil {
			return out, fmt.Errorf("read anime release observation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("finish anime release list: %w", err)
	}
	return out, nil
}
