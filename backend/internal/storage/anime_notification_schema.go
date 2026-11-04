package storage

import "strings"

const animeNotificationPhaseSQL = `CASE WHEN j.file_state='verified' THEN 'organize' WHEN j.download_state='completed' THEN 'files' WHEN COALESCE(j.block_code,'') LIKE 'DIRECTORY_%' OR j.download_state='queued' THEN 'directory' WHEN j.download_state IN ('submitting','unknown','failed','blocked') AND NOT EXISTS(SELECT 1 FROM anime_drive_operations phase_op WHERE phase_op.download_job_id=j.id AND phase_op.kind='submit' AND phase_op.generation=j.generation AND phase_op.state='confirmed') THEN 'submit' ELSE 'download' END`

// 任务状态与通知事件一起提交；目标 Bot 和模板均取事件发生时的设置快照。
const animeNotificationTablesSchema = `
CREATE TABLE IF NOT EXISTS anime_notification_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>0),
 bot_id TEXT NOT NULL DEFAULT '',
 download_complete INTEGER NOT NULL DEFAULT 0 CHECK(download_complete IN (0,1)),
 organized INTEGER NOT NULL DEFAULT 1 CHECK(organized IN (0,1)),
 failed INTEGER NOT NULL DEFAULT 1 CHECK(failed IN (0,1)),
 template TEXT NOT NULL DEFAULT '',
 image_mode TEXT NOT NULL DEFAULT 'cover' CHECK(image_mode IN ('none','cover','custom')),
 image_url TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO anime_notification_settings(id,revision) VALUES(1,1);
CREATE TABLE IF NOT EXISTS anime_notification_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 job_id TEXT NOT NULL REFERENCES anime_download_jobs(id) ON DELETE CASCADE,
 subscription_id TEXT NOT NULL REFERENCES anime_subscriptions(id) ON DELETE CASCADE,
 generation INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('downloaded','organized','failed')),
 phase TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL,
 bangumi_id TEXT NOT NULL,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 block_code TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 bot_id TEXT NOT NULL,
 template TEXT NOT NULL,
 image_mode TEXT NOT NULL DEFAULT 'cover' CHECK(image_mode IN ('none','cover','custom')),
 image_url TEXT NOT NULL DEFAULT '',
 cover_url TEXT NOT NULL DEFAULT '',
 processed INTEGER NOT NULL DEFAULT 0 CHECK(processed IN (0,1)),
 failure_count INTEGER NOT NULL DEFAULT 0,
 first_failure_at INTEGER NOT NULL DEFAULT 0,
 last_attempt_at INTEGER NOT NULL DEFAULT 0,
 invalidated INTEGER NOT NULL DEFAULT 0 CHECK(invalidated IN (0,1)),
 UNIQUE(job_id,generation,kind,phase,block_code)
);
CREATE INDEX IF NOT EXISTS anime_notification_events_subscription ON anime_notification_events(subscription_id);
CREATE INDEX IF NOT EXISTS anime_notification_events_created ON anime_notification_events(created_at,id);
`

var animeNotificationTriggersSchema = `
DROP TRIGGER IF EXISTS anime_notification_downloaded;
CREATE TRIGGER anime_notification_downloaded AFTER UPDATE OF download_state ON anime_download_jobs
WHEN NEW.download_state='completed' AND OLD.download_state<>'completed' AND NEW.workflow_state<>'cancelled' BEGIN
 INSERT OR IGNORE INTO anime_notification_events(job_id,subscription_id,generation,kind,title,bangumi_id,snapshot_json,block_code,created_at,bot_id,template,image_mode,image_url,cover_url)
 SELECT NEW.id,p.id,NEW.generation,'downloaded',e.title,COALESCE(e.bangumi_id,''),NEW.snapshot_json,'',NEW.updated_at,c.bot_id,c.template,c.image_mode,c.image_url,COALESCE(json_extract(e.metadata_json,'$.coverUrl'),'')
 FROM anime_notification_settings c JOIN anime_subscriptions p ON p.id=json_extract(NEW.snapshot_json,'$.source.subscriptionId') JOIN anime_entries e ON e.id=p.entry_id
 WHERE c.id=1 AND c.bot_id<>'' AND c.download_complete=1 AND p.status<>'removed';
END;
DROP TRIGGER IF EXISTS anime_notification_organized;
CREATE TRIGGER anime_notification_organized AFTER UPDATE OF workflow_state ON anime_download_jobs
WHEN NEW.workflow_state='organized' AND OLD.workflow_state<>'organized' BEGIN
 INSERT OR IGNORE INTO anime_notification_events(job_id,subscription_id,generation,kind,title,bangumi_id,snapshot_json,block_code,created_at,bot_id,template,image_mode,image_url,cover_url)
 SELECT NEW.id,p.id,NEW.generation,'organized',e.title,COALESCE(e.bangumi_id,''),NEW.snapshot_json,'',NEW.updated_at,c.bot_id,c.template,c.image_mode,c.image_url,COALESCE(json_extract(e.metadata_json,'$.coverUrl'),'')
 FROM anime_notification_settings c JOIN anime_subscriptions p ON p.id=json_extract(NEW.snapshot_json,'$.source.subscriptionId') JOIN anime_entries e ON e.id=p.entry_id
 WHERE c.id=1 AND c.bot_id<>'' AND c.organized=1 AND p.status<>'removed';
END;
DROP TRIGGER IF EXISTS anime_notification_failed;
CREATE TRIGGER anime_notification_failed AFTER UPDATE OF download_state,workflow_state,file_state,block_code,raw_status_json,directory_operation_json,generation ON anime_download_jobs
WHEN COALESCE(NEW.block_code,'')<>COALESCE(OLD.block_code,'') OR NEW.download_state<>OLD.download_state
 OR NEW.workflow_state<>OLD.workflow_state OR NEW.file_state<>OLD.file_state OR NEW.generation<>OLD.generation
 OR COALESCE(json_extract(NEW.raw_status_json,'$.attemptedAt'),0)>COALESCE(json_extract(OLD.raw_status_json,'$.attemptedAt'),0) BEGIN
 DELETE FROM anime_notification_events WHERE job_id=NEW.id AND kind='failed' AND processed=0
 AND (generation<>NEW.generation OR NEW.workflow_state<>'active' OR NEW.download_state='cancelled'
 OR phase<>(` + strings.ReplaceAll(animeNotificationPhaseSQL, "j.", "NEW.") + `)
 OR block_code<>COALESCE(NULLIF(NEW.block_code,''),CASE WHEN NEW.download_state IN ('failed','blocked') THEN NEW.download_state ELSE '' END));
 UPDATE anime_notification_events SET invalidated=1 WHERE job_id=NEW.id AND kind='failed' AND processed=1
 AND (generation<>NEW.generation OR NEW.workflow_state<>'active' OR NEW.download_state='cancelled'
 OR phase<>(` + strings.ReplaceAll(animeNotificationPhaseSQL, "j.", "NEW.") + `)
 OR block_code<>COALESCE(NULLIF(NEW.block_code,''),CASE WHEN NEW.download_state IN ('failed','blocked') THEN NEW.download_state ELSE '' END));
 INSERT INTO anime_notification_events(job_id,subscription_id,generation,kind,phase,title,bangumi_id,snapshot_json,block_code,created_at,bot_id,template,image_mode,image_url,cover_url,failure_count,first_failure_at,last_attempt_at)
 SELECT NEW.id,p.id,NEW.generation,'failed',(` + strings.ReplaceAll(animeNotificationPhaseSQL, "j.", "NEW.") + `),e.title,COALESCE(e.bangumi_id,''),NEW.snapshot_json,COALESCE(NULLIF(NEW.block_code,''),NEW.download_state),NEW.updated_at,c.bot_id,c.template,c.image_mode,c.image_url,COALESCE(json_extract(e.metadata_json,'$.coverUrl'),''),
 CASE WHEN COALESCE(json_extract(NEW.raw_status_json,'$.attemptedAt'),0)>COALESCE(json_extract(OLD.raw_status_json,'$.attemptedAt'),0) THEN 1 ELSE 0 END,
 NEW.updated_at,COALESCE(json_extract(NEW.raw_status_json,'$.attemptedAt'),0)
 FROM anime_notification_settings c JOIN anime_subscriptions p ON p.id=json_extract(NEW.snapshot_json,'$.source.subscriptionId') JOIN anime_entries e ON e.id=p.entry_id
 WHERE c.id=1 AND c.bot_id<>'' AND c.failed=1 AND p.status<>'removed'
 AND NEW.workflow_state='active' AND NEW.download_state<>'cancelled'
 AND (COALESCE(NEW.block_code,'')<>'' OR NEW.download_state IN ('failed','blocked'))
 AND COALESCE(NEW.block_code,'') NOT IN ('TASK_RESULT_PENDING','TASK_STATUS_UNKNOWN','TASK_POLL_CANCELLED','DOWNLOAD_PAUSED','SUBSCRIPTION_REMOVED','CANCELLED')
 AND (COALESCE(NEW.block_code,'')<>COALESCE(OLD.block_code,'') OR NEW.download_state<>OLD.download_state OR NEW.file_state<>OLD.file_state
 OR COALESCE(json_extract(NEW.raw_status_json,'$.attemptedAt'),0)>COALESCE(json_extract(OLD.raw_status_json,'$.attemptedAt'),0))
 ON CONFLICT(job_id,generation,kind,phase,block_code) DO UPDATE SET
 failure_count=failure_count+CASE WHEN excluded.last_attempt_at>last_attempt_at THEN 1 ELSE 0 END,
 last_attempt_at=max(last_attempt_at,excluded.last_attempt_at)
 WHERE processed=0;
END;
`
