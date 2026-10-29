package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"strm115/backend/internal/model"
)

const driveSTRMSchema = `
CREATE TABLE IF NOT EXISTS drive_strm_mappings(id TEXT PRIMARY KEY, record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS drive_strm_files(mapping_id TEXT NOT NULL REFERENCES drive_strm_mappings(id) ON DELETE CASCADE, file_id TEXT NOT NULL, directory INTEGER NOT NULL, record TEXT NOT NULL, PRIMARY KEY(mapping_id,file_id));
CREATE TABLE IF NOT EXISTS drive_strm_cursors(account_id TEXT PRIMARY KEY, cursor TEXT NOT NULL);`

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
	_, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS drive_strm_directories ON drive_strm_files(mapping_id,directory)`)
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
	return s.listDriveSTRMFiles(ctx, id, false)
}

func (s *Store) ListDriveSTRMDirectories(ctx context.Context, id string) ([]model.DriveSTRMFile, error) {
	return s.listDriveSTRMFiles(ctx, id, true)
}

func (s *Store) listDriveSTRMFiles(ctx context.Context, id string, directories bool) ([]model.DriveSTRMFile, error) {
	query := `SELECT record FROM drive_strm_files WHERE mapping_id=?`
	if directories {
		query += ` AND directory=1`
	}
	rows, err := s.db.QueryContext(ctx, query, id)
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
	if value.MappingID == "" || value.FileID == "" {
		return fmt.Errorf("目录 STRM 文件索引缺少身份")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO drive_strm_files(mapping_id,file_id,directory,record) VALUES(?,?,?,?) ON CONFLICT(mapping_id,file_id) DO UPDATE SET directory=excluded.directory,record=excluded.record`, value.MappingID, value.FileID, value.Directory, string(raw))
	if err != nil {
		return fmt.Errorf("保存目录 STRM 文件索引: %w", err)
	}
	return nil
}
