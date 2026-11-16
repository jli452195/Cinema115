package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// Stats 返回当前进程连接池的累计指标，不执行数据库查询。
func (s *Store) Stats() sql.DBStats { return s.db.Stats() }

// Optimize 限制统计采样量，补充或更新查询规划统计，不整理文件或改变业务数据。
func (s *Store) Optimize(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取数据库维护连接: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA analysis_limit=400"); err != nil {
		return fmt.Errorf("设置数据库统计采样上限: %w", err)
	}
	// 检查所有表的统计需求，覆盖启动后尚未查询过的索引。
	if _, err := conn.ExecContext(ctx, "PRAGMA optimize=0x10002"); err != nil {
		return fmt.Errorf("维护数据库查询统计: %w", err)
	}
	return nil
}

type Status struct {
	SchemaVersion  int            `json:"schemaVersion"`
	Jobs           int            `json:"jobs"`
	ByStage        map[string]int `json:"byStage"`
	LegacyImported bool           `json:"legacyImported"`
}

// Status 只返回统计数据，供运维检查，不输出分享地址或凭证。
func (s *Store) Status(ctx context.Context) (Status, error) {
	result := Status{ByStage: make(map[string]int)}
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&result.SchemaVersion); err != nil {
		return result, fmt.Errorf("读取数据库版本: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM data_migrations WHERE name=?)", legacyMigration).Scan(&result.LegacyImported); err != nil {
		return result, fmt.Errorf("读取迁移状态: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT status, count(*) FROM reshare_jobs GROUP BY status")
	if err != nil {
		return result, fmt.Errorf("统计转存任务: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var stage string
		var count int
		if err := rows.Scan(&stage, &count); err != nil {
			return result, fmt.Errorf("读取任务统计: %w", err)
		}
		result.ByStage[stage] = count
		result.Jobs += count
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("读取任务统计: %w", err)
	}
	return result, nil
}

func (s *Store) Check(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("检查数据库完整性: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("数据库完整性检查未通过")
	}
	return nil
}

// Backup 使用 SQLite 一致性快照，包含尚在 WAL 中的已提交数据。
// 目标必须不存在，防止覆盖原数据库或已有备份。
func (s *Store) Backup(ctx context.Context, destination string) error {
	abs, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("解析备份路径: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return fmt.Errorf("创建备份目录: %w", err)
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("创建备份文件（目标必须不存在）: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭备份文件: %w", err)
	}
	// VACUUM INTO 接受一个空文件作为目标，参数绑定避免路径中的特殊字符改变 SQL。
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", abs); err != nil {
		_ = os.Remove(abs)
		return fmt.Errorf("备份数据库: %w", err)
	}
	backup, err := os.OpenFile(abs, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("打开备份以同步: %w", err)
	}
	defer backup.Close()
	if err := backup.Sync(); err != nil {
		return fmt.Errorf("同步数据库备份: %w", err)
	}
	return nil
}
