package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type MediaOrganizeTaskRef struct {
	RunID  string
	FileID string
}

type MediaOrganizeResolutionCandidate struct {
	RunID     string
	Completed string
}

// 批量重试沿用记录页的字面搜索和排序，只读取失败任务的主键。
func (s *Store) QueryMediaOrganizeFailedRefs(ctx context.Context, query string) ([]MediaOrganizeTaskRef, error) {
	statement := `SELECT run_id,file_id FROM media_organize_tasks WHERE status_group='failed'`
	args := make([]any, 0, 1)
	if query = strings.ToLower(strings.TrimSpace(query)); query != "" {
		statement += ` AND instr(search,?)>0`
		args = append(args, query)
	}
	rows, err := s.db.QueryContext(ctx, statement+` ORDER BY at DESC,run_id,file_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询整理失败任务: %w", err)
	}
	defer rows.Close()
	out := make([]MediaOrganizeTaskRef, 0)
	for rows.Next() {
		var item MediaOrganizeTaskRef
		if err := rows.Scan(&item.RunID, &item.FileID); err != nil {
			return nil, fmt.Errorf("读取整理失败任务: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// 先按任务索引找同规则、同文件的失败与成功交集，仅投影候选成功条目。
// nil 范围供启动收敛使用；非 nil 空范围不读取任何历史。
func (s *Store) LoadMediaOrganizeResolvedCandidates(ctx context.Context, runIDs []string) ([]MediaOrganizeResolutionCandidate, error) {
	if runIDs != nil && len(runIDs) == 0 {
		return []MediaOrganizeResolutionCandidate{}, ctx.Err()
	}
	filter := ""
	args := make([]any, 0, 1)
	if runIDs != nil {
		raw, err := json.Marshal(runIDs)
		if err != nil {
			return nil, fmt.Errorf("编码整理失败范围: %w", err)
		}
		filter = ` AND f.run_id IN (SELECT value FROM json_each(?))`
		args = append(args, string(raw))
	}
	statement := `WITH matches AS MATERIALIZED (
		SELECT f.run_id AS failed_run_id,d.run_id AS completed_run_id,f.file_id
		FROM media_organize_tasks f
		JOIN media_organize_runs fr ON fr.id=f.run_id
		JOIN media_organize_tasks d ON d.file_id=f.file_id AND d.status_group='done' AND d.run_id!=f.run_id
		JOIN media_organize_runs sr ON sr.id=d.run_id AND sr.rule_id=fr.rule_id
		WHERE f.status_group='failed'` + filter + `
	),selected AS MATERIALIZED (
		SELECT p.failed_run_id,p.completed_run_id,e.value AS entry
		FROM (SELECT DISTINCT failed_run_id,completed_run_id FROM matches) p
		JOIN media_organize_runs r ON r.id=p.completed_run_id
		JOIN json_each(r.record,'$.entries') e
		WHERE json_extract(e.value,'$.file.mediaType')='video' AND json_extract(e.value,'$.status')='done'
		AND EXISTS (SELECT 1 FROM matches m WHERE m.failed_run_id=p.failed_run_id AND m.completed_run_id=p.completed_run_id
		AND m.file_id=CAST(json_extract(e.value,'$.file.id') AS TEXT))
	)
	SELECT p.failed_run_id,json_set(r.record,'$.entries',json((SELECT json_group_array(json(entry)) FROM selected
		WHERE failed_run_id=p.failed_run_id AND completed_run_id=p.completed_run_id)),'$.groups',json('[]'))
	FROM (SELECT DISTINCT failed_run_id,completed_run_id FROM selected) p
	JOIN media_organize_runs r ON r.id=p.completed_run_id
	ORDER BY p.failed_run_id,r.at DESC,r.id`
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("查询整理失败协调候选: %w", err)
	}
	defer rows.Close()
	out := make([]MediaOrganizeResolutionCandidate, 0)
	for rows.Next() {
		var item MediaOrganizeResolutionCandidate
		if err := rows.Scan(&item.RunID, &item.Completed); err != nil {
			return nil, fmt.Errorf("读取整理失败协调候选: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
