package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const mediaOrganizeSchema = `
CREATE TABLE IF NOT EXISTS media_organize_settings(id INTEGER PRIMARY KEY CHECK(id=1),record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS media_organize_runs(id TEXT PRIMARY KEY,rule_id TEXT NOT NULL,status TEXT NOT NULL,at TEXT NOT NULL,record TEXT NOT NULL,activity TEXT NOT NULL,cleanup TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS media_organize_run_rule ON media_organize_runs(rule_id,at DESC);
CREATE TABLE IF NOT EXISTS media_organize_tasks(run_id TEXT NOT NULL REFERENCES media_organize_runs(id) ON DELETE CASCADE,file_id TEXT NOT NULL,status_group TEXT NOT NULL,at TEXT NOT NULL,search TEXT NOT NULL,record TEXT NOT NULL,PRIMARY KEY(run_id,file_id));
CREATE INDEX IF NOT EXISTS media_organize_task_order ON media_organize_tasks(at DESC,run_id,file_id);
CREATE INDEX IF NOT EXISTS media_organize_task_status ON media_organize_tasks(status_group,at DESC,run_id,file_id);
`

type MediaOrganizeTaskRow struct {
	FileID string `json:"fileId"`
	Group  string `json:"group"`
	At     string `json:"at"`
	Search string `json:"search"`
	Record string `json:"record"`
}

type MediaOrganizeLiveTaskRow struct {
	RunID string `json:"runId"`
	MediaOrganizeTaskRow
}
type MediaOrganizeRunRow struct {
	ID, RuleID, Status, At, Record, Activity, Cleanup string
	Tasks                                             []MediaOrganizeTaskRow
}
type MediaOrganizeRows struct {
	Rules       string
	Runs        []string
	Initialized bool
}
type MediaOrganizeQuery struct {
	Records            []string
	Counts             map[string]int
	Page, Pages, Total int
	Activity, Cleanup  []string
}

func (s *Store) LoadMediaOrganize(ctx context.Context) (MediaOrganizeRows, error) {
	out := MediaOrganizeRows{Runs: []string{}}
	err := s.db.QueryRowContext(ctx, `SELECT record FROM media_organize_settings WHERE id=1`).Scan(&out.Rules)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("读取整理规则: %w", err)
	}
	out.Initialized = true
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM media_organize_runs ORDER BY at DESC,id`)
	if err != nil {
		return out, fmt.Errorf("读取整理任务: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, raw)
	}
	return out, rows.Err()
}

// Only the changed runs are encoded by the caller. Rules and their run updates
// commit together, so a failed checkpoint never exposes a newer search index.
func (s *Store) SaveMediaOrganize(ctx context.Context, rules string, updates []MediaOrganizeRunRow, removed []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存整理记录: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO media_organize_settings(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record WHERE media_organize_settings.record!=excluded.record`, rules); err != nil {
		return err
	}
	for _, id := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM media_organize_runs WHERE id=?`, id); err != nil {
			return err
		}
	}
	for _, run := range updates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_organize_runs(id,rule_id,status,at,record,activity,cleanup) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET rule_id=excluded.rule_id,status=excluded.status,at=excluded.at,record=excluded.record,activity=excluded.activity,cleanup=CASE WHEN excluded.cleanup='' THEN media_organize_runs.cleanup ELSE excluded.cleanup END WHERE media_organize_runs.rule_id!=excluded.rule_id OR media_organize_runs.status!=excluded.status OR media_organize_runs.at!=excluded.at OR media_organize_runs.record!=excluded.record OR media_organize_runs.activity!=excluded.activity OR (excluded.cleanup!='' AND media_organize_runs.cleanup!=excluded.cleanup)`, run.ID, run.RuleID, run.Status, run.At, run.Record, run.Activity, run.Cleanup); err != nil {
			return err
		}
		kept := make(map[string]bool, len(run.Tasks))
		for _, task := range run.Tasks {
			kept[task.FileID] = true
		}
		rows, err := tx.QueryContext(ctx, `SELECT file_id FROM media_organize_tasks WHERE run_id=?`, run.ID)
		if err != nil {
			return err
		}
		removed := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !kept[id] {
				removed = append(removed, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range removed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM media_organize_tasks WHERE run_id=? AND file_id=?`, run.ID, id); err != nil {
				return err
			}
		}
		for _, task := range run.Tasks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO media_organize_tasks(run_id,file_id,status_group,at,search,record) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,file_id) DO UPDATE SET status_group=excluded.status_group,at=excluded.at,search=excluded.search,record=excluded.record WHERE media_organize_tasks.record!=excluded.record OR media_organize_tasks.status_group!=excluded.status_group OR media_organize_tasks.at!=excluded.at OR media_organize_tasks.search!=excluded.search`, run.ID, task.FileID, task.Group, task.At, task.Search, task.Record); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交整理记录: %w", err)
	}
	return nil
}

func (s *Store) QueryMediaOrganize(ctx context.Context, query, status string, page int) (MediaOrganizeQuery, error) {
	return s.QueryMediaOrganizeLive(ctx, query, status, page, nil)
}

// 运行中及成功摘要仅参与本次只读查询，不写入任务历史表。
func (s *Store) QueryMediaOrganizeLive(ctx context.Context, query, status string, page int, runtime []MediaOrganizeLiveTaskRow) (MediaOrganizeQuery, error) {
	out := MediaOrganizeQuery{Records: []string{}, Activity: []string{}, Cleanup: []string{}, Counts: map[string]int{"all": 0, "running": 0, "done": 0, "failed": 0}}
	query = strings.ToLower(query)
	raw, err := json.Marshal(runtime)
	if err != nil {
		return out, fmt.Errorf("编码整理运行摘要: %w", err)
	}
	// 同一份运行摘要只展开一次，文本键允许 SQLite 为历史覆盖检查建立临时索引。
	const liveTasks = `WITH runtime AS MATERIALIZED (
		SELECT CAST(json_extract(value,'$.runId') AS TEXT) AS run_id,CAST(json_extract(value,'$.fileId') AS TEXT) AS file_id,
		json_extract(value,'$.group') AS status_group,json_extract(value,'$.at') AS at,
		json_extract(value,'$.search') AS search,json_extract(value,'$.record') AS record
		FROM json_each(?) WHERE json_extract(value,'$.group') IN ('running','done')
	),tasks AS (
		SELECT run_id,file_id,status_group,at,search,record FROM media_organize_tasks AS saved
		WHERE NOT EXISTS (SELECT 1 FROM runtime WHERE runtime.run_id=saved.run_id AND runtime.file_id=saved.file_id)
		UNION ALL SELECT run_id,file_id,status_group,at,search,record FROM runtime
	) `
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, liveTasks+`SELECT status_group,COUNT(*) FROM tasks WHERE instr(search,?)>0 GROUP BY status_group`, string(raw), query)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var group string
		var count int
		if err := rows.Scan(&group, &count); err != nil {
			rows.Close()
			return out, err
		}
		out.Counts[group] = count
		out.Counts["all"] += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Total = out.Counts[status]
	out.Pages = max(1, (out.Total+19)/20)
	out.Page = max(1, min(page, out.Pages))
	rows, err = tx.QueryContext(ctx, liveTasks+`SELECT record FROM tasks WHERE instr(search,?)>0 AND (?='all' OR status_group=?) ORDER BY at DESC,run_id,file_id LIMIT 20 OFFSET ?`, string(raw), query, status, status, (out.Page-1)*20)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return out, err
		}
		out.Records = append(out.Records, raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, entry := range []struct {
		sql string
		dst *[]string
	}{
		{`SELECT activity FROM (SELECT activity,at,id,ROW_NUMBER() OVER(PARTITION BY rule_id ORDER BY at DESC,id) n FROM media_organize_runs) WHERE n=1 AND activity!='' ORDER BY at DESC,id LIMIT 20`, &out.Activity},
		{`SELECT cleanup FROM media_organize_runs WHERE cleanup!='' ORDER BY at DESC,id LIMIT 20`, &out.Cleanup},
	} {
		rows, err = tx.QueryContext(ctx, entry.sql)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return out, err
			}
			*entry.dst = append(*entry.dst, raw)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}
