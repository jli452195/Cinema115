package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"strm115/backend/internal/model"
)

var (
	ErrAnimeIdempotency     = fmt.Errorf("%w: idempotency key reused", ErrAnimeConflict)
	ErrAnimeAccountChanged  = errors.New("anime primary account snapshot changed")
	ErrAnimeSettingsChanged = errors.New("anime directory settings changed")
)

func (k AnimeWriteKey) Validate() error { return k.validate() }

type AnimeWriteReceipt struct {
	Status int
	Body   json.RawMessage
}

func (s *Store) FindAnimeSubscriptionID(ctx context.Context, entryID string) (string, error) {
	if !model.ValidAnimeID(entryID) {
		return "", ErrAnimeInvalid
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM anime_subscriptions WHERE entry_id=? AND status<>'removed'`, entryID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find anime subscription: %w", err)
	}
	return id, nil
}

// LookupAnimeReceipt runs before any current-version checks or upstream I/O.
// The final write repeats this lookup in its transaction.
func (s *Store) LookupAnimeReceipt(ctx context.Context, k AnimeWriteKey) (*AnimeWriteReceipt, error) {
	if err := k.validate(); err != nil {
		return nil, err
	}
	var digest, raw string
	var out AnimeWriteReceipt
	err := s.db.QueryRowContext(ctx, `SELECT request_digest,http_status,receipt_json FROM anime_request_keys WHERE actor_key=? AND operation=? AND request_key=?`, k.ActorKey, k.Operation, k.Key).Scan(&digest, &out.Status, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup anime receipt: %w", err)
	}
	if digest != k.Digest {
		return nil, ErrAnimeIdempotency
	}
	if raw == animeDeletedReceipt {
		return nil, sql.ErrNoRows
	}
	out.Body = json.RawMessage(raw)
	return &out, nil
}

// Entry is trusted, server-observed metadata. Settings is the exact locally
// read snapshot; RequireAccount is only needed for a complete paused config.
type AnimeConfigGuard struct {
	Entry          model.AnimeEntry
	Settings       model.AnimeSettings
	RequireAccount bool
	Runtime        bool
}

func (s *Store) SaveAnimeConfiguration(ctx context.Context, in model.AnimeSubscriptionConfig, key AnimeWriteKey, guard AnimeConfigGuard) (model.AnimeConfigReceipt, error) {
	return s.saveAnimeConfiguration(ctx, in, &key, &guard)
}

func readAnimeSettingsTx(ctx context.Context, tx *sql.Tx) (model.AnimeSettings, error) {
	var v model.AnimeSettings
	err := tx.QueryRowContext(ctx, `SELECT revision,account_id,account_revision,uid,download_cid,download_path,organize_cid,organize_path FROM anime_settings WHERE id=1`).Scan(&v.Revision, &v.AccountID, &v.AccountRevision, &v.UID, &v.DownloadCID, &v.DownloadPath, &v.OrganizeCID, &v.OrganizePath)
	return v, err
}

func checkAnimePrimaryTx(ctx context.Context, tx *sql.Tx, v model.AnimeSettings) error {
	if v.AccountID == nil || v.AccountRevision == nil || v.UID == nil {
		return ErrAnimeAccountChanged
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM p115_accounts WHERE id=? AND revision=? AND uid=? AND is_primary=1 AND ownership='admin' AND length(cookie_enc)>0)`, *v.AccountID, *v.AccountRevision, *v.UID).Scan(&exists); err != nil {
		return fmt.Errorf("check anime primary account: %w", err)
	}
	if !exists {
		return ErrAnimeAccountChanged
	}
	return nil
}

func prepareAnimeConfigurationTx(ctx context.Context, tx *sql.Tx, g AnimeConfigGuard) (string, error) {
	settings, err := readAnimeSettingsTx(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("check anime settings: %w", err)
	}
	if !reflect.DeepEqual(settings, g.Settings) {
		return "", ErrAnimeSettingsChanged
	}
	if g.RequireAccount {
		if err := checkAnimePrimaryTx(ctx, tx, settings); err != nil {
			return "", err
		}
	}
	e := g.Entry
	if err := e.Validate(); err != nil {
		return "", ErrAnimeInvalid
	}
	if e.ID != "" {
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM anime_entries WHERE id=?`, e.ID).Scan(&revision); err != nil {
			return "", fmt.Errorf("read anime chapter version: %w", err)
		}
		if revision != e.Revision {
			return "", ErrAnimeConflict
		}
		return e.ID, nil
	}
	if e.Revision != 0 {
		return "", ErrAnimeInvalid
	}
	if e.BangumiID != nil {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM anime_entries WHERE bangumi_id=? AND edition_key=?`, *e.BangumiID, e.EditionKey).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("find anime chapter: %w", err)
		}
	}
	id, err := newAnimeID()
	if err != nil {
		return "", err
	}
	raw, err := animeJSON(e.Metadata)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO anime_entries(`+animeEntryColumns+`) VALUES(?,?,?,?,?,?,1,?,?)`, id, e.BangumiID, e.EditionKey, e.Title, e.OriginalTitle, raw, now, now); err != nil {
		return "", animeDBError("create anime chapter", err)
	}
	return id, nil
}

// SaveAnimeSettings commits only verified directory facts and a credential-free
// receipt. No network operation is performed while the transaction is held.
func (s *Store) SaveAnimeSettings(ctx context.Context, v model.AnimeSettings, key AnimeWriteKey) (model.AnimeSettings, error) {
	if err := key.validate(); err != nil {
		return model.AnimeSettings{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AnimeSettings{}, fmt.Errorf("begin anime settings: %w", err)
	}
	defer tx.Rollback()
	var digest, raw string
	err = tx.QueryRowContext(ctx, `SELECT request_digest,receipt_json FROM anime_request_keys WHERE actor_key=? AND operation=? AND request_key=?`, key.ActorKey, key.Operation, key.Key).Scan(&digest, &raw)
	if err == nil {
		if digest != key.Digest {
			return model.AnimeSettings{}, ErrAnimeIdempotency
		}
		var receipt model.AnimeSettings
		if err := decodeAnimeJSON(raw, &receipt); err != nil {
			return receipt, fmt.Errorf("read anime settings receipt: %w", err)
		}
		return receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.AnimeSettings{}, fmt.Errorf("read anime settings receipt: %w", err)
	}
	if v.Revision < 1 || v.Revision >= model.AnimeMaxRevision || v.DownloadCID == nil || v.DownloadPath == nil || (v.OrganizeCID == nil) != (v.OrganizePath == nil) {
		return model.AnimeSettings{}, ErrAnimeInvalid
	}
	if err := checkAnimePrimaryTx(ctx, tx, v); err != nil {
		return model.AnimeSettings{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE anime_settings SET revision=revision+1,account_id=?,account_revision=?,uid=?,download_cid=?,download_path=?,organize_cid=?,organize_path=? WHERE id=1 AND revision=?`, v.AccountID, v.AccountRevision, v.UID, v.DownloadCID, v.DownloadPath, v.OrganizeCID, v.OrganizePath, v.Revision)
	if err != nil {
		return model.AnimeSettings{}, animeDBError("save anime settings", err)
	}
	if err := animeChanged(res); err != nil {
		return model.AnimeSettings{}, err
	}
	v.Revision++
	raw, err = animeJSON(v)
	if err != nil {
		return model.AnimeSettings{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO anime_request_keys(actor_key,operation,request_key,request_digest,http_status,receipt_json,created_at) VALUES(?,?,?,?,200,?,?)`, key.ActorKey, key.Operation, key.Key, key.Digest, raw, time.Now().UTC().UnixMilli()); err != nil {
		return model.AnimeSettings{}, animeDBError("save anime settings receipt", err)
	}
	if err := tx.Commit(); err != nil {
		return model.AnimeSettings{}, fmt.Errorf("commit anime settings: %w", err)
	}
	return v, nil
}
