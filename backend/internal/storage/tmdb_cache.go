package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// 缓存只保存压缩后的数据和成功刷新时间；缺集、过期标记与下次刷新时间不落库。
func (s *Store) TMDBCache(ctx context.Context, key string) ([]byte, int64, error) {
	var data []byte
	var fetched int64
	err := s.db.QueryRowContext(ctx, `SELECT data,fetched_at FROM tmdb_cache WHERE key=?`, key).Scan(&data, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("读取 TMDB 缓存: %w", err)
	}
	return data, fetched, nil
}
func (s *Store) SaveTMDBCache(ctx context.Context, key string, data []byte, fetched int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存 TMDB 缓存: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO tmdb_cache(key,data,fetched_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data,fetched_at=excluded.fetched_at`, key, data, fetched); err != nil {
		return fmt.Errorf("保存 TMDB 缓存: %w", err)
	}
	// 完整简介按容量限制保留；全局缺集需要的精简播出表独立保留。
	if strings.HasPrefix(key, "detail:") || strings.HasPrefix(key, "season:") {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tmdb_cache WHERE key IN (SELECT key FROM tmdb_cache WHERE key GLOB 'detail:*' OR key GLOB 'season:*' ORDER BY fetched_at DESC,key LIMIT -1 OFFSET 512)`); err != nil {
			return fmt.Errorf("限制 TMDB 详情缓存: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 TMDB 缓存: %w", err)
	}
	return nil
}

type TMDBCacheRecord struct {
	Data      []byte
	FetchedAt int64
}

func (s *Store) TMDBSchedules(ctx context.Context, id int) ([]TMDBCacheRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data,fetched_at FROM tmdb_cache WHERE key GLOB ?`, fmt.Sprintf("schedule:%d:*", id))
	if err != nil {
		return nil, fmt.Errorf("读取剧集播出缓存: %w", err)
	}
	defer rows.Close()
	records := make([]TMDBCacheRecord, 0)
	for rows.Next() {
		var r TMDBCacheRecord
		if err := rows.Scan(&r.Data, &r.FetchedAt); err != nil {
			return nil, fmt.Errorf("读取季缓存记录: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历季缓存: %w", err)
	}
	return records, nil
}
