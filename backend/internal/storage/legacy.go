package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

const legacyMigration = "reshare-json-v1"

type ImportResult struct {
	Imported    int  `json:"imported"`
	Skipped     int  `json:"skipped"`
	AlreadyDone bool `json:"alreadyDone"`
}

// ImportLegacyJobs 在单个事务内导入旧记录并写迁移标记，原文件始终保留。
// 迁移标记存在时不再读取旧文件，避免旧检查点覆盖后续数据库进度。
func (s *Store) ImportLegacyJobs(ctx context.Context, directory string) (ImportResult, error) {
	result := ImportResult{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("开始旧任务迁移: %w", err)
	}
	defer tx.Rollback()
	var count int
	err = tx.QueryRowContext(ctx, "SELECT imported_count FROM data_migrations WHERE name=?", legacyMigration).Scan(&count)
	if err == nil {
		result.AlreadyDone = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("读取旧任务迁移状态: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("读取旧任务目录: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ImportResult{}, err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ImportResult{}, fmt.Errorf("旧任务 %s 是符号链接，迁移已回滚", entry.Name())
		}
		job, err := readLegacyJob(filepath.Join(directory, entry.Name()))
		if err != nil {
			return ImportResult{}, fmt.Errorf("旧任务 %s 无法迁移，已回滚: %w", entry.Name(), err)
		}
		if job.ID != strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())) {
			return ImportResult{}, fmt.Errorf("旧任务 %s 的编号与文件名不一致，迁移已回滚", entry.Name())
		}
		// 数据库中已有的任务具有优先权，旧 JSON 不能覆盖它。
		values, err := jobValues(job)
		if err != nil {
			return ImportResult{}, err
		}
		res, err := tx.ExecContext(ctx, insertJob+" ON CONFLICT(id) DO NOTHING", values...)
		if err != nil {
			return ImportResult{}, fmt.Errorf("导入旧任务 %s: %w", entry.Name(), err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return ImportResult{}, fmt.Errorf("核对旧任务导入数量: %w", err)
		}
		if n == 1 {
			result.Imported++
		} else {
			result.Skipped++
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO data_migrations(name, imported_count, completed_at) VALUES (?, ?, ?)", legacyMigration, result.Imported, time.Now().UTC().Format(databaseTime)); err != nil {
		return ImportResult{}, fmt.Errorf("保存旧任务迁移标记: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, fmt.Errorf("提交旧任务迁移: %w", err)
	}
	return result, nil
}

func readLegacyJob(path string) (model.ReshareJob, error) {
	var job model.ReshareJob
	f, err := os.Open(path)
	if err != nil {
		return job, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return job, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return job, fmt.Errorf("记录不是普通文件或超过 4 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&job); err != nil {
		// 不原样输出解码错误，避免损坏文件中的凭证片段进入日志。
		return job, fmt.Errorf("JSON 格式或字段无效")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return job, fmt.Errorf("JSON 包含多余内容")
	}
	return job, validateJob(job)
}
