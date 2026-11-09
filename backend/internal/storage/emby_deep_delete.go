package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"strm115/backend/internal/model"
)

const embyDeepDeleteSchema = `
CREATE TABLE IF NOT EXISTS emby_deep_deletes (
 key TEXT PRIMARY KEY,
 state TEXT NOT NULL CHECK(state IN ('pending','sending','accepted','uncertain','skipped')),
 record TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS emby_deep_delete_pending ON emby_deep_deletes(state);
CREATE INDEX IF NOT EXISTS drive_strm_file_identity ON drive_strm_files(file_id,directory);`

// QueueEmbyDeepDeletes 在同一事务内去重；任何已处理状态都不会被重复通知重置。
func (s *Store) QueueEmbyDeepDeletes(ctx context.Context, jobs []model.EmbyDeepDeleteJob) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("开始保存 Emby 删除联动任务: %w", err)
	}
	defer tx.Rollback()
	count := 0
	for _, job := range jobs {
		if job.Key == "" {
			return 0, fmt.Errorf("Emby 删除联动任务缺少去重标识")
		}
		raw, err := json.Marshal(job)
		if err != nil {
			return 0, fmt.Errorf("编码 Emby 删除联动任务: %w", err)
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO emby_deep_deletes(key,state,record) VALUES(?,'pending',?)`, job.Key, string(raw))
		if err != nil {
			return 0, fmt.Errorf("保存 Emby 删除联动任务: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("读取 Emby 删除联动任务入队数量: %w", err)
		}
		count += int(affected)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交 Emby 删除联动任务: %w", err)
	}
	return count, nil
}

func (s *Store) PendingEmbyDeepDeletes(ctx context.Context, limit int) ([]model.EmbyDeepDeleteJob, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("Emby 删除联动任务读取数量无效")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM emby_deep_deletes WHERE state='pending' ORDER BY rowid LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("读取 Emby 删除联动任务: %w", err)
	}
	defer rows.Close()
	jobs := make([]model.EmbyDeepDeleteJob, 0, limit)
	for rows.Next() {
		var raw string
		var job model.EmbyDeepDeleteJob
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("读取 Emby 删除联动任务内容: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return nil, fmt.Errorf("解析 Emby 删除联动任务: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 Emby 删除联动任务: %w", err)
	}
	return jobs, nil
}

func (s *Store) SetEmbyDeepDeleteState(ctx context.Context, keys []string, state string) error {
	switch state {
	case "pending", "sending", "accepted", "uncertain", "skipped":
	default:
		return fmt.Errorf("Emby 删除联动任务状态无效")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始更新 Emby 删除联动状态: %w", err)
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `UPDATE emby_deep_deletes SET state=? WHERE key=?`, state, key); err != nil {
			return fmt.Errorf("更新 Emby 删除联动状态: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 删除联动状态: %w", err)
	}
	return nil
}

// RecoverEmbyDeepDeletes 不重放中断的写入；其外部结果未知，保留为待人工核对。
func (s *Store) RecoverEmbyDeepDeletes(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE emby_deep_deletes SET state='uncertain' WHERE state='sending'`)
	if err != nil {
		return 0, fmt.Errorf("恢复 Emby 删除联动状态: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取 Emby 删除联动恢复数量: %w", err)
	}
	return count, nil
}

// FindDriveSTRMDeleteCandidates 使用文件身份索引读取所有来源，交由业务层核对归属和歧义。
func (s *Store) FindDriveSTRMDeleteCandidates(ctx context.Context, fileID string) ([]model.DriveSTRMFile, error) {
	return s.listDriveSTRMFiles(ctx, `file_id=? AND directory=0`, fileID)
}
