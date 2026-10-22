package storage

// Triggers only invalidate a small materialized result. No title parsing or
// history scan runs in a write callback or a list request. Generations fence
// results computed while another transaction changes the source's inputs.
const animeProgressSchema = `
CREATE TABLE IF NOT EXISTS anime_source_progress (
 source_id TEXT PRIMARY KEY REFERENCES anime_subscription_sources(id) ON DELETE CASCADE,
 generation INTEGER NOT NULL DEFAULT 1,
 computed_generation INTEGER NOT NULL DEFAULT 0,
 summary_json TEXT NOT NULL DEFAULT '{"state":"unknown"}' CHECK(json_valid(summary_json))
);
CREATE INDEX IF NOT EXISTS anime_progress_dirty ON anime_source_progress(source_id) WHERE generation<>computed_generation;
INSERT OR IGNORE INTO anime_source_progress(source_id) SELECT id FROM anime_subscription_sources WHERE status<>'removed';
CREATE TRIGGER IF NOT EXISTS anime_progress_source_insert AFTER INSERT ON anime_subscription_sources BEGIN
 INSERT INTO anime_source_progress(source_id) VALUES(NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_source_update AFTER UPDATE OF rules_json,binding_json,checked_at ON anime_subscription_sources
WHEN OLD.rules_json<>NEW.rules_json OR OLD.binding_json<>NEW.binding_json OR (OLD.checked_at IS NULL)<>(NEW.checked_at IS NULL) BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id=NEW.id;
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_subscription_update AFTER UPDATE OF form_json,naming_json ON anime_subscriptions
WHEN OLD.form_json<>NEW.form_json OR OLD.naming_json<>NEW.naming_json BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id IN (SELECT id FROM anime_subscription_sources WHERE subscription_id=NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_entry_update AFTER UPDATE OF metadata_json ON anime_entries
WHEN json_extract(OLD.metadata_json,'$.totalEpisodes') IS NOT json_extract(NEW.metadata_json,'$.totalEpisodes') BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id IN (SELECT s.id FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE p.entry_id=NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_decision_insert AFTER INSERT ON anime_release_decisions BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id=NEW.source_id;
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_decision_update AFTER UPDATE OF snapshot_json ON anime_release_decisions WHEN OLD.snapshot_json<>NEW.snapshot_json BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id=NEW.source_id;
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_decision_delete AFTER DELETE ON anime_release_decisions BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id=OLD.source_id;
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_selection_insert AFTER INSERT ON anime_job_selections BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id=NEW.source_id;
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_selection_update AFTER UPDATE OF status,snapshot_json ON anime_job_selections
WHEN OLD.status<>NEW.status OR OLD.snapshot_json<>NEW.snapshot_json BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id=NEW.source_id;
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_selection_delete AFTER DELETE ON anime_job_selections BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id=OLD.source_id;
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_job_update AFTER UPDATE OF workflow_state ON anime_download_jobs
WHEN OLD.workflow_state<>NEW.workflow_state AND (OLD.workflow_state='organized' OR NEW.workflow_state='organized') BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id IN (SELECT source_id FROM anime_job_selections WHERE job_id=NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS anime_progress_job_insert AFTER INSERT ON anime_download_jobs BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id IN (SELECT x.source_id FROM anime_job_selections x JOIN anime_download_jobs j ON j.id=x.job_id WHERE j.claim_id=NEW.claim_id AND j.workflow_state='organized');
END;
`
