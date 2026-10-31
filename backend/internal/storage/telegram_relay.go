package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

// InitTelegramRelay is installed once by the Telegram relay service, without
// changing any search, Bot, download or STRM persistence.
func (s *Store) InitTelegramRelay(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS telegram_relay_rules (
 id TEXT PRIMARY KEY, owner_id INTEGER NOT NULL, source_id TEXT NOT NULL,
 target_id TEXT NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 updated_at INTEGER NOT NULL, record TEXT NOT NULL,
 UNIQUE(owner_id,source_id)
);
CREATE TABLE IF NOT EXISTS telegram_relay_state (
 owner_id INTEGER NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL,
 PRIMARY KEY(owner_id,key)
);
CREATE TABLE IF NOT EXISTS telegram_relay_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 rule_id TEXT NOT NULL REFERENCES telegram_relay_rules(id) ON DELETE CASCADE,
 message_id INTEGER NOT NULL, fingerprint TEXT NOT NULL, payload TEXT NOT NULL,
 random_id INTEGER NOT NULL, title TEXT NOT NULL, source_url TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sent','failed')),
 attempts INTEGER NOT NULL DEFAULT 0, due INTEGER NOT NULL,
 error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
 UNIQUE(rule_id,message_id,fingerprint)
);
CREATE INDEX IF NOT EXISTS telegram_relay_jobs_due ON telegram_relay_jobs(status,due,id);
CREATE INDEX IF NOT EXISTS telegram_relay_jobs_rule ON telegram_relay_jobs(rule_id,id DESC);
DELETE FROM telegram_relay_jobs WHERE status<>'pending';`)
	if err != nil {
		return fmt.Errorf("初始化 Telegram 转发存储: %w", err)
	}
	return nil
}

func validateTelegramRelayRule(rule model.TelegramRelayRule) error {
	if rule.ID == "" || strings.TrimSpace(rule.Name) == "" || rule.OwnerID <= 0 || rule.SourceID == "" || rule.TargetID == "" || rule.StartedAt.IsZero() || rule.UpdatedAt.IsZero() {
		return errors.New("Telegram 转发规则缺少必要字段")
	}
	if len(rule.LinkTypes) == 0 || len(rule.LinkTypes) > 2 {
		return errors.New("Telegram 转发规则须选择链接类型")
	}
	seen := make(map[string]bool, len(rule.LinkTypes))
	for _, kind := range rule.LinkTypes {
		if kind != "115" && kind != "ed2k" || seen[kind] {
			return errors.New("Telegram 转发链接类型无效或重复")
		}
		seen[kind] = true
	}
	return nil
}

func (s *Store) SaveTelegramRelayRule(ctx context.Context, rule model.TelegramRelayRule) error {
	if err := validateTelegramRelayRule(rule); err != nil {
		return err
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("编码 Telegram 转发规则: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存 Telegram 转发规则: %w", err)
	}
	defer tx.Rollback()
	var owner int64
	var source, target string
	err = tx.QueryRowContext(ctx, `SELECT owner_id,source_id,target_id FROM telegram_relay_rules WHERE id=?`, rule.ID).Scan(&owner, &source, &target)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取原 Telegram 转发规则: %w", err)
	}
	if err == nil && (owner != rule.OwnerID || source != rule.SourceID || target != rule.TargetID) {
		// A queued card must never silently move to another account or target.
		if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_relay_jobs WHERE rule_id=?`, rule.ID); err != nil {
			return fmt.Errorf("清除原 Telegram 转发队列: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_relay_state WHERE owner_id=? AND key=?`, owner, source); err != nil {
			return fmt.Errorf("清除原 Telegram 频道进度: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_relay_state WHERE owner_id=? AND substr(key,1,?)=?`, owner, len("message:"+rule.ID+":"), "message:"+rule.ID+":"); err != nil {
			return fmt.Errorf("清除原 Telegram 消息去重记录: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO telegram_relay_rules(id,owner_id,source_id,target_id,enabled,updated_at,record)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET owner_id=excluded.owner_id,source_id=excluded.source_id,target_id=excluded.target_id,enabled=excluded.enabled,updated_at=excluded.updated_at,record=excluded.record`,
		rule.ID, rule.OwnerID, rule.SourceID, rule.TargetID, rule.Enabled, rule.UpdatedAt.UnixMilli(), string(raw))
	if err != nil {
		return fmt.Errorf("保存 Telegram 转发规则: %w", err)
	}
	if rule.SourcePts > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO telegram_relay_state(owner_id,key,value) VALUES(?,?,?) ON CONFLICT(owner_id,key) DO NOTHING`, rule.OwnerID, rule.SourceID, strconv.Itoa(rule.SourcePts)); err != nil {
			return fmt.Errorf("保存 Telegram 频道初始进度: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Telegram 转发规则: %w", err)
	}
	return nil
}

func scanTelegramRelayRule(row interface{ Scan(...any) error }) (model.TelegramRelayRule, error) {
	var raw string
	var rule model.TelegramRelayRule
	if err := row.Scan(&raw); err != nil {
		return rule, err
	}
	if err := json.Unmarshal([]byte(raw), &rule); err != nil {
		return rule, fmt.Errorf("解析 Telegram 转发规则: %w", err)
	}
	return rule, validateTelegramRelayRule(rule)
}

func (s *Store) GetTelegramRelayRule(ctx context.Context, id string) (model.TelegramRelayRule, error) {
	rule, err := scanTelegramRelayRule(s.db.QueryRowContext(ctx, `SELECT record FROM telegram_relay_rules WHERE id=?`, id))
	if err != nil {
		return rule, fmt.Errorf("读取 Telegram 转发规则: %w", err)
	}
	return rule, nil
}

func (s *Store) ListTelegramRelayRules(ctx context.Context) ([]model.TelegramRelayRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM telegram_relay_rules ORDER BY updated_at DESC,id DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询 Telegram 转发规则: %w", err)
	}
	defer rows.Close()
	rules := make([]model.TelegramRelayRule, 0)
	for rows.Next() {
		rule, err := scanTelegramRelayRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *Store) DeleteTelegramRelayRule(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始删除 Telegram 转发规则: %w", err)
	}
	defer tx.Rollback()
	var owner int64
	var source string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,source_id FROM telegram_relay_rules WHERE id=?`, id).Scan(&owner, &source); err != nil {
		return fmt.Errorf("读取待删除 Telegram 转发规则: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_relay_rules WHERE id=?`, id); err != nil {
		return fmt.Errorf("删除 Telegram 转发规则: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_relay_state WHERE owner_id=? AND key=?`, owner, source); err != nil {
		return fmt.Errorf("删除 Telegram 频道进度: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_relay_state WHERE owner_id=? AND substr(key,1,?)=?`, owner, len("message:"+id+":"), "message:"+id+":"); err != nil {
		return fmt.Errorf("删除 Telegram 消息去重记录: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Telegram 转发删除: %w", err)
	}
	return nil
}

func (s *Store) LoadTelegramRelayState(ctx context.Context, ownerID int64, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM telegram_relay_state WHERE owner_id=? AND key=?`, ownerID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取 Telegram 转发进度: %w", err)
	}
	return value, nil
}

func validateTelegramRelayJob(job model.TelegramRelayJob) error {
	if job.RuleID == "" || job.MessageID <= 0 || job.Fingerprint == "" || job.Payload == "" || job.RandomID == 0 || job.CreatedAt.IsZero() || job.Due.IsZero() {
		return errors.New("Telegram 转发任务缺少必要字段")
	}
	return nil
}

// CommitTelegramRelayUpdate persists the incoming update position with all
// accepted cards. Replayed updates keep the first payload and random ID.
func (s *Store) CommitTelegramRelayUpdate(ctx context.Context, ownerID int64, key, state string, jobs []model.TelegramRelayJob) error {
	if ownerID <= 0 || key == "" {
		return errors.New("Telegram 转发进度缺少账号或键")
	}
	for _, job := range jobs {
		if err := validateTelegramRelayJob(job); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存 Telegram 转发更新: %w", err)
	}
	defer tx.Rollback()
	for _, job := range jobs {
		_, err := tx.ExecContext(ctx, `INSERT INTO telegram_relay_jobs(rule_id,message_id,fingerprint,payload,random_id,title,source_url,due,created_at)
SELECT id,?,?,?,?,'','',?,? FROM telegram_relay_rules WHERE id=? AND owner_id=? AND enabled=1
ON CONFLICT(rule_id,message_id,fingerprint) DO NOTHING`, job.MessageID, job.Fingerprint, job.Payload, job.RandomID, job.Due.UnixMilli(), job.CreatedAt.UnixMilli(), job.RuleID, ownerID)
		if err != nil {
			return fmt.Errorf("保存 Telegram 转发任务: %w", err)
		}
	}
	// Paused/deleted rules retain their last accepted channel position even if
	// a cancelled watcher finishes an already dispatched state callback.
	if _, err := tx.ExecContext(ctx, `INSERT INTO telegram_relay_state(owner_id,key,value)
SELECT ?,?,? WHERE ? NOT LIKE 'channel:%' OR EXISTS(SELECT 1 FROM telegram_relay_rules WHERE owner_id=? AND source_id=? AND enabled=1)
ON CONFLICT(owner_id,key) DO UPDATE SET value=excluded.value`, ownerID, key, state, key, ownerID, key); err != nil {
		return fmt.Errorf("保存 Telegram 转发进度: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Telegram 转发更新: %w", err)
	}
	return nil
}

func (s *Store) DueTelegramRelayJobs(ctx context.Context, ownerID int64, now time.Time, limit int) ([]model.TelegramRelayJob, error) {
	limit = max(1, min(limit, 100))
	rows, err := s.db.QueryContext(ctx, `WITH heads AS (
SELECT j.id,ROW_NUMBER() OVER(PARTITION BY r.target_id ORDER BY j.id) AS position
FROM telegram_relay_jobs j JOIN telegram_relay_rules r ON r.id=j.rule_id
WHERE r.owner_id=? AND r.enabled=1 AND j.status='pending')
SELECT j.id,j.rule_id,j.message_id,j.fingerprint,j.payload,j.random_id,j.attempts,j.due,j.created_at FROM telegram_relay_jobs j JOIN heads h ON h.id=j.id
WHERE h.position=1 AND j.due<=? ORDER BY j.due,j.id LIMIT ?`, ownerID, now.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("查询到期 Telegram 转发任务: %w", err)
	}
	defer rows.Close()
	jobs := make([]model.TelegramRelayJob, 0)
	for rows.Next() {
		var job model.TelegramRelayJob
		var due, created int64
		if err := rows.Scan(&job.ID, &job.RuleID, &job.MessageID, &job.Fingerprint, &job.Payload, &job.RandomID, &job.Attempts, &due, &created); err != nil {
			return nil, fmt.Errorf("读取 Telegram 待投递任务: %w", err)
		}
		job.Due, job.CreatedAt = time.UnixMilli(due).UTC(), time.UnixMilli(created).UTC()
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) RemoveTelegramRelayJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM telegram_relay_jobs WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("清除 Telegram 已投递任务: %w", err)
	}
	return nil
}

func (s *Store) DeferTelegramRelayJob(ctx context.Context, id int64, due time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_relay_jobs SET attempts=attempts+1,due=? WHERE id=? AND status='pending'`, due.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("安排 Telegram 转发重试: %w", err)
	}
	return nil
}
