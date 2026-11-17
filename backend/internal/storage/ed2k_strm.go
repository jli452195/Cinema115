package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"strm115/backend/internal/model"
)

const ed2kSTRMSchema = `
CREATE TABLE IF NOT EXISTS ed2k_strm_settings(id INTEGER PRIMARY KEY CHECK(id=1), record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS ed2k_strm_cache(account_id TEXT PRIMARY KEY, record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS ed2k_strm_resources(hash TEXT NOT NULL, size INTEGER NOT NULL CHECK(size>0), sha1 TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, record TEXT NOT NULL, PRIMARY KEY(hash,size));
CREATE INDEX IF NOT EXISTS ed2k_strm_content ON ed2k_strm_resources(sha1,size);
CREATE INDEX IF NOT EXISTS ed2k_strm_status ON ed2k_strm_resources(status);`

// 持久记录单独封装，避免任务凭据快照等内部字段出现在管理 API。
type ed2kSTRMStored struct {
	Resource         model.ED2KSTRMResource `json:"resource"`
	AccountID        string                 `json:"accountId"`
	UID              string                 `json:"uid"`
	AccountRevision  uint64                 `json:"accountRevision"`
	CredentialScope  string                 `json:"credentialScope"`
	Settings         model.ED2KSTRMSettings `json:"settings"`
	DirectoryPending bool                   `json:"directoryPending"`
	FileID           string                 `json:"fileId"`
	PickCode         string                 `json:"pickCode"`
	Stage            string                 `json:"stage"`
	Attempts         int                    `json:"attempts"`
	NextRunAt        string                 `json:"nextRunAt"`
}

func (s *Store) GetED2KSTRMSettings(ctx context.Context) (model.ED2KSTRMSettings, error) {
	var v model.ED2KSTRMSettings
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM ed2k_strm_settings WHERE id=1`).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v, ErrNotFound
		}
		return v, fmt.Errorf("读取 ED2K STRM 设置: %w", err)
	}
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}

func (s *Store) SaveED2KSTRMSettings(ctx context.Context, v model.ED2KSTRMSettings) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ed2k_strm_settings(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(raw))
	return err
}

func (s *Store) GetED2KSTRMCache(ctx context.Context, accountID string) (model.ED2KSTRMCache, error) {
	var v model.ED2KSTRMCache
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM ed2k_strm_cache WHERE account_id=?`, accountID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v, ErrNotFound
		}
		return v, err
	}
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}

func (s *Store) SaveED2KSTRMCache(ctx context.Context, v model.ED2KSTRMCache) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ed2k_strm_cache(account_id,record) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET record=excluded.record`, v.AccountID, string(raw))
	return err
}

func encodeED2KSTRM(v model.ED2KSTRMResource) (string, error) {
	w := ed2kSTRMStored{Resource: v, AccountID: v.AccountID, UID: v.UID, AccountRevision: v.AccountRevision, CredentialScope: v.CredentialScope, Settings: v.Settings, DirectoryPending: v.DirectoryPending, FileID: v.FileID, PickCode: v.PickCode, Stage: v.Stage, Attempts: v.Attempts, NextRunAt: v.NextRunAt.Format("2006-01-02T15:04:05.999999999Z07:00")}
	raw, err := json.Marshal(w)
	return string(raw), err
}

func decodeED2KSTRM(raw string) (model.ED2KSTRMResource, error) {
	var w ed2kSTRMStored
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return w.Resource, err
	}
	v := w.Resource
	v.AccountID, v.UID, v.AccountRevision, v.CredentialScope = w.AccountID, w.UID, w.AccountRevision, w.CredentialScope
	v.Settings, v.DirectoryPending, v.FileID, v.PickCode = w.Settings, w.DirectoryPending, w.FileID, w.PickCode
	v.Stage, v.Attempts = w.Stage, w.Attempts
	if w.NextRunAt != "" {
		if err := v.NextRunAt.UnmarshalText([]byte(w.NextRunAt)); err != nil {
			return v, err
		}
	}
	return v, nil
}

func (s *Store) SaveED2KSTRMResource(ctx context.Context, v model.ED2KSTRMResource) error {
	raw, err := encodeED2KSTRM(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ed2k_strm_resources(hash,size,sha1,status,record) VALUES(?,?,?,?,?) ON CONFLICT(hash,size) DO UPDATE SET sha1=excluded.sha1,status=excluded.status,record=excluded.record`, v.Hash, v.Size, v.SHA1, v.Status, raw)
	return err
}

func (s *Store) GetED2KSTRMResource(ctx context.Context, hash string, size int64) (model.ED2KSTRMResource, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM ed2k_strm_resources WHERE hash=? AND size=?`, hash, size).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ED2KSTRMResource{}, ErrNotFound
		}
		return model.ED2KSTRMResource{}, err
	}
	return decodeED2KSTRM(raw)
}

func (s *Store) ListED2KSTRMResources(ctx context.Context) ([]model.ED2KSTRMResource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM ed2k_strm_resources ORDER BY json_extract(record,'$.resource.updatedAt') DESC,hash,size`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.ED2KSTRMResource, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		v, err := decodeED2KSTRM(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
