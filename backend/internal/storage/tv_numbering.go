package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrNumberingConflict = errors.New("季集方案已变化，请重新读取并预览")

type TVNumberingRecord struct {
	Revision int64
	GroupID  string
	Name     string
	Type     int
	SavedAt  int64
	Snapshot []byte
}

func (s *Store) TVNumbering(ctx context.Context, id int) (TVNumberingRecord, error) {
	var r TVNumberingRecord
	err := s.db.QueryRowContext(ctx, `SELECT revision,group_id,name,type,saved_at,snapshot FROM tv_numbering WHERE tmdb_id=?`, id).Scan(&r.Revision, &r.GroupID, &r.Name, &r.Type, &r.SavedAt, &r.Snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, fmt.Errorf("读取季集方案: %w", err)
	}
	return r, nil
}

// Choice, version invalidation and a silent follow baseline reset commit together.
// Keep a default-choice row so reset cannot resurrect an obsolete revision.
func (s *Store) SaveTVNumbering(ctx context.Context, id int, r TVNumberingRecord, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存季集方案: %w", err)
	}
	defer tx.Rollback()
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM tv_numbering WHERE tmdb_id=?`, id).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取季集方案版本: %w", err)
	}
	if revision != expected {
		return ErrNumberingConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tv_numbering(tmdb_id,revision,group_id,name,type,saved_at,snapshot) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tmdb_id) DO UPDATE SET revision=excluded.revision,group_id=excluded.group_id,name=excluded.name,type=excluded.type,saved_at=excluded.saved_at,snapshot=excluded.snapshot`, id, expected+1, r.GroupID, r.Name, r.Type, r.SavedAt, r.Snapshot); err != nil {
		return fmt.Errorf("保存季集方案: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE follow_checks SET state='unknown',aired=0,owned=0,checked_at=0,error='',due=0,version=version+1 WHERE kind='tv' AND tmdb_id=?`, id); err != nil {
		return fmt.Errorf("安排季集方案追更复核: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE follow_requests SET status='pending',revision=revision+1 WHERE kind='tv' AND tmdb_id=? AND status='completed'`, id); err != nil {
		return fmt.Errorf("重置季集方案完成状态: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='checking',due=0 WHERE kind='tv' AND tmdb_id=? AND status IN ('queued','running','no_need')`, id); err != nil {
		return fmt.Errorf("安排资源任务复核: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交季集方案: %w", err)
	}
	return nil
}
