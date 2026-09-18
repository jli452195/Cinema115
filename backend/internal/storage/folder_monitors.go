package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

func validateFolderMonitor(value model.FolderMonitor) error {
	if value.ID == "" || value.Name == "" || value.AccountID == "" || value.SourceCID == "" || value.UpdatedAt.IsZero() {
		return fmt.Errorf("目录监控缺少必要字段")
	}
	if value.Stage == "" {
		return fmt.Errorf("目录监控缺少状态")
	}
	if value.IntervalSeconds < 30 || value.IntervalSeconds > 86400 {
		return fmt.Errorf("目录监控扫描间隔必须为 30 到 86400 秒")
	}
	if value.MinSizeMB < 0 {
		return fmt.Errorf("目录监控最小文件大小不能为负数")
	}
	return nil
}

func (s *Store) SaveFolderMonitor(ctx context.Context, value model.FolderMonitor) error {
	if err := validateFolderMonitor(value); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码目录监控: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO p115_folder_monitors(id,account_id,status,updated_at,record)
VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,status=excluded.status,updated_at=excluded.updated_at,record=excluded.record`,
		value.ID, value.AccountID, value.Stage, value.UpdatedAt.UTC().Format(databaseTime), string(raw))
	if err != nil {
		return fmt.Errorf("保存目录监控检查点: %w", err)
	}
	return nil
}

func scanFolderMonitor(row interface{ Scan(...any) error }) (model.FolderMonitor, error) {
	var value model.FolderMonitor
	var raw, updated string
	if err := row.Scan(&raw, &updated); err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, fmt.Errorf("解析目录监控记录: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return value, fmt.Errorf("解析目录监控更新时间: %w", err)
	}
	value.UpdatedAt = parsed
	if err := validateFolderMonitor(value); err != nil {
		return value, err
	}
	return value, nil
}

func (s *Store) GetFolderMonitor(ctx context.Context, id string) (model.FolderMonitor, error) {
	value, err := scanFolderMonitor(s.db.QueryRowContext(ctx, "SELECT record,updated_at FROM p115_folder_monitors WHERE id=?", id))
	if err != nil {
		if err == sql.ErrNoRows {
			return value, ErrNotFound
		}
		return value, fmt.Errorf("读取目录监控: %w", err)
	}
	return value, nil
}

func (s *Store) ListFolderMonitors(ctx context.Context) ([]model.FolderMonitor, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT record,updated_at FROM p115_folder_monitors ORDER BY updated_at DESC,id DESC")
	if err != nil {
		return nil, fmt.Errorf("查询目录监控: %w", err)
	}
	defer rows.Close()
	values := make([]model.FolderMonitor, 0)
	for rows.Next() {
		value, err := scanFolderMonitor(rows)
		if err != nil {
			return nil, fmt.Errorf("解析目录监控列表: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取目录监控列表: %w", err)
	}
	return values, nil
}

func (s *Store) DeleteFolderMonitor(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM p115_folder_monitors WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("删除目录监控: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("确认目录监控删除结果: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}
