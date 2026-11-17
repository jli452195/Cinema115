package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

type ed2kSTRMStoredTask struct {
	Task        model.ED2KSTRMTask `json:"task"`
	RequestID   string             `json:"requestId"`
	Fingerprint string             `json:"fingerprint"`
	TargetID    string             `json:"targetId,omitempty"`
	OutputDir   string             `json:"outputDir,omitempty"`
	BaseURL     string             `json:"baseUrl,omitempty"`
}

func encodeED2KSTRMTask(v model.ED2KSTRMTask) (string, error) {
	raw, err := json.Marshal(ed2kSTRMStoredTask{Task: v, RequestID: v.RequestID, Fingerprint: v.Fingerprint, TargetID: v.TargetID, OutputDir: v.OutputDir, BaseURL: v.BaseURL})
	return string(raw), err
}

func (s *Store) ListED2KSTRMTargets(ctx context.Context) ([]model.ED2KSTRMTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM ed2k_strm_targets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.ED2KSTRMTarget, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v model.ED2KSTRMTarget
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SaveED2KSTRMTarget(ctx context.Context, v model.ED2KSTRMTarget) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ed2k_strm_targets(id,record) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, v.ID, string(raw))
	return err
}

func (s *Store) DeleteED2KSTRMTarget(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ed2k_strm_targets WHERE id=?`, id)
	return err
}

func (s *Store) ListED2KSTRMTasks(ctx context.Context) ([]model.ED2KSTRMTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM ed2k_strm_tasks ORDER BY updated_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.ED2KSTRMTask, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var w ed2kSTRMStoredTask
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		v := w.Task
		v.RequestID, v.Fingerprint, v.TargetID, v.OutputDir, v.BaseURL = w.RequestID, w.Fingerprint, w.TargetID, w.OutputDir, w.BaseURL
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SaveED2KSTRMTask(ctx context.Context, v model.ED2KSTRMTask) error {
	raw, err := encodeED2KSTRMTask(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ed2k_strm_tasks(id,status,updated_at,record) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,updated_at=excluded.updated_at,record=excluded.record`, v.ID, v.Status, v.UpdatedAt.UTC().Format(time.RFC3339Nano), raw)
	return err
}

func (s *Store) DeleteED2KSTRMTask(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ed2k_strm_tasks WHERE id=?`, id)
	return err
}

// SaveED2KSTRMImport 同事务保存内容登记和输出请求，避免半批接收后重放遗漏输出。
func (s *Store) SaveED2KSTRMImport(ctx context.Context, resources []model.ED2KSTRMResource, tasks []model.ED2KSTRMTask) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range resources {
		raw, err := encodeED2KSTRM(v)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ed2k_strm_resources(hash,size,sha1,status,record) VALUES(?,?,?,?,?) ON CONFLICT(hash,size) DO UPDATE SET sha1=excluded.sha1,status=excluded.status,record=excluded.record`, v.Hash, v.Size, v.SHA1, v.Status, raw); err != nil {
			return err
		}
	}
	for _, v := range tasks {
		raw, err := encodeED2KSTRMTask(v)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ed2k_strm_tasks(id,status,updated_at,record) VALUES(?,?,?,?)`, v.ID, v.Status, v.UpdatedAt.UTC().Format(time.RFC3339Nano), raw); err != nil {
			return fmt.Errorf("保存 ED2K 输出请求: %w", err)
		}
	}
	return tx.Commit()
}
