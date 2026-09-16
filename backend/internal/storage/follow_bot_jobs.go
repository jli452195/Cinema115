package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

func (s *Store) RecoverFollowBotJobs(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE follow_requests SET status='processing',note='资源待确认',updated_at=?,revision=revision+1
WHERE status<>'completed' AND search_closed=0 AND (kind,tmdb_id,cycle) IN (SELECT kind,tmdb_id,cycle FROM follow_bot_jobs WHERE status='generating')`, now); err != nil {
		return fmt.Errorf("恢复追更待确认状态: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='waiting_library',updated_at=? WHERE status='generating'`, now); err != nil {
		return fmt.Errorf("恢复追更生成任务: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='cancelled',due=0,last_error='',updated_at=? WHERE status IN ('checking','queued','running') AND EXISTS(
SELECT 1 FROM follow_requests r WHERE r.kind=follow_bot_jobs.kind AND r.tmdb_id=follow_bot_jobs.tmdb_id AND r.search_closed=1)`, now); err != nil {
		return fmt.Errorf("恢复已关闭追更任务: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='queued',due=0,last_error='',updated_at=? WHERE status='running' AND EXISTS(
SELECT 1 FROM follow_requests r WHERE r.kind=follow_bot_jobs.kind AND r.tmdb_id=follow_bot_jobs.tmdb_id AND r.search_closed=0)`, now); err != nil {
		return fmt.Errorf("恢复追更搜索任务: %w", err)
	}
	return tx.Commit()
}

func (s *Store) NextFollowBotJob(ctx context.Context, now time.Time) (*model.FollowBotJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var job model.FollowBotJob
	err = tx.QueryRowContext(ctx, `SELECT j.kind,j.tmdb_id,j.cycle,r.title,r.release_date,j.status,j.attempts
FROM follow_bot_jobs j JOIN follow_requests r USING(kind,tmdb_id)
WHERE j.status='queued' AND j.due<=? AND r.cycle=j.cycle AND r.search_closed=0
AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=j.kind AND f.tmdb_id=j.tmdb_id AND f.active=1)
ORDER BY j.created_at,j.kind,j.tmdb_id LIMIT 1`, now.Unix()).Scan(&job.Kind, &job.TMDBID, &job.Cycle, &job.Title, &job.Date, &job.Status, &job.Attempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取追更自动搜索队列: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='running',attempts=attempts+1,updated_at=? WHERE kind=? AND tmdb_id=? AND cycle=? AND status='queued'`, now.UTC().Format(time.RFC3339Nano), job.Kind, job.TMDBID, job.Cycle)
	if err != nil {
		return nil, fmt.Errorf("领取追更自动搜索任务: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return nil, fmt.Errorf("追更自动搜索任务已发生变化")
	}
	job.Status = "running"
	job.Attempts++
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) FollowBotJobActive(ctx context.Context, job model.FollowBotJob) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM follow_requests r WHERE r.kind=? AND r.tmdb_id=? AND r.cycle=? AND r.search_closed=0 AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=r.kind AND f.tmdb_id=r.tmdb_id AND f.active=1))`, job.Kind, job.TMDBID, job.Cycle).Scan(&active)
	return active, err
}

func (s *Store) FollowBotJobChecking(ctx context.Context, kind string, tmdbID int) (bool, error) {
	var checking bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM follow_bot_jobs j JOIN follow_requests r USING(kind,tmdb_id)
WHERE j.kind=? AND j.tmdb_id=? AND j.cycle=r.cycle AND j.status='checking' AND r.search_closed=0)`, kind, tmdbID).Scan(&checking)
	if err != nil {
		return false, fmt.Errorf("读取订阅自动任务检查状态: %w", err)
	}
	return checking, nil
}

func (s *Store) SetFollowBotGenerating(ctx context.Context, job model.FollowBotJob) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='generating',last_error='',updated_at=? WHERE kind=? AND tmdb_id=? AND cycle=? AND status='running'`, time.Now().UTC().Format(time.RFC3339Nano), job.Kind, job.TMDBID, job.Cycle)
	if err != nil {
		return false, fmt.Errorf("保存追更生成状态: %w", err)
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) FinishFollowBotGenerated(ctx context.Context, job model.FollowBotJob) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	result, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='waiting_library',last_error='',updated_at=? WHERE kind=? AND tmdb_id=? AND cycle=? AND status='generating'`, now.UTC().Format(time.RFC3339Nano), job.Kind, job.TMDBID, job.Cycle)
	if err != nil {
		return fmt.Errorf("保存资源待确认任务: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("追更生成任务已发生变化")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_requests SET status='processing',note='资源待确认',updated_at=?,revision=revision+1
WHERE kind=? AND tmdb_id=? AND cycle=? AND status<>'completed' AND search_closed=0 AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_requests.kind AND f.tmdb_id=follow_requests.tmdb_id AND f.active=1)`, now.UTC().Format(time.RFC3339Nano), job.Kind, job.TMDBID, job.Cycle); err != nil {
		return fmt.Errorf("更新资源待确认状态: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO website_notifications(user_id,kind,tmdb_id,title,message,created_at)
SELECT f.user_id,r.kind,r.tmdb_id,r.title,'资源待确认',? FROM user_follows f JOIN follow_requests r USING(kind,tmdb_id)
WHERE f.active=1 AND r.kind=? AND r.tmdb_id=? AND r.cycle=? AND r.search_closed=0`, now.Unix(), job.Kind, job.TMDBID, job.Cycle); err != nil {
		return fmt.Errorf("发送资源待确认消息: %w", err)
	}
	return tx.Commit()
}

func (s *Store) FinishFollowBotNoMatch(ctx context.Context, job model.FollowBotJob) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='no_match',last_error='',updated_at=? WHERE kind=? AND tmdb_id=? AND cycle=? AND status='running'`, now, job.Kind, job.TMDBID, job.Cycle)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_requests SET note='自动搜索未找到匹配资源',updated_at=?,revision=revision+1
WHERE kind=? AND tmdb_id=? AND cycle=? AND status='pending' AND search_closed=0 AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_requests.kind AND f.tmdb_id=follow_requests.tmdb_id AND f.active=1)`, now, job.Kind, job.TMDBID, job.Cycle); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailFollowBotJob(ctx context.Context, job model.FollowBotJob, message string, retry bool) error {
	status, due := "failed", int64(0)
	if retry && job.Attempts < 3 {
		status, due = "queued", time.Now().Add(15*time.Minute).Unix()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status=?,due=?,last_error=?,updated_at=? WHERE kind=? AND tmdb_id=? AND cycle=? AND status IN ('running','generating')`, status, due, message, now, job.Kind, job.TMDBID, job.Cycle)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if status == "failed" && changed == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE follow_requests SET note='自动资源处理失败，请查看任务日志',updated_at=?,revision=revision+1
WHERE kind=? AND tmdb_id=? AND cycle=? AND status<>'completed' AND search_closed=0 AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_requests.kind AND f.tmdb_id=follow_requests.tmdb_id AND f.active=1)`, now, job.Kind, job.TMDBID, job.Cycle); err != nil {
			return err
		}
	}
	return tx.Commit()
}
