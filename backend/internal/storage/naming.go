package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"strm115/backend/internal/naming"
)

var ErrNamingConflict = errors.New("命名模板已在其他页面修改，请重新读取后保存")

func (s *Store) NamingTemplate(ctx context.Context, kind naming.Kind) (naming.Snapshot, error) {
	return readNamingTemplate(ctx, s.db, kind)
}

func readNamingTemplate(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, kind naming.Kind) (naming.Snapshot, error) {
	template, err := naming.Default(kind)
	if err != nil {
		return naming.Snapshot{}, err
	}
	out := naming.Snapshot{Template: template}
	var raw string
	err = q.QueryRowContext(ctx, `SELECT revision,template_json FROM naming_templates WHERE kind=?`, kind).Scan(&out.Revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("读取命名模板: %w", err)
	}
	out.Template = naming.Template{}
	if err := json.Unmarshal([]byte(raw), &out.Template); err != nil {
		return out, fmt.Errorf("解码命名模板: %w", err)
	}
	if out.Template.Kind != kind {
		return out, fmt.Errorf("命名模板作品类型不一致")
	}
	if _, err := naming.Compile(out.Template); err != nil {
		return out, fmt.Errorf("校验已保存的命名模板: %w", err)
	}
	return out, nil
}

// SaveNamingTemplate uses a per-kind revision. Replaying the exact last save
// returns its result without another update, including after a lost response.
func (s *Store) SaveNamingTemplate(ctx context.Context, template naming.Template, expected int64) (naming.Snapshot, error) {
	if expected < 0 || expected >= 1<<53-1 {
		return naming.Snapshot{}, fmt.Errorf("%w：模板版本无效", naming.ErrInput)
	}
	if _, err := naming.Compile(template); err != nil {
		return naming.Snapshot{}, err
	}
	raw, err := json.Marshal(template)
	if err != nil {
		return naming.Snapshot{}, fmt.Errorf("编码命名模板: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return naming.Snapshot{}, fmt.Errorf("开始保存命名模板: %w", err)
	}
	defer tx.Rollback()
	current, err := readNamingTemplate(ctx, tx, template.Kind)
	if err != nil {
		return naming.Snapshot{}, err
	}
	if current.Revision != expected {
		previous, err := json.Marshal(current.Template)
		if err != nil {
			return naming.Snapshot{}, fmt.Errorf("编码已保存的命名模板: %w", err)
		}
		if current.Revision == expected+1 && string(previous) == string(raw) {
			return current, nil
		}
		return naming.Snapshot{}, ErrNamingConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO naming_templates(kind,revision,template_json) VALUES(?,?,?) ON CONFLICT(kind) DO UPDATE SET revision=excluded.revision,template_json=excluded.template_json`, template.Kind, expected+1, string(raw)); err != nil {
		return naming.Snapshot{}, fmt.Errorf("保存命名模板: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return naming.Snapshot{}, fmt.Errorf("提交命名模板: %w", err)
	}
	return naming.Snapshot{Revision: expected + 1, Template: template}, nil
}
