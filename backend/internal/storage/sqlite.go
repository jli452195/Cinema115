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

const schemaVersion = 42

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
	if version < 12 {
		if _, err := tx.ExecContext(ctx, `
ALTER TABLE follow_settings ADD COLUMN auto_enabled INTEGER NOT NULL DEFAULT 0 CHECK(auto_enabled IN (0,1));
ALTER TABLE follow_settings ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
CREATE TABLE follow_bot_jobs (
 kind TEXT NOT NULL, tmdb_id INTEGER NOT NULL, cycle INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0,
 due INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 PRIMARY KEY(kind,tmdb_id,cycle),
 FOREIGN KEY(kind,tmdb_id) REFERENCES follow_requests(kind,tmdb_id) ON DELETE CASCADE
);
CREATE INDEX follow_bot_jobs_due ON follow_bot_jobs(status,due,created_at);
PRAGMA user_version=12;`); err != nil {
			return fmt.Errorf("迁移追更自动搜索队列: %w", err)
		}
	}
	if version < 13 {
		if _, err := tx.ExecContext(ctx, `
UPDATE follow_bot_jobs SET status='checking',due=0,last_error='' WHERE status='queued' AND attempts=0;
PRAGMA user_version=13;`); err != nil {
			return fmt.Errorf("迁移追更搜索前入库检查: %w", err)
		}
	}
	if version < 14 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS share_run_cache (
 fingerprint TEXT PRIMARY KEY NOT NULL,
 record TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
PRAGMA user_version=14;`); err != nil {
			return fmt.Errorf("迁移分享快照复用缓存: %w", err)
		}
	}
	if version < 15 {
		rows, err := tx.QueryContext(ctx, "PRAGMA table_info(follow_requests)")
		if err != nil {
			return fmt.Errorf("读取追更表结构: %w", err)
		}
		hasSearchClosed := false
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = rows.Close()
				return fmt.Errorf("读取追更表字段: %w", err)
			}
			hasSearchClosed = hasSearchClosed || name == "search_closed"
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("遍历追更表字段: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("关闭追更表结构查询: %w", err)
		}
		if !hasSearchClosed {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE follow_requests ADD COLUMN search_closed INTEGER NOT NULL DEFAULT 0 CHECK(search_closed IN (0,1))`); err != nil {
				return fmt.Errorf("增加追更关闭状态: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `PRAGMA user_version=15`); err != nil {
			return fmt.Errorf("迁移追更关闭状态: %w", err)
		}
	}
	if version < 16 {
		rows, err := tx.QueryContext(ctx, "PRAGMA table_info(follow_settings)")
		if err != nil {
			return fmt.Errorf("读取追更设置表结构: %w", err)
		}
		columns := make(map[string]bool)
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = rows.Close()
				return fmt.Errorf("读取追更设置字段: %w", err)
			}
			columns[name] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("遍历追更设置字段: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("关闭追更设置表结构查询: %w", err)
		}
		if !columns["telegram_enabled"] {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE follow_settings ADD COLUMN telegram_enabled INTEGER NOT NULL DEFAULT 0 CHECK(telegram_enabled IN (0,1))"); err != nil {
				return fmt.Errorf("增加追更 Telegram 开关: %w", err)
			}
		}
		if !columns["telegram_bot_id"] {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE follow_settings ADD COLUMN telegram_bot_id TEXT NOT NULL DEFAULT ''"); err != nil {
				return fmt.Errorf("增加追更 Telegram Bot 绑定: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version=16"); err != nil {
			return fmt.Errorf("保存追更 Telegram Bot 迁移版本: %w", err)
		}
	}
	if version < 17 {
		if err := migrateMediaUpdates(ctx, tx); err != nil {
			return err
		}
	}
	if version < 18 {
		if err := migrateEmbySyncCheckpoints(ctx, tx); err != nil {
			return err
		}
	}
	if version < 19 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS p115_folder_monitors (
 id TEXT PRIMARY KEY NOT NULL,
 account_id TEXT NOT NULL,
 status TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 record TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS p115_folder_monitors_status ON p115_folder_monitors(status,updated_at);
PRAGMA user_version=19;`); err != nil {
			return fmt.Errorf("迁移 115 目录监控: %w", err)
		}
	}
	if version < 20 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('p115_accounts') WHERE name='playback_parent_cid'`).Scan(&exists); err != nil {
			return fmt.Errorf("检查账号播放目录字段: %w", err)
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE p115_accounts ADD COLUMN playback_parent_cid TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("迁移账号播放目录: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `PRAGMA user_version=20;`); err != nil {
			return fmt.Errorf("迁移账号播放目录: %w", err)
		}
	}
	if version < 21 {
		for _, column := range []struct{ name, definition string }{
			{"is_primary", "INTEGER NOT NULL DEFAULT 0 CHECK(is_primary IN (0,1))"},
			{"uid", "TEXT NOT NULL DEFAULT ''"},
		} {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('p115_accounts') WHERE name=?`, column.name).Scan(&exists); err != nil {
				return fmt.Errorf("检查账号角色字段: %w", err)
			}
			if exists == 0 {
				if _, err := tx.ExecContext(ctx, "ALTER TABLE p115_accounts ADD COLUMN "+column.name+" "+column.definition); err != nil {
					return fmt.Errorf("创建账号角色字段: %w", err)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS p115_single_primary ON p115_accounts(is_primary) WHERE is_primary=1; PRAGMA user_version=21;`); err != nil {
			return fmt.Errorf("保存主账号结构: %w", err)
		}
	}
	if version < 22 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS playback_materials (id TEXT PRIMARY KEY, sha1 TEXT NOT NULL, size INTEGER NOT NULL CHECK(size>0), record TEXT NOT NULL, UNIQUE(sha1,size));
CREATE INDEX IF NOT EXISTS playback_material_sha1 ON playback_materials(sha1);
CREATE TABLE IF NOT EXISTS playback_origins (origin TEXT NOT NULL, material_id TEXT NOT NULL REFERENCES playback_materials(id) ON DELETE CASCADE, PRIMARY KEY(origin,material_id));
CREATE TABLE IF NOT EXISTS playback_ranges (material_id TEXT NOT NULL REFERENCES playback_materials(id) ON DELETE CASCADE, start INTEGER NOT NULL, end INTEGER NOT NULL, record TEXT NOT NULL, PRIMARY KEY(material_id,start,end));
CREATE TABLE IF NOT EXISTS playback_copies (id TEXT PRIMARY KEY, account_id TEXT NOT NULL, sha1 TEXT NOT NULL, source_key TEXT NOT NULL, record TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS playback_copy_account_hash ON playback_copies(account_id,sha1);
CREATE INDEX IF NOT EXISTS playback_copy_hash ON playback_copies(sha1);
CREATE INDEX IF NOT EXISTS playback_copy_source ON playback_copies(source_key);
CREATE TABLE IF NOT EXISTS playback_settings (id INTEGER PRIMARY KEY CHECK(id=1), record TEXT NOT NULL);
PRAGMA user_version=22;`); err != nil {
			return fmt.Errorf("创建播放秒传资料库: %w", err)
		}
	}
	if version < 26 {
		for _, column := range []struct{ name, definition string }{{"ownership", "TEXT NOT NULL DEFAULT 'admin'"}, {"owner_user_id", "TEXT NOT NULL DEFAULT ''"}} {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('p115_accounts') WHERE name=?`, column.name).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				if _, err := tx.ExecContext(ctx, "ALTER TABLE p115_accounts ADD COLUMN "+column.name+" "+column.definition); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `PRAGMA user_version=26;`); err != nil {
			return err
		}
	}
	// User identity schema is fresh: no website-account migration or linking.
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS emby_profiles(id TEXT PRIMARY KEY,server_id TEXT NOT NULL,emby_id TEXT NOT NULL,username TEXT NOT NULL,token_version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,UNIQUE(server_id,emby_id));
CREATE TABLE IF NOT EXISTS member_playback(user_id TEXT PRIMARY KEY REFERENCES emby_profiles(id),record TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS member_emby_identity ON member_playback(json_extract(record,'$.serverId'),json_extract(record,'$.embyId'));
DROP INDEX IF EXISTS member_pool_assignment;
CREATE TABLE IF NOT EXISTS member_playback_settings(id INTEGER PRIMARY KEY CHECK(id=1),record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS playback_pool(id INTEGER PRIMARY KEY CHECK(id=1),record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS playback_child_creations(id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES emby_profiles(id), record TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS playback_child_owner ON playback_child_creations(owner_id);
CREATE TABLE IF NOT EXISTS portal_invites(id TEXT PRIMARY KEY,code_hash TEXT NOT NULL UNIQUE,created_by TEXT NOT NULL,note TEXT NOT NULL,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,revoked_at INTEGER NOT NULL DEFAULT 0,used_at INTEGER NOT NULL DEFAULT 0,used_by TEXT NOT NULL DEFAULT '',playback_source TEXT NOT NULL CHECK(playback_source IN ('personal','assigned')),registration TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS portal_invites_created ON portal_invites(created_at DESC,id);
PRAGMA user_version=30;`); err != nil {
		return fmt.Errorf("创建 Emby 个人中心配置: %w", err)
	}
	if version < 31 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('p115_accounts') WHERE name='login_client'`).Scan(&exists); err != nil {
			return fmt.Errorf("检查登录客户端字段: %w", err)
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE p115_accounts ADD COLUMN login_client TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("创建登录客户端字段: %w", err)
			}
		}
	}
	if version < 32 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('p115_accounts') WHERE name='summary'`).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE p115_accounts ADD COLUMN summary TEXT NOT NULL DEFAULT '{}'`); err != nil {
				return fmt.Errorf("创建账号资料字段: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS user_playback_leases(id INTEGER PRIMARY KEY CHECK(id=1),record TEXT NOT NULL); CREATE TABLE IF NOT EXISTS member_deletions(id TEXT PRIMARY KEY, record TEXT NOT NULL); PRAGMA user_version=35;`); err != nil {
		return err
	}
	// Also repair the lookup index for databases created by an earlier v22 build.
	if version < 36 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('reshare_jobs') WHERE name='stats'`).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE reshare_jobs ADD COLUMN stats TEXT NOT NULL DEFAULT 'null'`); err != nil {
				return fmt.Errorf("创建转存任务统计字段: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tv_numbering(tmdb_id INTEGER PRIMARY KEY CHECK(tmdb_id>0),revision INTEGER NOT NULL,group_id TEXT NOT NULL,name TEXT NOT NULL,type INTEGER NOT NULL,saved_at INTEGER NOT NULL,snapshot BLOB NOT NULL); PRAGMA user_version=37`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS playback_origin_material ON playback_origins(material_id)`); err != nil {
		return fmt.Errorf("创建播放来源反向索引: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS emby_missing_exclusions (
 id TEXT PRIMARY KEY, service TEXT NOT NULL, series_id TEXT NOT NULL, tmdb TEXT NOT NULL,
 title TEXT NOT NULL, library_id TEXT NOT NULL, episode_count INTEGER NOT NULL,
 excluded_at TEXT NOT NULL, UNIQUE(service,series_id,tmdb)
); PRAGMA user_version=38;`); err != nil {
		return fmt.Errorf("创建缺集检测排除记录: %w", err)
	}
	if version < 39 {
		if err := discardLegacyAnimeV38(ctx, tx); err != nil {
			return fmt.Errorf("清理废弃动漫订阅存储: %w", err)
		}
		if _, err := tx.ExecContext(ctx, animeSchemaV39); err != nil {
			return fmt.Errorf("创建动漫订阅基础存储: %w", err)
		}
	}
	if version < 40 {
		if err := upgradeAnimePreviewsV40(ctx, tx); err != nil {
			return fmt.Errorf("创建动漫预览快照存储: %w", err)
		}
	}
	if version < 41 {
		if err := discardArchivedAnimeV38(ctx, tx); err != nil {
			return err
		}
	}
	if version < 42 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('anime_subscriptions') WHERE name='naming_json'`).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE anime_subscriptions ADD COLUMN naming_json TEXT NOT NULL DEFAULT 'null' CHECK(json_valid(naming_json))`); err != nil {
				return fmt.Errorf("创建动漫命名配置: %w", err)
			}
		}
	}
	// Earlier idempotent schema repairs assign historical versions above. Always
	// finish with the current version, including when reopening a migrated DB.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return fmt.Errorf("保存数据库结构版本: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库结构迁移: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }
