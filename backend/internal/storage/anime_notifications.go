package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"strm115/backend/internal/model"
)

func initializeAnimeNotificationSchema(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, animeNotificationTablesSchema); err != nil {
		return fmt.Errorf("创建动漫通知存储: %w", err)
	}
	for _, column := range []struct{ table, name, definition string }{
		{"telegram_notifications", "parse_mode", "TEXT NOT NULL DEFAULT '' CHECK(parse_mode IN ('','HTML'))"},
		{"telegram_notifications", "photo_url", "TEXT NOT NULL DEFAULT ''"},
		{"telegram_notifications", "photo_sent", "INTEGER NOT NULL DEFAULT 0 CHECK(photo_sent IN (0,1))"},
		{"anime_notification_settings", "image_mode", "TEXT NOT NULL DEFAULT 'cover' CHECK(image_mode IN ('none','cover','custom'))"},
		{"anime_notification_settings", "image_url", "TEXT NOT NULL DEFAULT ''"},
		{"anime_notification_events", "image_mode", "TEXT NOT NULL DEFAULT 'cover' CHECK(image_mode IN ('none','cover','custom'))"},
		{"anime_notification_events", "image_url", "TEXT NOT NULL DEFAULT ''"},
		{"anime_notification_events", "cover_url", "TEXT NOT NULL DEFAULT ''"},
		{"anime_notification_events", "processed", "INTEGER NOT NULL DEFAULT 0 CHECK(processed IN (0,1))"},
		{"anime_notification_events", "failure_count", "INTEGER NOT NULL DEFAULT 0"},
		{"anime_notification_events", "first_failure_at", "INTEGER NOT NULL DEFAULT 0"},
		{"anime_notification_events", "last_attempt_at", "INTEGER NOT NULL DEFAULT 0"},
		{"anime_notification_events", "phase", "TEXT NOT NULL DEFAULT ''"},
		{"anime_notification_events", "invalidated", "INTEGER NOT NULL DEFAULT 0 CHECK(invalidated IN (0,1))"},
		{"telegram_notifications", "anime_job_id", "TEXT NOT NULL DEFAULT ''"},
		{"telegram_notifications", "anime_generation", "INTEGER NOT NULL DEFAULT 0"},
		{"telegram_notifications", "anime_kind", "TEXT NOT NULL DEFAULT ''"},
		{"telegram_notifications", "anime_phase", "TEXT NOT NULL DEFAULT ''"},
		{"telegram_notifications", "anime_block_code", "TEXT NOT NULL DEFAULT ''"},
		{"telegram_notifications", "anime_bot_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, column.table, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("检查通知字段 %s.%s: %w", column.table, column.name, err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE `+column.table+` ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("创建通知字段 %s.%s: %w", column.table, column.name, err)
			}
		}
	}
	var eventSchema string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='anime_notification_events'`).Scan(&eventSchema); err != nil {
		return fmt.Errorf("读取动漫通知约束: %w", err)
	}
	if strings.Contains(strings.Join(strings.Fields(eventSchema), ""), "UNIQUE(job_id,generation,kind,block_code)") {
		// 同一限流码可出现在不同阶段，升级唯一键后仍保留原来的去重记录。
		var previousSequence int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='anime_notification_events'),0)`).Scan(&previousSequence); err != nil {
			return fmt.Errorf("读取动漫通知事件序号: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS anime_notification_downloaded; DROP TRIGGER IF EXISTS anime_notification_organized; DROP TRIGGER IF EXISTS anime_notification_failed; ALTER TABLE anime_notification_events RENAME TO anime_notification_events_previous`); err != nil {
			return fmt.Errorf("准备动漫通知阶段约束: %w", err)
		}
		if _, err := tx.ExecContext(ctx, animeNotificationTablesSchema); err != nil {
			return fmt.Errorf("创建动漫通知阶段约束: %w", err)
		}
		const columns = `id,job_id,subscription_id,generation,kind,phase,title,bangumi_id,snapshot_json,block_code,created_at,bot_id,template,image_mode,image_url,cover_url,processed,failure_count,first_failure_at,last_attempt_at,invalidated`
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_notification_events(`+columns+`) SELECT `+columns+` FROM anime_notification_events_previous; DROP TABLE anime_notification_events_previous`); err != nil {
			return fmt.Errorf("保存动漫通知阶段记录: %w", err)
		}
		// 清空过的旧表仍有已使用序号，重建后不得重新分配给另一条通知。
		if _, err := tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=max(seq,?) WHERE name='anime_notification_events'; INSERT INTO sqlite_sequence(name,seq) SELECT 'anime_notification_events',? WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='anime_notification_events')`, previousSequence, previousSequence); err != nil {
			return fmt.Errorf("保留动漫通知事件序号: %w", err)
		}
		if _, err := tx.ExecContext(ctx, animeNotificationTablesSchema); err != nil {
			return fmt.Errorf("恢复动漫通知索引: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_notification_events AS e SET phase=(SELECT `+animeNotificationPhaseSQL+` FROM anime_download_jobs j WHERE j.id=e.job_id) WHERE kind='failed' AND phase=''`); err != nil {
		return fmt.Errorf("补齐动漫通知阶段: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_notification_events SET first_failure_at=created_at WHERE kind='failed' AND processed=0 AND first_failure_at=0`); err != nil {
		return fmt.Errorf("补齐动漫通知确认窗口: %w", err)
	}
	if _, err := tx.ExecContext(ctx, animeNotificationTriggersSchema); err != nil {
		return fmt.Errorf("创建动漫通知事件触发器: %w", err)
	}
	return nil
}

func (s *Store) GetAnimeNotificationSettings(ctx context.Context) (model.AnimeNotificationSettings, error) {
	var out model.AnimeNotificationSettings
	err := s.db.QueryRowContext(ctx, `SELECT revision,bot_id,download_complete,organized,failed,template,image_mode,image_url FROM anime_notification_settings WHERE id=1`).Scan(&out.Revision, &out.BotID, &out.DownloadComplete, &out.Organized, &out.Failed, &out.Template, &out.ImageMode, &out.ImageURL)
	if err != nil {
		return out, fmt.Errorf("读取动漫通知设置: %w", err)
	}
	return out, nil
}

func (s *Store) SaveAnimeNotificationSettings(ctx context.Context, in model.AnimeNotificationSettings) (model.AnimeNotificationSettings, error) {
	if in.ImageMode == "" {
		in.ImageMode = "cover"
	}
	if in.Revision < 1 || in.Revision >= model.AnimeMaxRevision || len(in.BotID) > 128 || !utf8.ValidString(in.Template) || utf8.RuneCountInString(in.Template) > 4096 || (in.ImageMode != "none" && in.ImageMode != "cover" && in.ImageMode != "custom") || len(in.ImageURL) > 2048 || !utf8.ValidString(in.ImageURL) {
		return model.AnimeNotificationSettings{}, ErrAnimeInvalid
	}
	var out model.AnimeNotificationSettings
	err := s.db.QueryRowContext(ctx, `UPDATE anime_notification_settings SET revision=revision+1,bot_id=?,download_complete=?,organized=?,failed=?,template=?,image_mode=?,image_url=? WHERE id=1 AND revision=? RETURNING revision,bot_id,download_complete,organized,failed,template,image_mode,image_url`, in.BotID, in.DownloadComplete, in.Organized, in.Failed, in.Template, in.ImageMode, in.ImageURL, in.Revision).Scan(&out.Revision, &out.BotID, &out.DownloadComplete, &out.Organized, &out.Failed, &out.Template, &out.ImageMode, &out.ImageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrAnimeConflict
	}
	if err != nil {
		return out, fmt.Errorf("保存动漫通知设置: %w", err)
	}
	return out, nil
}

func (s *Store) BindAnimeNotificationBot(ctx context.Context, expected, id string) error {
	if len(id) > 128 {
		return ErrAnimeInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE anime_notification_settings SET revision=revision+1,bot_id=? WHERE id=1 AND bot_id=? AND revision<?`, id, expected, model.AnimeMaxRevision)
	if err != nil {
		return fmt.Errorf("分配动漫通知 Bot: %w", err)
	}
	return animeChanged(result)
}

func (s *Store) PendingAnimeNotificationEvents(ctx context.Context) ([]model.AnimeNotificationEvent, error) {
	return s.pendingAnimeNotificationEvents(ctx, time.Now())
}

func (s *Store) pendingAnimeNotificationEvents(ctx context.Context, now time.Time) ([]model.AnimeNotificationEvent, error) {
	cutoff := now.Add(-7 * 24 * time.Hour).UnixMilli()
	// 每轮只清理有限数量，去重键保留到任务删除，不继续保存过期任务快照。
	if _, err := s.db.ExecContext(ctx, `UPDATE anime_notification_events SET `+animeNotificationProcessedFields+` WHERE id IN (SELECT id FROM anime_notification_events WHERE processed=0 AND created_at<? ORDER BY created_at,id LIMIT 100)`, cutoff); err != nil {
		return nil, fmt.Errorf("清理过期动漫通知事件: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.job_id,e.generation,e.kind,e.phase,e.title,e.bangumi_id,e.snapshot_json,e.block_code,e.created_at,e.bot_id,e.template,e.image_mode,e.image_url,e.cover_url FROM anime_notification_events e JOIN anime_download_jobs j ON j.id=e.job_id JOIN anime_subscriptions p ON p.id=e.subscription_id WHERE e.processed=0 AND e.created_at>=? AND `+animeNotificationCurrentSQL+` ORDER BY e.id LIMIT 20`, cutoff, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("读取待处理动漫通知: %w", err)
	}
	defer rows.Close()
	out := make([]model.AnimeNotificationEvent, 0, 20)
	for rows.Next() {
		var event model.AnimeNotificationEvent
		var snapshot string
		if err := rows.Scan(&event.ID, &event.JobID, &event.Generation, &event.Kind, &event.Phase, &event.Title, &event.BangumiID, &snapshot, &event.BlockCode, &event.CreatedAt, &event.BotID, &event.Template, &event.ImageMode, &event.ImageURL, &event.CoverURL); err != nil {
			return nil, fmt.Errorf("读取动漫通知快照: %w", err)
		}
		if err := decodeAnimeJSON(snapshot, &event.Snapshot); err != nil {
			return nil, fmt.Errorf("解析动漫通知快照: %w", err)
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历动漫通知快照: %w", err)
	}
	return out, nil
}

// CompleteAnimeNotificationEvent 原子转交给发送队列；重复确认不会重新创建通知。
func (s *Store) CompleteAnimeNotificationEvent(ctx context.Context, eventID int64, n *model.TelegramNotification) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始动漫通知转交: %w", err)
	}
	defer tx.Rollback()
	var event model.AnimeNotificationEvent
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT e.job_id,e.generation,e.kind,e.phase,e.block_code,e.bot_id,(`+animeNotificationCurrentSQL+`) AND c.bot_id=e.bot_id AND CASE e.kind WHEN 'downloaded' THEN c.download_complete WHEN 'organized' THEN c.organized WHEN 'failed' THEN c.failed ELSE 0 END FROM anime_notification_events e JOIN anime_download_jobs j ON j.id=e.job_id JOIN anime_subscriptions p ON p.id=e.subscription_id JOIN anime_notification_settings c ON c.id=1 WHERE e.id=? AND e.processed=0`, time.Now().UnixMilli(), eventID).Scan(&event.JobID, &event.Generation, &event.Kind, &event.Phase, &event.BlockCode, &event.BotID, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("完成动漫通知事件: %w", err)
	}
	if n != nil && current {
		out := *n
		out.AnimeJobID, out.AnimeGeneration, out.AnimeKind, out.AnimeBlockCode, out.AnimeBotID = event.JobID, event.Generation, event.Kind, event.BlockCode, event.BotID
		out.AnimePhase = event.Phase
		if err := enqueueTelegramNotification(ctx, tx, out); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_notification_events SET `+animeNotificationProcessedFields+` WHERE id=?`, eventID); err != nil {
		return fmt.Errorf("清理动漫通知快照: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交动漫通知转交: %w", err)
	}
	return nil
}

const animeNotificationProcessedFields = `processed=1,title='',bangumi_id='',snapshot_json='{}',template='',image_url='',cover_url=''`

// 成熟判断沿用业务终态；可重试读错误需要连续三次观测和五分钟窗口。
const animeNotificationFailureReadySQL = `e.block_code NOT IN ('TASK_RESULT_PENDING','TASK_STATUS_UNKNOWN','TASK_POLL_CANCELLED','DOWNLOAD_PAUSED','SUBSCRIPTION_REMOVED','CANCELLED') AND (
 (e.phase='files' AND j.file_state='needs_review' AND (COALESCE(json_extract(j.raw_status_json,'$.fileReadFailures'),0)>=3 OR e.block_code IN ('FILES_OUTSIDE_TASK','DOWNLOAD_ACCOUNT_CHANGED')))
 OR (j.file_state='needs_review' AND e.block_code NOT IN ('TASK_READ_FAILED','TASK_MISSING','TASK_RATE_LIMITED','TASK_ACCESS_DENIED','FILES_READ_FAILED','FILES_INCOMPLETE','DIRECTORY_READ_FAILED','ORGANIZE_READ_FAILED'))
 OR (e.phase='organize' AND EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind='organize' AND o.generation=j.generation AND json_extract(o.result_json,'$.state')='needs_review' AND json_extract(o.result_json,'$.code')=e.block_code))
 OR e.block_code IN ('TASK_TARGET_CONFLICT','TASK_RESULT_CHANGED','TASK_STATUS_CONFLICT','DOWNLOAD_ACCOUNT_CHANGED','DIRECTORY_CONFLICT','FILES_OUTSIDE_TASK','SUBMIT_REJECTED')
 OR (j.download_state IN ('failed','blocked') AND e.block_code NOT IN ('TASK_READ_FAILED','TASK_MISSING','TASK_RATE_LIMITED','TASK_ACCESS_DENIED','FILES_READ_FAILED','FILES_INCOMPLETE','ORGANIZE_READ_FAILED','ORGANIZE_UNCERTAIN','SUBMIT_UNCERTAIN','DIRECTORY_CREATE_UNCERTAIN','DIRECTORY_READ_FAILED'))
 OR (e.failure_count>=3 AND e.first_failure_at>0 AND e.first_failure_at<=?-300000
 AND e.block_code NOT IN ('FILES_READ_FAILED','FILES_INCOMPLETE','ORGANIZE_UNCERTAIN','ORGANIZE_RESULT_UNRESOLVED','SUBMIT_UNCERTAIN','DIRECTORY_CREATE_UNCERTAIN')
 AND NOT (j.download_state='completed' AND j.file_state IN ('pending','checking'))))`

const animeNotificationCurrentSQL = `j.generation=e.generation AND j.workflow_state<>'cancelled' AND j.download_state<>'cancelled' AND p.status<>'removed' AND CASE e.kind
 WHEN 'downloaded' THEN j.download_state='completed'
 WHEN 'organized' THEN j.workflow_state='organized'
 WHEN 'failed' THEN j.workflow_state='active' AND e.phase=(` + animeNotificationPhaseSQL + `) AND e.block_code=COALESCE(NULLIF(j.block_code,''),CASE WHEN j.download_state IN ('failed','blocked') THEN j.download_state ELSE '' END) AND (` + animeNotificationFailureReadySQL + `)
 ELSE 0 END`
