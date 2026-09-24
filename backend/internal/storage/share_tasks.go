package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

func (s *Store) SaveShareTask(ctx context.Context, task model.ShareTask) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("编码分享任务: %w", err)
	}
	next := ""
	if task.NextRetryAt != nil {
		next = task.NextRetryAt.UTC().Format(databaseTime)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO share_tasks (id,status,mode,source_url,share_url,updated_at,next_retry_at,record,request) VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status,mode=excluded.mode,source_url=excluded.source_url,share_url=excluded.share_url,updated_at=excluded.updated_at,next_retry_at=excluded.next_retry_at,record=excluded.record,request=excluded.request`, task.ID, task.Status, task.Mode, task.SourceURL, task.ShareURL, task.UpdatedAt.UTC().Format(databaseTime), next, string(data), string(task.Request))
	if err != nil {
		return fmt.Errorf("保存分享任务: %w", err)
	}
	return nil
}

func (s *Store) queryShareTasks(ctx context.Context, query string, args ...any) ([]model.ShareTask, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询分享任务: %w", err)
	}
	defer rows.Close()
	tasks := make([]model.ShareTask, 0, 20)
	for rows.Next() {
		var task model.ShareTask
		var record, request string
		if err := rows.Scan(&record, &request); err != nil {
			return nil, fmt.Errorf("读取分享任务: %w", err)
		}
		if err := json.Unmarshal([]byte(record), &task); err != nil {
			return nil, fmt.Errorf("解析分享任务: %w", err)
		}
		task.Request = json.RawMessage(request)
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) ListShareTasks(ctx context.Context, status, mode, search string, limit, offset int) ([]model.ShareTask, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, fmt.Errorf("任务分页参数无效")
	}
	// 在分页前合并展示分类，保留原始状态供调度和清理使用。
	return s.queryShareTasks(ctx, `SELECT record,request FROM share_tasks WHERE
(?='' OR status=? OR (?='failed' AND status IN ('interrupted','cancelled')) OR (?='review' AND status='waiting_review'))
AND (?='' OR mode=?) AND (?='' OR instr(source_url,?)>0 OR instr(share_url,?)>0)
ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?`, status, status, status, status, mode, mode, search, search, search, limit, offset)
}

func (s *Store) DueShareTasks(ctx context.Context, now time.Time) ([]model.ShareTask, error) {
	return s.queryShareTasks(ctx, `SELECT record,request FROM share_tasks WHERE status IN ('queued','retry_queued') OR (status='waiting_review' AND next_retry_at<=?) ORDER BY next_retry_at,id LIMIT 20`, now.UTC().Format(databaseTime))
}

func (s *Store) GetShareTask(ctx context.Context, id string) (model.ShareTask, error) {
	tasks, err := s.queryShareTasks(ctx, `SELECT record,request FROM share_tasks WHERE id=?`, id)
	if err != nil {
		return model.ShareTask{}, err
	}
	if len(tasks) == 0 {
		return model.ShareTask{}, sql.ErrNoRows
	}
	return tasks[0], nil
}

// CompareShareTask 原子抢占，防止手动、定时重试和清理互相覆盖。
func (s *Store) CompareShareTask(ctx context.Context, before, task model.ShareTask) (bool, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return false, fmt.Errorf("编码重试任务: %w", err)
	}
	next := ""
	if task.NextRetryAt != nil {
		next = task.NextRetryAt.UTC().Format(databaseTime)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE share_tasks SET status=?,updated_at=?,next_retry_at=?,record=?,request=? WHERE id=? AND status=? AND updated_at=? AND request=?`, task.Status, task.UpdatedAt.UTC().Format(databaseTime), next, string(data), string(task.Request), before.ID, before.Status, before.UpdatedAt.UTC().Format(databaseTime), string(before.Request))
	if err != nil {
		return false, fmt.Errorf("抢占重试任务: %w", err)
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ClearShareTasks 只清理已结束的展示记录，不删除网盘文件和转存检查点。
func (s *Store) ClearShareTasks(ctx context.Context, ids []string) (int64, error) {
	query := `DELETE FROM share_tasks WHERE status NOT IN ('queued','running','waiting_review','retry_queued') AND NOT EXISTS (SELECT 1 FROM p115_folder_monitors m WHERE json_extract(m.record, '$.generationTaskId') = share_tasks.id AND COALESCE(json_extract(m.record, '$.batchDirectoryID'), '') <> '')`
	args := make([]any, 0, len(ids))
	if len(ids) > 0 {
		query += " AND id IN (?"
		args = append(args, ids[0])
		for _, id := range ids[1:] {
			query += ",?"
			args = append(args, id)
		}
		query += ")"
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("清理分享日志: %w", err)
	}
	return result.RowsAffected()
}

// InterruptShareTasks 将上次进程未完成的执行标记为中断，禁止重放不确定的云端写入。
func (s *Store) InterruptShareTasks(ctx context.Context) error {
	tasks, err := s.queryShareTasks(ctx, `SELECT record,request FROM share_tasks WHERE status='running'`)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		task.Status, task.Error = "interrupted", "服务在任务执行期间退出，请核对转存任务记录后再重新发起"
		task.UpdatedAt = time.Now().UTC()
		if err := s.SaveShareTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LoadShareRunCache(ctx context.Context, fingerprint string) (string, bool, error) {
	var record string
	err := s.db.QueryRowContext(ctx, `SELECT record FROM share_run_cache WHERE fingerprint=?`, fingerprint).Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取分享快照缓存: %w", err)
	}
	return record, true, nil
}

func (s *Store) SaveShareRunCache(ctx context.Context, fingerprint, record string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO share_run_cache(fingerprint,record,updated_at) VALUES(?,?,?)
ON CONFLICT(fingerprint) DO UPDATE SET record=excluded.record,updated_at=excluded.updated_at`, fingerprint, record, time.Now().UTC().Format(databaseTime))
	if err != nil {
		return fmt.Errorf("保存分享快照缓存: %w", err)
	}
	return nil
}

func (s *Store) DeleteShareRunCache(ctx context.Context, fingerprint string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM share_run_cache WHERE fingerprint=?`, fingerprint); err != nil {
		return fmt.Errorf("清理分享快照缓存: %w", err)
	}
	return nil
}
