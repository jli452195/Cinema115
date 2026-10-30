package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

const driveSTRMSchema = `
CREATE TABLE IF NOT EXISTS drive_strm_settings(id INTEGER PRIMARY KEY CHECK(id=1), record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS drive_strm_mappings(id TEXT PRIMARY KEY, record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS drive_strm_files(mapping_id TEXT NOT NULL REFERENCES drive_strm_mappings(id) ON DELETE CASCADE, file_id TEXT NOT NULL, directory INTEGER NOT NULL, record TEXT NOT NULL, PRIMARY KEY(mapping_id,file_id));
CREATE TABLE IF NOT EXISTS drive_strm_attachments(id INTEGER PRIMARY KEY AUTOINCREMENT, mapping_id TEXT NOT NULL REFERENCES drive_strm_mappings(id) ON DELETE CASCADE, file_id TEXT NOT NULL, payload TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', UNIQUE(mapping_id,file_id));
CREATE INDEX IF NOT EXISTS drive_strm_attachments_due ON drive_strm_attachments(next_attempt_at,id);
CREATE TABLE IF NOT EXISTS drive_strm_cleanup(id INTEGER PRIMARY KEY AUTOINCREMENT, mapping_id TEXT NOT NULL REFERENCES drive_strm_mappings(id) ON DELETE CASCADE, file_id TEXT NOT NULL, path TEXT NOT NULL, record TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', UNIQUE(mapping_id,file_id,path));
CREATE INDEX IF NOT EXISTS drive_strm_cleanup_due ON drive_strm_cleanup(next_attempt_at,id);
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

func (s *Store) GetDriveSTRMSettings(ctx context.Context) (model.DriveSTRMSettings, error) {
	value := model.DefaultDriveSTRMSettings()
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM drive_strm_settings WHERE id=1`).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return value, ErrNotFound
		}
		return value, fmt.Errorf("读取目录 STRM 基础设置: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, fmt.Errorf("解析目录 STRM 基础设置: %w", err)
	}
	return value, nil
}

func (s *Store) SaveDriveSTRMSettings(ctx context.Context, value model.DriveSTRMSettings) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码目录 STRM 基础设置: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO drive_strm_settings(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(raw)); err != nil {
		return fmt.Errorf("保存目录 STRM 基础设置: %w", err)
	}
	return nil
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
	value.MetadataPending, value.MetadataFailed = 0, 0
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM drive_strm_attachments WHERE mapping_id=?`, value.ID); err != nil {
			return fmt.Errorf("清除目录 STRM 附件待办: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM drive_strm_cleanup WHERE mapping_id=?`, value.ID); err != nil {
			return fmt.Errorf("清除目录 STRM 本地清理待办: %w", err)
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

const driveSTRMFileUpsert = `INSERT INTO drive_strm_files(mapping_id,file_id,directory,record) VALUES(?,?,?,?) ON CONFLICT(mapping_id,file_id) DO UPDATE SET directory=excluded.directory,record=excluded.record WHERE directory<>excluded.directory OR record<>excluded.record`

const driveSTRMCleanupConflict = ` ON CONFLICT(mapping_id,file_id,path) DO UPDATE SET id=excluded.id,record=excluded.record,attempts=0,next_attempt_at=0,last_error='' WHERE json_extract(drive_strm_cleanup.record,'$.digest')<>json_extract(excluded.record,'$.digest')`

// Register previous ownership before overwriting it, in the same transaction.
// No local output becomes obsolete until its replacement is indexed.
func queueReplacedDriveSTRMFile(ctx context.Context, tx *sql.Tx, value model.DriveSTRMFile) error {
	if value.Directory || value.Digest == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO drive_strm_cleanup(mapping_id,file_id,path,record) SELECT mapping_id,file_id,json_extract(record,'$.path'),record FROM drive_strm_files WHERE mapping_id=? AND file_id=? AND directory=0 AND COALESCE(json_extract(record,'$.digest'),'')<>'' AND json_extract(record,'$.path')<>?`+driveSTRMCleanupConflict, value.MappingID, value.FileID, value.Path)
	if err != nil {
		return fmt.Errorf("登记目录 STRM 旧输出清理: %w", err)
	}
	return nil
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
	stmt, err := tx.PrepareContext(ctx, driveSTRMFileUpsert)
	if err != nil {
		return fmt.Errorf("保存目录 STRM 文件索引: %w", err)
	}
	defer stmt.Close()
	for i, value := range values {
		if err := queueReplacedDriveSTRMFile(ctx, tx, value); err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, value.MappingID, value.FileID, value.Directory, raw[i]); err != nil {
			return fmt.Errorf("保存目录 STRM 文件索引: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目录 STRM 文件索引: %w", err)
	}
	return nil
}

// Repeated discovery retains the original settings and retry boundary. Only a
// changed source or file replaces a job and gives it a new completion token.
func (s *Store) QueueDriveSTRMAttachments(ctx context.Context, values []model.DriveSTRMAttachment) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存目录 STRM 附件待办: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO drive_strm_attachments(mapping_id,file_id,payload) VALUES(?,?,?) ON CONFLICT(mapping_id,file_id) DO UPDATE SET id=excluded.id,payload=excluded.payload,attempts=0,next_attempt_at=0,last_error='' WHERE json_remove(drive_strm_attachments.payload,'$.settings')<>json_remove(excluded.payload,'$.settings')`)
	if err != nil {
		return fmt.Errorf("准备目录 STRM 附件待办: %w", err)
	}
	defer stmt.Close()
	for _, value := range values {
		if value.Record.MappingID == "" || value.Record.FileID == "" || value.Record.Directory || value.Record.Path == "" || value.AccountID == "" || value.CredentialKey == "" || value.SourceCID == "" || value.Name == "" {
			return fmt.Errorf("目录 STRM 附件待办缺少文件或来源信息")
		}
		value.Record.Digest = ""
		payload, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("编码目录 STRM 附件待办: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, value.Record.MappingID, value.Record.FileID, string(payload)); err != nil {
			return fmt.Errorf("保存目录 STRM 附件待办: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目录 STRM 附件待办: %w", err)
	}
	return nil
}

func (s *Store) ListDriveSTRMAttachments(ctx context.Context, limit int) ([]model.DriveSTRMAttachment, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("目录 STRM 附件读取数量应为 1 到 1000")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.payload,a.attempts,a.next_attempt_at,a.last_error FROM drive_strm_attachments a JOIN drive_strm_mappings m ON m.id=a.mapping_id WHERE COALESCE(json_extract(m.record,'$.status'),'') NOT IN ('running','stopped') AND COALESCE(json_extract(m.record,'$.queued'),0)=0 ORDER BY a.next_attempt_at,a.id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询目录 STRM 附件待办: %w", err)
	}
	defer rows.Close()
	values := make([]model.DriveSTRMAttachment, 0, limit)
	for rows.Next() {
		var value model.DriveSTRMAttachment
		var payload string
		var next int64
		if err := rows.Scan(&value.ID, &payload, &value.Attempts, &next, &value.LastError); err != nil {
			return nil, fmt.Errorf("读取目录 STRM 附件待办: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			return nil, fmt.Errorf("解析目录 STRM 附件待办: %w", err)
		}
		if next > 0 {
			value.NextAttemptAt = time.UnixMilli(next).UTC()
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) RetryDriveSTRMAttachment(ctx context.Context, job model.DriveSTRMAttachment, reason string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drive_strm_attachments SET attempts=attempts+1,next_attempt_at=?,last_error=? WHERE id=? AND mapping_id=? AND file_id=?`, next.UnixMilli(), reason, job.ID, job.Record.MappingID, job.Record.FileID)
	if err != nil {
		return fmt.Errorf("延后目录 STRM 附件重试: %w", err)
	}
	return nil
}

// Complete the queue token and file ownership together. A stale worker cannot
// acknowledge a newer job or replace its output index.
func (s *Store) CompleteDriveSTRMAttachment(ctx context.Context, job model.DriveSTRMAttachment, record model.DriveSTRMFile) error {
	if record.MappingID != job.Record.MappingID || record.FileID != job.Record.FileID || record.Path != job.Record.Path || record.Signature != job.Record.Signature || record.Directory || record.Digest == "" {
		return fmt.Errorf("目录 STRM 附件完成信息与待办不一致")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("编码目录 STRM 附件索引: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始完成目录 STRM 附件: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM drive_strm_attachments WHERE id=? AND mapping_id=? AND file_id=?`, job.ID, job.Record.MappingID, job.Record.FileID)
	if err != nil {
		return fmt.Errorf("确认目录 STRM 附件完成: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取目录 STRM 附件完成结果: %w", err)
	}
	if deleted == 0 {
		return nil
	}
	if err := queueReplacedDriveSTRMFile(ctx, tx, record); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, driveSTRMFileUpsert, record.MappingID, record.FileID, false, string(payload)); err != nil {
		return fmt.Errorf("保存目录 STRM 附件索引: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目录 STRM 附件完成结果: %w", err)
	}
	return nil
}

func (s *Store) DeleteDriveSTRMAttachment(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drive_strm_attachments WHERE id=?`, id); err != nil {
		return fmt.Errorf("删除目录 STRM 附件待办: %w", err)
	}
	return nil
}

func (s *Store) DeleteDriveSTRMAttachments(ctx context.Context, mappingID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drive_strm_attachments WHERE mapping_id=?`, mappingID); err != nil {
		return fmt.Errorf("清除目录 STRM 附件待办: %w", err)
	}
	return nil
}

// One grouped query supplies the list page without querying each mapping.
func (s *Store) DriveSTRMAttachmentCounts(ctx context.Context) (map[string][2]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mapping_id,count(*),sum(CASE WHEN attempts>0 THEN 1 ELSE 0 END) FROM drive_strm_attachments GROUP BY mapping_id`)
	if err != nil {
		return nil, fmt.Errorf("统计目录 STRM 附件待办: %w", err)
	}
	defer rows.Close()
	values := make(map[string][2]int)
	for rows.Next() {
		var id string
		var counts [2]int
		if err := rows.Scan(&id, &counts[0], &counts[1]); err != nil {
			return nil, fmt.Errorf("读取目录 STRM 附件统计: %w", err)
		}
		values[id] = counts
	}
	return values, rows.Err()
}

// QueueDriveSTRMCleanup accepts locally verified ownership whose index was
// already replaced. Repeated evidence preserves its retry deadline and token.
func (s *Store) QueueDriveSTRMCleanup(ctx context.Context, values []model.DriveSTRMFile) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存目录 STRM 本地清理待办: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO drive_strm_cleanup(mapping_id,file_id,path,record) VALUES(?,?,?,?)`+driveSTRMCleanupConflict)
	if err != nil {
		return fmt.Errorf("准备目录 STRM 本地清理待办: %w", err)
	}
	defer stmt.Close()
	for _, value := range values {
		if value.MappingID == "" || value.FileID == "" || value.Path == "" || value.Digest == "" || value.Directory {
			return fmt.Errorf("目录 STRM 本地清理待办缺少文件归属证据")
		}
		record, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("编码目录 STRM 本地清理待办: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, value.MappingID, value.FileID, value.Path, string(record)); err != nil {
			return fmt.Errorf("保存目录 STRM 本地清理待办: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目录 STRM 本地清理待办: %w", err)
	}
	return nil
}

func (s *Store) ListDriveSTRMCleanup(ctx context.Context, limit int) ([]model.DriveSTRMCleanup, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("目录 STRM 本地清理读取数量应为 1 到 1000")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.record,c.attempts,c.next_attempt_at,c.last_error FROM drive_strm_cleanup c JOIN drive_strm_mappings m ON m.id=c.mapping_id WHERE c.next_attempt_at<=? AND COALESCE(json_extract(m.record,'$.status'),'') NOT IN ('running','stopped') AND COALESCE(json_extract(m.record,'$.queued'),0)=0 ORDER BY c.next_attempt_at,c.id LIMIT ?`, time.Now().UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("查询目录 STRM 本地清理待办: %w", err)
	}
	defer rows.Close()
	values := make([]model.DriveSTRMCleanup, 0, limit)
	for rows.Next() {
		var value model.DriveSTRMCleanup
		var record string
		var next int64
		if err := rows.Scan(&value.ID, &record, &value.Attempts, &next, &value.LastError); err != nil {
			return nil, fmt.Errorf("读取目录 STRM 本地清理待办: %w", err)
		}
		if err := json.Unmarshal([]byte(record), &value.Record); err != nil {
			return nil, fmt.Errorf("解析目录 STRM 本地清理待办: %w", err)
		}
		if next > 0 {
			value.NextAttemptAt = time.UnixMilli(next).UTC()
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) RetryDriveSTRMCleanup(ctx context.Context, job model.DriveSTRMCleanup, reason string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drive_strm_cleanup SET attempts=attempts+1,next_attempt_at=?,last_error=? WHERE id=? AND mapping_id=? AND file_id=? AND path=?`, next.UnixMilli(), reason, job.ID, job.Record.MappingID, job.Record.FileID, job.Record.Path)
	if err != nil {
		return fmt.Errorf("延后目录 STRM 本地清理重试: %w", err)
	}
	return nil
}

func (s *Store) DeleteDriveSTRMCleanup(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drive_strm_cleanup WHERE id=?`, id); err != nil {
		return fmt.Errorf("删除目录 STRM 本地清理待办: %w", err)
	}
	return nil
}
