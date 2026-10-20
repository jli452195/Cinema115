-- Current anime schema v53; development runtime reset once, settings retained.

CREATE TABLE IF NOT EXISTS anime_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>0),
 account_id TEXT,
 account_revision INTEGER CHECK(account_revision>0),
 uid TEXT,
 download_cid TEXT,
 download_path TEXT,
 organize_cid TEXT,
 organize_path TEXT,
 CHECK((account_id IS NULL AND account_revision IS NULL AND uid IS NULL
        AND download_cid IS NULL AND download_path IS NULL)
    OR (account_id IS NOT NULL AND length(account_id)>0
        AND account_revision IS NOT NULL AND uid IS NOT NULL AND length(uid)>0
        AND download_cid IS NOT NULL AND length(download_cid)>0
        AND download_path IS NOT NULL)),
 CHECK((organize_cid IS NULL AND organize_path IS NULL)
    OR (account_id IS NOT NULL AND organize_cid IS NOT NULL
        AND length(organize_cid)>0 AND organize_path IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS anime_entries (
 id TEXT PRIMARY KEY NOT NULL,
 bangumi_id TEXT,
 edition_key TEXT NOT NULL,
 title TEXT NOT NULL CHECK(length(title)>0),
 original_title TEXT NOT NULL,
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json)),
 revision INTEGER NOT NULL CHECK(revision>0),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS anime_entries_external ON anime_entries(bangumi_id,edition_key)
 WHERE bangumi_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS anime_subscriptions (
 id TEXT PRIMARY KEY NOT NULL,
 entry_id TEXT NOT NULL REFERENCES anime_entries(id),
 status TEXT NOT NULL CHECK(status IN ('draft','paused','enabled','archived','removed')),
 revision INTEGER NOT NULL CHECK(revision>0),
 run_generation INTEGER NOT NULL CHECK(run_generation>0),
 first_enabled_at INTEGER,
 form_json TEXT NOT NULL CHECK(json_valid(form_json)),
 naming_json TEXT NOT NULL DEFAULT 'null' CHECK(json_valid(naming_json)),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS anime_subscriptions_entry ON anime_subscriptions(entry_id)
 WHERE status<>'removed';

CREATE INDEX IF NOT EXISTS anime_subscriptions_list ON anime_subscriptions(status,updated_at DESC,id DESC);

CREATE TABLE IF NOT EXISTS anime_subscription_sources (
 id TEXT PRIMARY KEY NOT NULL,
 subscription_id TEXT NOT NULL REFERENCES anime_subscriptions(id),
 role TEXT NOT NULL CHECK(role IN ('main','standby')),
 position INTEGER NOT NULL CHECK(position BETWEEN 0 AND 4),
 status TEXT NOT NULL CHECK(status IN ('draft','paused','enabled','removed')),
 run_when_enabled INTEGER NOT NULL CHECK(run_when_enabled IN (0,1)),
 provider TEXT NOT NULL CHECK(provider IN ('mikan','anibt','garden')),
 source_key TEXT,
 item_id TEXT,
 group_id TEXT,
 group_name TEXT,
 namespace_id TEXT NOT NULL,
 binding_json TEXT NOT NULL CHECK(json_valid(binding_json)),
 rules_json TEXT NOT NULL CHECK(json_valid(rules_json)),
 revision INTEGER NOT NULL CHECK(revision>0),
 run_generation INTEGER NOT NULL CHECK(run_generation>0),
 active_baseline_id TEXT REFERENCES anime_source_baselines(id) DEFERRABLE INITIALLY DEFERRED,
 health TEXT NOT NULL CHECK(health IN ('unknown','ok','error','cooldown','unavailable')),
 checked_at INTEGER,
 attempted_at INTEGER,
 check_token TEXT,
 check_until INTEGER,
 poll_failures INTEGER NOT NULL DEFAULT 0 CHECK(poll_failures BETWEEN 0 AND 4),
 poll_snapshot_json TEXT CHECK(poll_snapshot_json IS NULL OR json_valid(poll_snapshot_json)),
 next_check_at INTEGER,
 retry_after INTEGER,
 error_code TEXT,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 CHECK(status IN ('draft','removed') OR
   (source_key IS NOT NULL AND item_id IS NOT NULL AND group_id IS NOT NULL
    AND group_name IS NOT NULL AND length(group_name)>0))
);

CREATE UNIQUE INDEX IF NOT EXISTS anime_sources_main ON anime_subscription_sources(subscription_id)
 WHERE role='main' AND status<>'removed';

CREATE UNIQUE INDEX IF NOT EXISTS anime_sources_key ON anime_subscription_sources(subscription_id,source_key)
 WHERE source_key IS NOT NULL AND status<>'removed';

CREATE INDEX IF NOT EXISTS anime_sources_due ON anime_subscription_sources(status,health,next_check_at);

CREATE TABLE IF NOT EXISTS anime_releases (
 id TEXT PRIMARY KEY NOT NULL,
 resource_id TEXT REFERENCES anime_resources(id),
 provider TEXT NOT NULL CHECK(provider IN ('mikan','anibt','garden')),
 post_key TEXT NOT NULL CHECK(length(post_key)>0),
 original_guid TEXT,
 title TEXT NOT NULL,
 published_raw TEXT,
 published_at INTEGER,
 time_version INTEGER NOT NULL CHECK(time_version>0),
 parse_json TEXT NOT NULL CHECK(json_valid(parse_json)),
 parser_version INTEGER NOT NULL CHECK(parser_version>0),
 content_fingerprint TEXT NOT NULL,
 observed_at INTEGER NOT NULL,
 UNIQUE(provider,post_key)
);

CREATE TABLE IF NOT EXISTS anime_preview_batches (
 id TEXT PRIMARY KEY NOT NULL,
 actor_key TEXT NOT NULL,
 subscription_id TEXT REFERENCES anime_subscriptions(id),
 expected_revision INTEGER NOT NULL CHECK(expected_revision>=0),
 settings_revision INTEGER NOT NULL CHECK(settings_revision>=0),
 config_fingerprint TEXT NOT NULL,
 snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(snapshot_json)),
 created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS anime_source_previews (
 id TEXT PRIMARY KEY NOT NULL,
 batch_id TEXT NOT NULL REFERENCES anime_preview_batches(id),
 source_ref TEXT NOT NULL,
 source_id TEXT REFERENCES anime_subscription_sources(id),
 source_revision INTEGER NOT NULL CHECK(source_revision>=0),
 fingerprint TEXT NOT NULL,
 load_revision INTEGER NOT NULL CHECK(load_revision>0),
 t0 INTEGER NOT NULL,
 generated_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL CHECK(expires_at=generated_at+900000),
 snapshot_observed_at INTEGER NOT NULL,
 snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(snapshot_json)),
 raw_count INTEGER NOT NULL CHECK(raw_count BETWEEN 0 AND 2000),
 requested_take INTEGER NOT NULL CHECK(requested_take BETWEEN 1 AND 2000),
 completeness TEXT NOT NULL CHECK(completeness IN ('partial','visible_end')),
 stop_reason TEXT,
 UNIQUE(batch_id,source_ref)
);

CREATE TABLE IF NOT EXISTS anime_preview_items (
 preview_id TEXT NOT NULL REFERENCES anime_source_previews(id),
 release_id TEXT NOT NULL REFERENCES anime_releases(id),
 ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 selectable INTEGER NOT NULL CHECK(selectable IN (0,1)),
 default_selected INTEGER NOT NULL CHECK(default_selected IN (0,1)),
 decision TEXT NOT NULL,
 reason_code TEXT,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 PRIMARY KEY(preview_id,release_id),
 UNIQUE(preview_id,ordinal),
 CHECK(default_selected<=selectable)
);

CREATE TABLE IF NOT EXISTS anime_source_baselines (
 id TEXT PRIMARY KEY NOT NULL,
 source_id TEXT NOT NULL REFERENCES anime_subscription_sources(id),
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 created_at INTEGER NOT NULL,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json))
);

CREATE TABLE IF NOT EXISTS anime_release_decisions (
 source_id TEXT NOT NULL REFERENCES anime_subscription_sources(id),
 release_id TEXT NOT NULL REFERENCES anime_releases(id),
 disposition TEXT NOT NULL CHECK(disposition IN
   ('seen','filtered','needs_confirmation','selected','linked','dismissed')),
 reason_code TEXT,
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 release_fingerprint TEXT NOT NULL,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 next_verify_at INTEGER,
 observation_json TEXT CHECK(observation_json IS NULL OR json_valid(observation_json)),
 verify_failures INTEGER NOT NULL DEFAULT 0 CHECK(verify_failures BETWEEN 0 AND 4),
 generation INTEGER NOT NULL DEFAULT 1 CHECK(generation>0),
 lease_token TEXT,
 lease_until INTEGER,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(source_id,release_id),
 CHECK((lease_token IS NULL)=(lease_until IS NULL))
);

CREATE INDEX IF NOT EXISTS anime_decisions_verify ON anime_release_decisions(disposition,next_verify_at);

CREATE TABLE IF NOT EXISTS anime_request_keys (
 actor_key TEXT NOT NULL,
 operation TEXT NOT NULL,
 request_key TEXT NOT NULL,
 request_digest TEXT NOT NULL,
 http_status INTEGER NOT NULL CHECK(http_status BETWEEN 200 AND 299),
 receipt_json TEXT NOT NULL CHECK(json_valid(receipt_json)),
 created_at INTEGER NOT NULL,
 PRIMARY KEY(actor_key,operation,request_key)
);
INSERT OR IGNORE INTO anime_settings(id,revision) VALUES(1,1);

CREATE TABLE IF NOT EXISTS anime_resources (
 id TEXT PRIMARY KEY NOT NULL,
 protocol TEXT NOT NULL CHECK(protocol='bt_v1'),
 info_hash TEXT NOT NULL CHECK(length(info_hash)=40 AND info_hash NOT GLOB '*[^0-9a-f]*'),
 verification_version INTEGER NOT NULL CHECK(verification_version>0),
 verified_at INTEGER NOT NULL,
 evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)),
 UNIQUE(protocol,info_hash)
);
CREATE TABLE IF NOT EXISTS anime_resource_claims (
 id TEXT PRIMARY KEY NOT NULL,
 uid TEXT NOT NULL CHECK(length(uid)>0),
 resource_id TEXT NOT NULL REFERENCES anime_resources(id),
 account_id TEXT NOT NULL,
 target_cid TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(uid,resource_id)
);
CREATE TABLE IF NOT EXISTS anime_download_jobs (
 id TEXT PRIMARY KEY NOT NULL,
 claim_id TEXT NOT NULL REFERENCES anime_resource_claims(id),
 attempt INTEGER NOT NULL CHECK(attempt>0),
 account_id TEXT NOT NULL,
 account_revision INTEGER NOT NULL CHECK(account_revision>0),
 uid TEXT NOT NULL,
 settings_revision INTEGER NOT NULL CHECK(settings_revision>0),
 parent_cid TEXT NOT NULL,
 directory_name TEXT NOT NULL,
 directory_cid TEXT,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 download_state TEXT NOT NULL CHECK(download_state IN ('queued','submitting','unknown','accepted','downloading','completed','failed','blocked','cancelled')),
 file_state TEXT NOT NULL CHECK(file_state IN ('pending','checking','verified','needs_review')),
 workflow_state TEXT NOT NULL CHECK(workflow_state IN ('active','original_saved','organized','cancelled')),
 block_code TEXT,
 directory_operation_json TEXT CHECK(directory_operation_json IS NULL OR json_valid(directory_operation_json)),
 files_json TEXT CHECK(files_json IS NULL OR json_valid(files_json)),
 raw_status_json TEXT CHECK(raw_status_json IS NULL OR json_valid(raw_status_json)),
 result_object_id TEXT,
 result_object_kind TEXT CHECK(result_object_kind IN ('file','directory','unknown')),
 revision INTEGER NOT NULL CHECK(revision>0),
 generation INTEGER NOT NULL CHECK(generation>0),
 lease_token TEXT,
 lease_until INTEGER,
 next_run_at INTEGER,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(claim_id,attempt),
 CHECK((lease_token IS NULL)=(lease_until IS NULL)),
 CHECK(workflow_state='active' OR (next_run_at IS NULL AND lease_token IS NULL)),
 CHECK(workflow_state NOT IN ('original_saved','organized') OR (download_state='completed' AND file_state='verified'))
);
CREATE UNIQUE INDEX IF NOT EXISTS anime_jobs_live_claim ON anime_download_jobs(claim_id)
 WHERE workflow_state='active' AND download_state NOT IN ('failed','cancelled');
CREATE INDEX IF NOT EXISTS anime_jobs_due ON anime_download_jobs(workflow_state,next_run_at);
CREATE TABLE IF NOT EXISTS anime_job_selections (
 job_id TEXT NOT NULL REFERENCES anime_download_jobs(id),
 source_id TEXT NOT NULL REFERENCES anime_subscription_sources(id),
 release_id TEXT NOT NULL REFERENCES anime_releases(id),
 status TEXT NOT NULL CHECK(status IN ('authorized','withdrawn')),
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 created_at INTEGER NOT NULL,
 PRIMARY KEY(job_id,source_id,release_id)
);
CREATE INDEX IF NOT EXISTS anime_selections_release ON anime_job_selections(source_id,release_id);

CREATE TABLE IF NOT EXISTS anime_catalog_links (
 provider TEXT NOT NULL CHECK(provider IN ('mikan','anibt','garden')),
 item_id TEXT NOT NULL,
 bangumi_id TEXT,
 observed_at INTEGER NOT NULL CHECK(observed_at>0),
 PRIMARY KEY(provider,item_id)
);
CREATE INDEX IF NOT EXISTS anime_sources_catalog ON anime_subscription_sources(provider,item_id,subscription_id) WHERE status<>'removed';

CREATE TABLE IF NOT EXISTS anime_drive_operations (
 id TEXT PRIMARY KEY NOT NULL,
 download_job_id TEXT NOT NULL REFERENCES anime_download_jobs(id),
 operation_key TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('submit','organize')),
 state TEXT NOT NULL CHECK(state IN ('prepared','dispatched','confirmed','unknown','failed')),
 generation INTEGER NOT NULL CHECK(generation>0),
 request_json TEXT NOT NULL CHECK(json_valid(request_json)),
 result_json TEXT CHECK(result_json IS NULL OR json_valid(result_json)),
 error_code TEXT,
 authorized_at INTEGER,
 updated_at INTEGER NOT NULL,
 UNIQUE(download_job_id,kind),
 CHECK(state NOT IN ('dispatched','confirmed','unknown') OR authorized_at IS NOT NULL)
);


CREATE INDEX IF NOT EXISTS anime_discovery_pending ON anime_release_decisions(disposition,next_verify_at,lease_until) WHERE observation_json IS NOT NULL;
CREATE INDEX IF NOT EXISTS anime_jobs_status_uid ON anime_download_jobs(uid,lease_until,next_run_at) WHERE workflow_state='active' AND file_state<>'verified';
