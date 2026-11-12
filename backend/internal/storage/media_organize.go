package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const mediaOrganizeSchema = `
CREATE TABLE IF NOT EXISTS media_organize_settings(id INTEGER PRIMARY KEY CHECK(id=1),record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS media_organize_runs(id TEXT PRIMARY KEY,rule_id TEXT NOT NULL,at TEXT NOT NULL,record TEXT NOT NULL,activity TEXT NOT NULL,cleanup TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS media_organize_run_rule ON media_organize_runs(rule_id,at DESC);
CREATE INDEX IF NOT EXISTS media_organize_run_state ON media_organize_runs(CASE WHEN json_valid(record) THEN json_extract(record,'$.status') END);
CREATE TABLE IF NOT EXISTS media_organize_tasks(run_id TEXT NOT NULL REFERENCES media_organize_runs(id) ON DELETE CASCADE,file_id TEXT NOT NULL,status_group TEXT NOT NULL,at TEXT NOT NULL,search TEXT NOT NULL,record TEXT NOT NULL,PRIMARY KEY(run_id,file_id));
CREATE INDEX IF NOT EXISTS media_organize_task_order ON media_organize_tasks(at DESC,run_id,file_id);
CREATE INDEX IF NOT EXISTS media_organize_task_status ON media_organize_tasks(status_group,at DESC,run_id,file_id);
CREATE INDEX IF NOT EXISTS media_organize_task_file ON media_organize_tasks(file_id,status_group,run_id);
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
	ID, RuleID, At, Record, Activity, Cleanup string
	Tasks                                     []MediaOrganizeTaskRow
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

func initializeMediaOrganizeSchema(ctx context.Context, tx *sql.Tx, version int) error {
	if _, err := tx.ExecContext(ctx, mediaOrganizeSchema); err != nil {
		return err
	}
	if version < 66 {
		// 运行状态以记录内的完成证据为准，移除开发数据库中未使用的重复列。
		var hasStatus bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('media_organize_runs') WHERE name='status')`).Scan(&hasStatus); err != nil {
			return fmt.Errorf("检查整理运行状态列: %w", err)
		}
		if hasStatus {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE media_organize_runs DROP COLUMN status`); err != nil {
				return fmt.Errorf("移除重复整理运行状态列: %w", err)
			}
		}
	}
	return nil
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
	// 已结束历史只留在数据库，启动按状态索引恢复尚未结束的工作。
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM media_organize_runs WHERE CASE WHEN json_valid(record) THEN json_extract(record,'$.status') END IN ('queued','scanning','running') ORDER BY at DESC,id`)
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

// 按主键、规则或任务分组读取历史，仅供详情、恢复及明确的记录管理操作使用。
func (s *Store) LoadMediaOrganizeRuns(ctx context.Context, ids []string, ruleID, status string) ([]string, error) {
	where := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if len(ids) > 0 {
		raw, err := json.Marshal(ids)
		if err != nil {
			return nil, err
		}
		where = append(where, `id IN (SELECT value FROM json_each(?))`)
		args = append(args, string(raw))
	}
	if ruleID != "" {
		where = append(where, `rule_id=?`)
		args = append(args, ruleID)
	}
	switch status {
	case "":
	case "done", "failed":
		where = append(where, `id IN (SELECT run_id FROM media_organize_tasks WHERE status_group=?)`)
		args = append(args, status)
	case "empty":
		where = append(where, `NOT EXISTS (SELECT 1 FROM media_organize_tasks WHERE run_id=media_organize_runs.id)`)
	case "pending":
		where = append(where, `id IN (SELECT run_id FROM media_organize_tasks WHERE status_group='failed') AND EXISTS (SELECT 1 FROM json_each(record,'$.entries') WHERE json_extract(value,'$.file.mediaType')='video' AND json_extract(value,'$.status')!='done' AND json_type(value,'$.p115Move')='object')`)
	default:
		return nil, fmt.Errorf("读取整理历史: 未知分组 %q", status)
	}
	query := `SELECT record FROM media_organize_runs`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	return s.mediaOrganizeRecords(ctx, query+` ORDER BY at DESC,id`, args...)
}

// 成功检查点只读取同一文件的旧失败，再由业务层核对冻结来源及文件身份。
func (s *Store) LoadMediaOrganizeFailures(ctx context.Context, ruleID string, fileIDs []string) ([]string, error) {
	if len(fileIDs) == 0 {
		return []string{}, ctx.Err()
	}
	raw, err := json.Marshal(fileIDs)
	if err != nil {
		return nil, err
	}
	return s.mediaOrganizeRecords(ctx, `SELECT record FROM media_organize_runs WHERE rule_id=? AND id IN (SELECT run_id FROM media_organize_tasks WHERE status_group='failed' AND file_id IN (SELECT value FROM json_each(?))) ORDER BY at DESC,id`, ruleID, string(raw))
}

func (s *Store) mediaOrganizeRecords(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("读取整理历史: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}

// 详情只投影所选视频及关联附件，避免几千条同批历史为单张卡片重复解码。
func (s *Store) LoadMediaOrganizeTask(ctx context.Context, id, fileID string) (string, error) {
	const query = `WITH selected AS (SELECT record FROM media_organize_runs WHERE id=?),
	entries AS MATERIALIZED (SELECT value FROM selected,json_each(record,'$.entries') WHERE CAST(json_extract(value,'$.file.id') AS TEXT)=? OR CAST(json_extract(value,'$.videoId') AS TEXT)=?)
	SELECT json_set(record,'$.entries',json((SELECT json_group_array(json(value)) FROM entries)),
	'$.groups',json((SELECT json_group_array(json(value)) FROM json_each(record,'$.groups') WHERE json_extract(value,'$.id') IN (SELECT json_extract(value,'$.group') FROM entries))))
	FROM selected WHERE EXISTS (SELECT 1 FROM entries WHERE json_extract(value,'$.file.mediaType')='video' AND CAST(json_extract(value,'$.file.id') AS TEXT)=?)`
	var raw string
	err := s.db.QueryRowContext(ctx, query, id, fileID, fileID, fileID).Scan(&raw)
	return raw, err
}

// 附件关联只读取当前目录范围需要的成功视频，完整冻结规则继续交给业务层核对。
func (s *Store) LoadMediaOrganizeReferences(ctx context.Context, ruleID string, parents, scopes []string, recursive bool) ([]string, error) {
	parentJSON, err := json.Marshal(parents)
	if err != nil {
		return nil, err
	}
	scopeJSON, err := json.Marshal(scopes)
	if err != nil {
		return nil, err
	}
	const query = `WITH selected AS MATERIALIZED (
	SELECT r.id AS run_id,e.value AS entry FROM media_organize_runs r,json_each(r.record,'$.entries') e
	WHERE r.rule_id=? AND json_extract(e.value,'$.file.mediaType')='video' AND json_extract(e.value,'$.status')='done' AND COALESCE(json_extract(e.value,'$.relative'),'')!=''
	AND (? OR CAST(json_extract(e.value,'$.file.parentId') AS TEXT) IN (SELECT value FROM json_each(?))
	OR EXISTS (SELECT 1 FROM json_each(?) scope WHERE scope.value IN ('','.') OR json_extract(e.value,'$.file.sourcePath')=scope.value OR substr(json_extract(e.value,'$.file.sourcePath'),1,length(scope.value)+1)=scope.value||'/')))
	SELECT json_set(r.record,'$.entries',json((SELECT json_group_array(json(entry)) FROM selected WHERE run_id=r.id)),
	'$.groups',json((SELECT json_group_array(json(value)) FROM json_each(r.record,'$.groups') WHERE json_extract(value,'$.id') IN (SELECT json_extract(entry,'$.group') FROM selected WHERE run_id=r.id))))
	FROM media_organize_runs r WHERE r.id IN (SELECT run_id FROM selected) ORDER BY r.at DESC,r.id`
	return s.mediaOrganizeRecords(ctx, query, ruleID, recursive, string(parentJSON), string(scopeJSON))
}

// 调用方仅编码变化的批次；规则、完整记录和任务索引在同一事务内提交。
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_organize_runs(id,rule_id,at,record,activity,cleanup) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET rule_id=excluded.rule_id,at=excluded.at,record=excluded.record,activity=excluded.activity,cleanup=CASE WHEN excluded.cleanup='' THEN media_organize_runs.cleanup ELSE excluded.cleanup END WHERE media_organize_runs.rule_id!=excluded.rule_id OR media_organize_runs.at!=excluded.at OR media_organize_runs.record!=excluded.record OR media_organize_runs.activity!=excluded.activity OR (excluded.cleanup!='' AND media_organize_runs.cleanup!=excluded.cleanup)`, run.ID, run.RuleID, run.At, run.Record, run.Activity, run.Cleanup); err != nil {
			return err
		}
		var tasks strings.Builder
		if err := json.NewEncoder(&tasks).Encode(run.Tasks); err != nil {
			return fmt.Errorf("编码整理任务索引: %w", err)
		}
		taskJSON := tasks.String()
		if _, err := tx.ExecContext(ctx, `DELETE FROM media_organize_tasks WHERE run_id=? AND file_id NOT IN (SELECT json_extract(value,'$.fileId') FROM json_each(?) WHERE type='object')`, run.ID, taskJSON); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_organize_tasks(run_id,file_id,status_group,at,search,record)
			SELECT ?,json_extract(value,'$.fileId'),json_extract(value,'$.group'),json_extract(value,'$.at'),json_extract(value,'$.search'),json_extract(value,'$.record') FROM json_each(?) WHERE type='object'
			ON CONFLICT(run_id,file_id) DO UPDATE SET status_group=excluded.status_group,at=excluded.at,search=excluded.search,record=excluded.record WHERE media_organize_tasks.record!=excluded.record OR media_organize_tasks.status_group!=excluded.status_group OR media_organize_tasks.at!=excluded.at OR media_organize_tasks.search!=excluded.search`, run.ID, taskJSON); err != nil {
			return err
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
	// 没有运行任务时直接使用历史索引，避免无意义的 JSON 展开及合并排序。
	prefix, table, where := "", "media_organize_tasks", ""
	args := make([]any, 0, 4)
	if len(runtime) > 0 {
		raw, err := json.Marshal(runtime)
		if err != nil {
			return out, fmt.Errorf("编码整理运行摘要: %w", err)
		}
		prefix, table = liveTasks, "tasks"
		args = append(args, string(raw))
	}
	if query != "" {
		where = " WHERE instr(search,?)>0"
		args = append(args, query)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, prefix+`SELECT status_group,COUNT(*) FROM `+table+where+` GROUP BY status_group`, args...)
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
	pageArgs := slices.Clone(args)
	if status != "all" {
		if where == "" {
			where = " WHERE status_group=?"
		} else {
			where += " AND status_group=?"
		}
		pageArgs = append(pageArgs, status)
	}
	pageArgs = append(pageArgs, (out.Page-1)*20)
	rows, err = tx.QueryContext(ctx, prefix+`SELECT record FROM `+table+where+` ORDER BY at DESC,run_id,file_id LIMIT 20 OFFSET ?`, pageArgs...)
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
