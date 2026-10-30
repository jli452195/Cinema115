package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"strm115/backend/internal/model"
)

const driveSTRMSchema = `
CREATE TABLE IF NOT EXISTS drive_strm_mappings(id TEXT PRIMARY KEY, record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS drive_strm_files(mapping_id TEXT NOT NULL REFERENCES drive_strm_mappings(id) ON DELETE CASCADE, file_id TEXT NOT NULL, directory INTEGER NOT NULL, record TEXT NOT NULL, PRIMARY KEY(mapping_id,file_id));
CREATE TABLE IF NOT EXISTS drive_strm_cursors(account_id TEXT PRIMARY KEY, cursor TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS drive_strm_exports(export_id TEXT PRIMARY KEY, account_id TEXT NOT NULL);`

func initializeDriveSTRMSchema(ctx context.Context, tx *sql.Tx, version int) error {
	if _, err := tx.ExecContext(ctx, driveSTRMSchema); err != nil {
		return err
	}
	if version < 64 {
		// CREATE TABLE IF NOT EXISTS leaves an existing development table
		// untouched. Add and backfill the column before creating its index.
		var hasDirectory bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('drive_strm_files') WHERE name='directory')`).Scan(&hasDirectory); err != nil {
			return fmt.Errorf("检查目录标记列: %w", err)
		}
		if !hasDirectory {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE drive_strm_files ADD COLUMN directory INTEGER NOT NULL DEFAULT 0;
UPDATE drive_strm_files SET directory=CASE WHEN json_extract(record,'$.directory')=1 THEN 1 ELSE 0 END;`); err != nil {
				return fmt.Errorf("补全目录标记列: %w", err)
			}
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS drive_strm_directories ON drive_strm_files(mapping_id,directory);
CREATE INDEX IF NOT EXISTS drive_strm_paths ON drive_strm_files(mapping_id,json_extract(record,'$.path') COLLATE NOCASE);`)
	return err
}

func (s *Store) LoadDriveSTRMCursor(ctx context.Context, accountID string) (string, error) {
	var cursor string
	err := s.db.QueryRowContext(ctx, `SELECT cursor FROM drive_strm_cursors WHERE account_id=?`, accountID).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取目录 STRM 事件游标: %w", err)
	}
	return cursor, nil
}

func (s *Store) SaveDriveSTRMCursor(ctx context.Context, accountID, cursor string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO drive_strm_cursors(account_id,cursor) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET cursor=excluded.cursor`, accountID, cursor)
	if err != nil {
		return fmt.Errorf("保存目录 STRM 事件游标: %w", err)
	}
	return nil
}

// Export cleanup identities outlive individual mappings. They contain no
// credentials, and cannot be reassigned to a different account on replay.
func (s *Store) SaveDriveSTRMExport(ctx context.Context, accountID, exportID string) error {
	if accountID == "" || exportID == "" {
		return fmt.Errorf("目录 STRM 导出清理记录缺少身份")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO drive_strm_exports(export_id,account_id) VALUES(?,?) ON CONFLICT(export_id) DO NOTHING`, exportID, accountID)
	if err != nil {
		return fmt.Errorf("保存目录 STRM 导出清理记录: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取目录 STRM 导出清理保存结果: %w", err)
	}
	if created > 0 {
		return nil
	}
	var owner string
	if err := s.db.QueryRowContext(ctx, `SELECT account_id FROM drive_strm_exports WHERE export_id=?`, exportID).Scan(&owner); err != nil {
		return fmt.Errorf("读取目录 STRM 导出归属: %w", err)
	}
	if owner != accountID {
		return fmt.Errorf("目录 STRM 导出清理记录已属于其他账号")
	}
	return nil
}

// ListDriveSTRMExports returns export ID -> pinned account configuration ID.
func (s *Store) ListDriveSTRMExports(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT export_id,account_id FROM drive_strm_exports`)
	if err != nil {
		return nil, fmt.Errorf("查询目录 STRM 导出清理记录: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var exportID, accountID string
		if err := rows.Scan(&exportID, &accountID); err != nil {
			return nil, fmt.Errorf("读取目录 STRM 导出清理记录: %w", err)
		}
		out[exportID] = accountID
	}
	return out, rows.Err()
}

func (s *Store) DeleteDriveSTRMExport(ctx context.Context, exportID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drive_strm_exports WHERE export_id=?`, exportID); err != nil {
		return fmt.Errorf("删除目录 STRM 导出清理记录: %w", err)
	}
	return nil
}

func (s *Store) SaveDriveSTRM(ctx context.Context, value model.DriveSTRM) error {
	return s.SaveDriveSTRMConfiguration(ctx, value, false)
}

// SaveDriveSTRMConfiguration resets ownership together with changed roots.
// Previous local outputs deliberately remain on disk and become protected.
func (s *Store) SaveDriveSTRMConfiguration(ctx context.Context, value model.DriveSTRM, reset bool) error {
	if value.ID == "" || value.AccountID == "" || value.SourceCID == "" || value.UpdatedAt.IsZero() {
		return fmt.Errorf("目录 STRM 缺少必要字段")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码目录 STRM: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存目录 STRM: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO drive_strm_mappings(id,record) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, value.ID, string(raw))
	if err != nil {
		return fmt.Errorf("保存目录 STRM: %w", err)
	}
	if reset {
		if _, err := tx.ExecContext(ctx, `DELETE FROM drive_strm_files WHERE mapping_id=?`, value.ID); err != nil {
			return fmt.Errorf("重置目录 STRM 输出归属: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) GetDriveSTRM(ctx context.Context, id string) (model.DriveSTRM, error) {
	var out model.DriveSTRM
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM drive_strm_mappings WHERE id=?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, fmt.Errorf("读取目录 STRM: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, fmt.Errorf("解析目录 STRM: %w", err)
	}
	return out, nil
}

func (s *Store) ListDriveSTRM(ctx context.Context) ([]model.DriveSTRM, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM drive_strm_mappings ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询目录 STRM: %w", err)
	}
	defer rows.Close()
	out := make([]model.DriveSTRM, 0)
	for rows.Next() {
		var raw string
		var value model.DriveSTRM
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("解析目录 STRM: %w", err)
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDriveSTRM(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM drive_strm_mappings WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("删除目录 STRM: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListDriveSTRMFiles(ctx context.Context, id string) ([]model.DriveSTRMFile, error) {
	return s.listDriveSTRMFiles(ctx, `mapping_id=?`, id)
}

func (s *Store) ListDriveSTRMDirectories(ctx context.Context, id string) ([]model.DriveSTRMFile, error) {
	return s.listDriveSTRMFiles(ctx, `mapping_id=? AND directory=1`, id)
}

func (s *Store) ListDriveSTRMFilesByIDs(ctx context.Context, mappingID string, ids []string) ([]model.DriveSTRMFile, error) {
	return s.listDriveSTRMFilesBy(ctx, mappingID, `file_id`, ids)
}

// Paths are local output paths. Return every owner of a matched path so the
// caller can reject ambiguity instead of silently choosing another file ID.
// NOCASE follows SQLite's ASCII case folding; non-ASCII names match exactly.
func (s *Store) ListDriveSTRMFilesByPaths(ctx context.Context, mappingID string, paths []string) ([]model.DriveSTRMFile, error) {
	return s.listDriveSTRMFilesBy(ctx, mappingID, `json_extract(record,'$.path') COLLATE NOCASE`, paths)
}

func (s *Store) listDriveSTRMFilesBy(ctx context.Context, mappingID, column string, keys []string) ([]model.DriveSTRMFile, error) {
	out := make([]model.DriveSTRMFile, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	// Leave room for the mapping parameter on SQLite builds with a smaller
	// variable limit. The indexed query reads only this upstream page's owners.
	for offset := 0; offset < len(keys); offset += 500 {
		end := min(offset+500, len(keys))
		args := make([]any, 1, end-offset+1)
		args[0] = mappingID
		for _, key := range keys[offset:end] {
			args = append(args, key)
		}
		where := `mapping_id=? AND ` + column + ` IN (` + strings.TrimSuffix(strings.Repeat("?,", end-offset), ",") + `)`
		files, err := s.listDriveSTRMFiles(ctx, where, args...)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if !seen[file.FileID] {
				seen[file.FileID] = true
				out = append(out, file)
			}
		}
	}
	return out, nil
}

func (s *Store) listDriveSTRMFiles(ctx context.Context, where string, args ...any) ([]model.DriveSTRMFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM drive_strm_files WHERE `+where+` ORDER BY file_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询目录 STRM 文件索引: %w", err)
	}
	defer rows.Close()
	out := make([]model.DriveSTRMFile, 0)
	for rows.Next() {
		var raw string
		var value model.DriveSTRMFile
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("解析目录 STRM 文件索引: %w", err)
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) SaveDriveSTRMFile(ctx context.Context, value model.DriveSTRMFile) error {
	return s.SaveDriveSTRMFiles(ctx, []model.DriveSTRMFile{value})
}

// SaveDriveSTRMFiles commits one page of ownership records together. Replaying
// identical records does not dirty SQLite pages or rewrite the output index.
func (s *Store) SaveDriveSTRMFiles(ctx context.Context, values []model.DriveSTRMFile) error {
	if len(values) == 0 {
		return nil
	}
	raw := make([]string, len(values))
	for i, value := range values {
		if value.MappingID == "" || value.FileID == "" {
			return fmt.Errorf("目录 STRM 文件索引缺少身份")
		}
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("编码目录 STRM 文件索引: %w", err)
		}
		raw[i] = string(data)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存目录 STRM 文件索引: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO drive_strm_files(mapping_id,file_id,directory,record) VALUES(?,?,?,?) ON CONFLICT(mapping_id,file_id) DO UPDATE SET directory=excluded.directory,record=excluded.record WHERE directory<>excluded.directory OR record<>excluded.record`)
	if err != nil {
		return fmt.Errorf("保存目录 STRM 文件索引: %w", err)
	}
	defer stmt.Close()
	for i, value := range values {
		if _, err := stmt.ExecContext(ctx, value.MappingID, value.FileID, value.Directory, raw[i]); err != nil {
			return fmt.Errorf("保存目录 STRM 文件索引: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目录 STRM 文件索引: %w", err)
	}
	return nil
}
