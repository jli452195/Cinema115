package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

const jobColumns = `id, source_url, source_cid, directory_name, directory_id, share_url, status, finished, last_error, updated_at`
const insertJob = `INSERT INTO reshare_jobs (` + jobColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
const databaseTime = "2006-01-02T15:04:05.000000000Z"

func validateJob(job model.ReshareJob) error {
	if job.ID == "" || job.Status == "" || job.UpdatedAt.IsZero() {
		return fmt.Errorf("任务缺少编号、阶段或更新时间")
	}
	return nil
}

func jobValues(job model.ReshareJob) []any {
	return []any{job.ID, job.SourceURL, job.SourceCID, job.DirectoryName, job.DirectoryID, job.ShareURL, job.Status, job.Finished, job.LastError, job.UpdatedAt.UTC().Format(databaseTime)}
}

func (s *Store) SaveReshareJob(ctx context.Context, job model.ReshareJob) error {
	if err := validateJob(job); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, insertJob+` ON CONFLICT(id) DO UPDATE SET
source_url=excluded.source_url, source_cid=excluded.source_cid,
directory_name=excluded.directory_name, directory_id=excluded.directory_id,
share_url=excluded.share_url, status=excluded.status, finished=excluded.finished,
last_error=excluded.last_error, updated_at=excluded.updated_at`, jobValues(job)...)
	if err != nil {
		return fmt.Errorf("保存转存任务检查点: %w", err)
	}
	return nil
}

func scanJob(row interface{ Scan(...any) error }) (model.ReshareJob, error) {
	var job model.ReshareJob
	var updated string
	if err := row.Scan(&job.ID, &job.SourceURL, &job.SourceCID, &job.DirectoryName, &job.DirectoryID, &job.ShareURL, &job.Status, &job.Finished, &job.LastError, &updated); err != nil {
		return job, err
	}
	var err error
	job.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return job, err
}

func (s *Store) GetReshareJob(ctx context.Context, id string) (model.ReshareJob, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM reshare_jobs WHERE id=?", id))
	if err != nil {
		return job, fmt.Errorf("读取转存任务: %w", err)
	}
	return job, nil
}

func (s *Store) ListReshareJobs(ctx context.Context, status string, limit, offset int) ([]model.ReshareJob, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, fmt.Errorf("任务分页参数无效")
	}
	query := "SELECT " + jobColumns + " FROM reshare_jobs"
	args := make([]any, 0, 3)
	if status != "" {
		query += " WHERE status=?"
		args = append(args, status)
	}
	query += " ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询转存任务: %w", err)
	}
	defer rows.Close()
	jobs := make([]model.ReshareJob, 0, limit)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("解析转存任务: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取任务列表: %w", err)
	}
	return jobs, nil
}

// 保留 database/sql 的未找到语义，方便接口区分 404 与数据库故障。
var ErrNotFound = sql.ErrNoRows
