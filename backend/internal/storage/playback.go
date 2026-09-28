package storage

import (
	"context"
	"fmt"
)

// These records contain metadata only. Account credentials stay in the account
// store. A batch commits before the caller updates its in-memory indices.
type PlaybackMaterialRow struct {
	ID, SHA1, Record string
	Size             int64
	Origins          []string
	Ranges           []PlaybackRangeRow
}
type PlaybackRangeRow struct {
	Start, End int64
	Record     string
}
type PlaybackCopyRow struct{ ID, AccountID, SHA1, SourceKey, Record string }
type PlaybackBatch struct {
	Settings                      *string
	Materials                     []PlaybackMaterialRow
	Copies                        []PlaybackCopyRow
	DeleteMaterials, DeleteCopies []string
}

func (s *Store) LoadPlayback(ctx context.Context) (PlaybackBatch, error) {
	var result PlaybackBatch
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM playback_settings WHERE id=1`)
	if err != nil {
		return result, fmt.Errorf("读取播放设置: %w", err)
	}
	if rows.Next() {
		var record string
		if err = rows.Scan(&record); err == nil {
			result.Settings = &record
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id,sha1,size,record FROM playback_materials ORDER BY rowid`)
	if err != nil {
		return result, err
	}
	positions := make(map[string]int)
	for rows.Next() {
		var m PlaybackMaterialRow
		if err = rows.Scan(&m.ID, &m.SHA1, &m.Size, &m.Record); err != nil {
			break
		}
		positions[m.ID] = len(result.Materials)
		result.Materials = append(result.Materials, m)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT material_id,origin FROM playback_origins ORDER BY rowid`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id, origin string
		if err = rows.Scan(&id, &origin); err != nil {
			break
		}
		if i, ok := positions[id]; ok {
			result.Materials[i].Origins = append(result.Materials[i].Origins, origin)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT material_id,start,end,record FROM playback_ranges ORDER BY rowid`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id string
		var r PlaybackRangeRow
		if err = rows.Scan(&id, &r.Start, &r.End, &r.Record); err != nil {
			break
		}
		if i, ok := positions[id]; ok {
			result.Materials[i].Ranges = append(result.Materials[i].Ranges, r)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id,account_id,sha1,source_key,record FROM playback_copies ORDER BY rowid`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var e PlaybackCopyRow
		if err := rows.Scan(&e.ID, &e.AccountID, &e.SHA1, &e.SourceKey, &e.Record); err != nil {
			return result, err
		}
		result.Copies = append(result.Copies, e)
	}
	return result, rows.Err()
}

func (s *Store) SavePlayback(ctx context.Context, batch PlaybackBatch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始播放资料事务: %w", err)
	}
	defer tx.Rollback()
	if batch.Settings != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO playback_settings(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, *batch.Settings); err != nil {
			return err
		}
	}
	for _, id := range batch.DeleteMaterials {
		if _, err = tx.ExecContext(ctx, `DELETE FROM playback_materials WHERE id=?`, id); err != nil {
			return err
		}
	}
	for _, id := range batch.DeleteCopies {
		if _, err = tx.ExecContext(ctx, `DELETE FROM playback_copies WHERE id=?`, id); err != nil {
			return err
		}
	}
	for _, m := range batch.Materials {
		if _, err = tx.ExecContext(ctx, `INSERT INTO playback_materials(id,sha1,size,record) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET sha1=excluded.sha1,size=excluded.size,record=excluded.record`, m.ID, m.SHA1, m.Size, m.Record); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM playback_origins WHERE material_id=?`, m.ID); err != nil {
			return err
		}
		for _, origin := range m.Origins {
			if _, err = tx.ExecContext(ctx, `INSERT INTO playback_origins(origin,material_id) VALUES(?,?)`, origin, m.ID); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM playback_ranges WHERE material_id=?`, m.ID); err != nil {
			return err
		}
		for _, r := range m.Ranges {
			if _, err = tx.ExecContext(ctx, `INSERT INTO playback_ranges(material_id,start,end,record) VALUES(?,?,?,?)`, m.ID, r.Start, r.End, r.Record); err != nil {
				return err
			}
		}
	}
	for _, e := range batch.Copies {
		if _, err = tx.ExecContext(ctx, `INSERT INTO playback_copies(id,account_id,sha1,source_key,record) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,sha1=excluded.sha1,source_key=excluded.source_key,record=excluded.record`, e.ID, e.AccountID, e.SHA1, e.SourceKey, e.Record); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交播放资料事务: %w", err)
	}
	return nil
}
