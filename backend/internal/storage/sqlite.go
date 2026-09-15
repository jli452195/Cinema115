package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const schemaVersion = 11

// Store 由主进程持有，后台任务和 HTTP 请求退出后统一关闭。
type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}
	// 预先以仅所有者可读写的权限创建文件；不截断已有数据库。
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("打开数据库文件: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("关闭数据库文件句柄: %w", err)
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	// 每个新连接都应用配置，不能只在首个连接上设置 PRAGMA。
	for _, pragma := range []string{"busy_timeout(5000)", "foreign_keys(ON)", "synchronous(FULL)"} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("初始化 SQLite: %w", err)
	}
	// 当前任务写入串行化，单连接减少 NAS 内存占用和写锁竞争。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return fmt.Errorf("启用数据库 WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("数据库未启用 WAL")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始数据库结构迁移: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取数据库版本: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于程序支持的 %d，请使用较新程序", version, schemaVersion)
	}
	if version == 0 {
		_, err = tx.ExecContext(ctx, `
CREATE TABLE reshare_jobs (
 id TEXT PRIMARY KEY NOT NULL,
 source_url TEXT NOT NULL,
 source_cid TEXT NOT NULL,
 directory_name TEXT NOT NULL,
 directory_id TEXT NOT NULL,
 share_url TEXT NOT NULL,
 status TEXT NOT NULL,
 finished INTEGER NOT NULL CHECK (finished IN (0, 1)),
 last_error TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX reshare_jobs_updated ON reshare_jobs(updated_at DESC, id DESC);
CREATE INDEX reshare_jobs_status_updated ON reshare_jobs(status, updated_at DESC, id DESC);
CREATE TABLE data_migrations (
 name TEXT PRIMARY KEY NOT NULL,
 imported_count INTEGER NOT NULL,
 completed_at TEXT NOT NULL
);
PRAGMA user_version=1;`)
		if err != nil {
			return fmt.Errorf("创建数据库结构: %w", err)
		}
	}
	if version < 2 {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE share_tasks (
id TEXT PRIMARY KEY NOT NULL, status TEXT NOT NULL, mode TEXT NOT NULL,
source_url TEXT NOT NULL, share_url TEXT NOT NULL, updated_at TEXT NOT NULL,
next_retry_at TEXT NOT NULL, record TEXT NOT NULL, request TEXT NOT NULL);
CREATE INDEX share_tasks_updated ON share_tasks(updated_at DESC, id DESC);
CREATE INDEX share_tasks_due ON share_tasks(status, next_retry_at);
PRAGMA user_version=2;`); err != nil {
			return fmt.Errorf("迁移分享任务表: %w", err)
		}
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE emby_state (scope TEXT PRIMARY KEY, active TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'idle', count INTEGER NOT NULL DEFAULT 0, scanned INTEGER NOT NULL DEFAULT 0, synced_at TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '', webhook_at TEXT NOT NULL DEFAULT '', webhook_event TEXT NOT NULL DEFAULT '', webhook_error TEXT NOT NULL DEFAULT '');
CREATE TABLE emby_items (scope TEXT NOT NULL, generation TEXT NOT NULL, id TEXT NOT NULL, library_id TEXT NOT NULL, type TEXT NOT NULL, tmdb TEXT NOT NULL, series_id TEXT NOT NULL, parent_id TEXT NOT NULL, record TEXT NOT NULL, PRIMARY KEY(scope,generation,id));
CREATE INDEX emby_tmdb ON emby_items(scope,generation,type,tmdb);
CREATE INDEX emby_series ON emby_items(scope,generation,series_id);
CREATE TABLE emby_events (id INTEGER PRIMARY KEY AUTOINCREMENT, scope TEXT NOT NULL, item_id TEXT NOT NULL, kind TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, due INTEGER NOT NULL, last_error TEXT NOT NULL DEFAULT '', UNIQUE(scope,item_id));
CREATE INDEX emby_due ON emby_events(scope,due);
PRAGMA user_version=3;`); err != nil {
			return fmt.Errorf("迁移 Emby 索引表: %w", err)
		}
	}
	if version < 4 {
		if err := migrateMediaCache(ctx, tx); err != nil {
			return err
		}
	}
	if version < 5 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE p115_accounts (id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL, cookie_enc BLOB NOT NULL, revision INTEGER NOT NULL CHECK(revision > 0), position INTEGER NOT NULL);
CREATE TABLE p115_account_state (id INTEGER PRIMARY KEY CHECK(id=1), active_id TEXT REFERENCES p115_accounts(id) DEFERRABLE INITIALLY DEFERRED, revision INTEGER NOT NULL CHECK(revision > 0));
PRAGMA user_version=5;`); err != nil {
			return fmt.Errorf("迁移 115 账号表: %w", err)
		}
	}
	if version < 6 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE p115_feature_migration (feature TEXT PRIMARY KEY NOT NULL, account_id TEXT NOT NULL);
INSERT INTO p115_feature_migration(feature,account_id) SELECT 'share',active_id FROM p115_account_state WHERE active_id IS NOT NULL;
CREATE TABLE p115_account_state_v6 (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL CHECK(revision > 0));
INSERT INTO p115_account_state_v6(id,revision) SELECT id,revision FROM p115_account_state;
DROP TABLE p115_account_state;
ALTER TABLE p115_account_state_v6 RENAME TO p115_account_state;
UPDATE p115_accounts SET name='账号 1',revision=revision+1 WHERE id='default' AND name='默认账号';
PRAGMA user_version=6;`); err != nil {
			return fmt.Errorf("迁移 115 功能独立账号选择: %w", err)
		}
	}
	if version < 7 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE telegram_notifications (
 id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL,
 bot_id TEXT NOT NULL, chat_id INTEGER NOT NULL, text TEXT NOT NULL,
 preview_url TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
 due INTEGER NOT NULL, created_at INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending',
 UNIQUE(task_id,bot_id,chat_id)
);
CREATE INDEX telegram_notifications_due ON telegram_notifications(status,bot_id,chat_id,due);
PRAGMA user_version=7;`); err != nil {
			return fmt.Errorf("迁移 Telegram 生成通知表: %w", err)
		}
	}
	if version < 8 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE website_users (
 id TEXT PRIMARY KEY NOT NULL, username TEXT NOT NULL COLLATE NOCASE UNIQUE,
 password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user' CHECK(role='user'),
 disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0,1)),
 token_version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL
);
PRAGMA user_version=8;`); err != nil {
			return fmt.Errorf("迁移网站用户表: %w", err)
		}
	}
	if version < 9 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE follow_requests (
 kind TEXT NOT NULL CHECK(kind IN ('movie','tv')), tmdb_id INTEGER NOT NULL CHECK(tmdb_id>0),
 title TEXT NOT NULL, poster_url TEXT NOT NULL, release_date TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','completed')),
 created_at TEXT NOT NULL, PRIMARY KEY(kind,tmdb_id)
);
CREATE TABLE user_follows (
 user_id TEXT NOT NULL, kind TEXT NOT NULL, tmdb_id INTEGER NOT NULL,
 active INTEGER NOT NULL CHECK(active IN (0,1)), subscribed_at TEXT NOT NULL,
 PRIMARY KEY(user_id,kind,tmdb_id),
 FOREIGN KEY(kind,tmdb_id) REFERENCES follow_requests(kind,tmdb_id)
);
CREATE INDEX user_follows_list ON user_follows(user_id,active,subscribed_at DESC,kind,tmdb_id);
CREATE INDEX user_follows_request ON user_follows(kind,tmdb_id,active);
PRAGMA user_version=9;`); err != nil {
			return fmt.Errorf("迁移追更订阅表: %w", err)
		}
	}
	if version < 10 {
		if _, err := tx.ExecContext(ctx, `
ALTER TABLE follow_requests ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE follow_requests ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE follow_requests ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE follow_requests ADD COLUMN cycle INTEGER NOT NULL DEFAULT 0;
UPDATE follow_requests SET updated_at=created_at;
CREATE TABLE follow_settings(id INTEGER PRIMARY KEY CHECK(id=1),base_url TEXT NOT NULL DEFAULT '');
INSERT INTO follow_settings(id) VALUES(1);
PRAGMA user_version=10;`); err != nil {
			return fmt.Errorf("迁移追更管理与通知配置: %w", err)
		}
	}
	if version < 11 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE follow_checks (
 kind TEXT NOT NULL, tmdb_id INTEGER NOT NULL,
 state TEXT NOT NULL DEFAULT 'unknown', aired INTEGER NOT NULL DEFAULT 0, owned INTEGER NOT NULL DEFAULT 0,
 checked_at INTEGER NOT NULL DEFAULT 0, due INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1,
 error TEXT NOT NULL DEFAULT '', PRIMARY KEY(kind,tmdb_id),
 FOREIGN KEY(kind,tmdb_id) REFERENCES follow_requests(kind,tmdb_id) ON DELETE CASCADE
);
INSERT INTO follow_checks(kind,tmdb_id) SELECT kind,tmdb_id FROM follow_requests;
UPDATE follow_checks SET due=9223372036854775807 WHERE NOT EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_checks.kind AND f.tmdb_id=follow_checks.tmdb_id AND f.active=1);
CREATE INDEX follow_checks_due ON follow_checks(due);
CREATE TABLE website_notifications (
 id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL, kind TEXT NOT NULL, tmdb_id INTEGER NOT NULL,
 title TEXT NOT NULL, message TEXT NOT NULL, created_at INTEGER NOT NULL, read_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX website_notifications_list ON website_notifications(user_id,id DESC);
CREATE INDEX website_notifications_unread ON website_notifications(user_id,id) WHERE read_at=0;
PRAGMA user_version=11;`); err != nil {
			return fmt.Errorf("迁移追更入库检查与站内通知: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库结构迁移: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }
