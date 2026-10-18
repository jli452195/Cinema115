"""Offline P0-05 DDL validation. Never opens the application's database/network."""
from __future__ import annotations

import json
import sqlite3
from pathlib import Path

HERE = Path(__file__).resolve().parent
DOCS = HERE.parents[1]


def insert(db: sqlite3.Connection, table: str, **values: object) -> None:
    # All identifiers are literals in this script; fixture data uses parameters.
    columns = ",".join(values)
    marks = ",".join("?" for _ in values)
    db.execute(f"INSERT INTO {table}({columns}) VALUES({marks})", tuple(values.values()))


def expect_integrity(db: sqlite3.Connection, action, label: str) -> None:
    db.execute("SAVEPOINT invalid_example")
    try:
        action()
    except sqlite3.IntegrityError:
        pass
    else:
        raise AssertionError(f"constraint not enforced: {label}")
    finally:
        db.execute("ROLLBACK TO invalid_example")
        db.execute("RELEASE invalid_example")


def run() -> None:
    fixture = json.loads((HERE / "cases.json").read_text(encoding="utf-8"))
    assert fixture["synthetic"] is True
    case_ids = [case["id"] for case in fixture["serviceCases"]]
    assert len(case_ids) == len(set(case_ids)) == 23
    assert all("input" in case and "expect" in case for case in fixture["serviceCases"])
    sql = (DOCS / "anime-subscription-schema.sql").read_text(encoding="utf-8")
    with sqlite3.connect(":memory:") as db:
        db.executescript(sql)
        now = fixture["now"]
        insert(db, "anime_settings", id=1, revision=1)
        insert(db, "anime_entries", id="entry", edition_key="tv_original", title="Example",
               original_title="Example", metadata_json="{}", revision=1, created_at=now, updated_at=now)
        insert(db, "anime_subscriptions", id="subscription", entry_id="entry", status="paused",
               revision=1, run_generation=1, form_json="{}", created_at=now, updated_at=now)

        def source_row(source, identifier=None, role=None):
            insert(db, "anime_subscription_sources", id=identifier or source["id"],
                   subscription_id="subscription", role=role or source["role"], position=0,
                   status="paused", run_when_enabled=1, provider="mikan",
                   source_key=f"mikan:{source['itemId']}:{source['groupId']}",
                   item_id=source["itemId"], group_id=source["groupId"], group_name=source["groupName"],
                   namespace_id=source["id"], binding_json="{}", rules_json="{}", revision=1,
                   run_generation=1, health="unknown", created_at=now, updated_at=now)

        for source in fixture["sources"]:
            source_row(source)
        expect_integrity(db, lambda: source_row(fixture["sources"][1], "second-main", "main"),
                         "one active main source")
        expect_integrity(db, lambda: source_row(fixture["sources"][0], "duplicate-source", "standby"),
                         "same normalized source")
        expect_integrity(db, lambda: db.execute("UPDATE anime_subscription_sources SET status='enabled' WHERE id='source-a'"),
                         "first source enable needs baseline")
        insert(db, "anime_source_baselines", id="baseline-a", source_id="source-a", source_revision=1,
               t0=now-60000, confirmed_at=now, fingerprint="v1:example", snapshot_json="{}")
        db.execute("UPDATE anime_subscription_sources SET status='enabled',active_baseline_id='baseline-a' WHERE id='source-a'")

        for resource in fixture["resources"]:
            insert(db, "anime_resources", id=resource["id"], protocol="bt_v1", info_hash=resource["hash"],
                   verification_version=1, verified_at=now, evidence_json="{}")
        expect_integrity(db, lambda: insert(db, "anime_resources", id="bad-hash", protocol="bt_v1",
                         info_hash="z" * 40, verification_version=1, verified_at=now, evidence_json="{}"),
                         "hash format")
        for release in fixture["releases"]:
            insert(db, "anime_releases", id=release["id"], provider="mikan", post_key=release["postKey"],
                   original_guid=release["guid"], title=release["title"], time_version=1,
                   parse_json=json.dumps({"rawEpisode": release["episode"]}), parser_version=1,
                   resource_id=release["resourceId"], content_fingerprint=release["id"], observed_at=now)
            insert(db, "anime_release_decisions", source_id=release["sourceId"], release_id=release["id"],
                   disposition="selected", source_revision=1, release_fingerprint=release["id"],
                   snapshot_json="{}", updated_at=now)
        assert db.execute("SELECT count(*) FROM anime_releases WHERE original_guid='colliding-guid'").fetchone()[0] == 2
        expect_integrity(db, lambda: db.execute("UPDATE anime_releases SET post_key='episode-example-a' WHERE id='release-b'"),
                         "original post identity")

        for resource in fixture["resources"]:
            insert(db, "anime_resource_claims", id="claim-" + resource["id"], uid="90000001",
                   resource_id=resource["id"], account_id="primary-example", target_cid="900100", created_at=now)
        expect_integrity(db, lambda: insert(db, "anime_resource_claims", id="duplicate-claim", uid="90000001",
                         resource_id="resource-a", account_id="primary-example", target_cid="900100", created_at=now),
                         "same UID and hash")
        insert(db, "anime_resource_claims", id="other-uid", uid="90000002", resource_id="resource-a",
               account_id="different-primary-snapshot", target_cid="900200", created_at=now)

        def job_row(identifier, resource_id, attempt=1):
            insert(db, "anime_download_jobs", id=identifier, claim_id="claim-" + resource_id, attempt=attempt,
                   account_id="primary-example", account_revision=1, uid="90000001", settings_revision=1,
                   parent_cid="900100", directory_name=identifier, snapshot_json="{}", download_state="queued",
                   file_state="pending", workflow_state="active", revision=1, generation=1,
                   next_run_at=now, created_at=now, updated_at=now)

        for resource in fixture["resources"]:
            job_row("job-" + resource["id"], resource["id"])
        expect_integrity(db, lambda: job_row("duplicate-active", "resource-a", 2), "one active attempt per claim")
        for source_id in ("source-a", "source-mirror"):
            insert(db, "anime_job_selections", job_id="job-resource-a", source_id=source_id,
                   release_id="release-a", status="authorized", source_revision=1,
                   snapshot_json="{}", created_at=now)
        assert db.execute("SELECT count(*) FROM anime_job_selections WHERE job_id='job-resource-a'").fetchone()[0] == 2
        assert db.execute("SELECT count(*) FROM anime_download_jobs").fetchone()[0] == 3

        db.execute("SAVEPOINT retry_example")
        db.execute("UPDATE anime_download_jobs SET download_state='failed' WHERE id='job-resource-a'")
        job_row("new-explicit-attempt", "resource-a", 2)
        db.execute("ROLLBACK TO retry_example")
        db.execute("RELEASE retry_example")
        db.execute("UPDATE anime_download_jobs SET download_state='unknown' WHERE id='job-resource-a'")
        expect_integrity(db, lambda: job_row("unknown-must-not-retry", "resource-a", 2), "unknown attempt retains claim")

        insert(db, "anime_drive_operations", id="submit-operation", download_job_id="job-resource-a",
               operation_key="job-resource-a:submit", kind="submit", state="dispatched", generation=1,
               request_json='{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","cid":"900100"}',
               authorized_at=now, updated_at=now)
        expect_integrity(db, lambda: insert(db, "anime_drive_operations", id="submit-again",
                         download_job_id="job-resource-a", operation_key="job-resource-a:submit", kind="submit",
                         state="prepared", generation=1, request_json="{}", updated_at=now), "single write intent key")
        expect_integrity(db, lambda: db.execute("UPDATE anime_drive_operations SET authorized_at=NULL"),
                         "dispatched operation authorization")

        insert(db, "anime_preview_batches", id="batch", actor_key="admin-example", expected_revision=0,
               settings_revision=1, config_fingerprint="v1:example", created_at=now)
        insert(db, "anime_source_previews", id="preview", batch_id="batch", source_ref="source-a",
               source_id="source-a", source_revision=1, fingerprint="v1:example", load_revision=1,
               t0=now-60000, generated_at=now, expires_at=now+900000, snapshot_observed_at=now-60000,
               raw_count=3, requested_take=50, completeness="visible_end")
        expect_integrity(db, lambda: db.execute("UPDATE anime_source_previews SET expires_at=expires_at+60000"),
                         "preview fixed absolute duration")

        insert(db, "anime_files", id="file-a", uid="90000001", file_id="900300", parent_cid="900100",
               name="original.mkv", size=12345, sha1="d" * 40, verified_at=now)
        insert(db, "anime_job_files", job_id="job-resource-a", file_id="file-a", role="video", selected=1, original_json="{}")
        expect_integrity(db, lambda: db.execute("UPDATE anime_download_jobs SET workflow_state='original_saved' WHERE id='job-resource-a'"),
                         "terminal requires completed download and verified files")
        db.execute("UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='original_saved',next_run_at=NULL WHERE id='job-resource-a'")
        expect_integrity(db, lambda: db.execute("UPDATE anime_download_jobs SET next_run_at=? WHERE id='job-resource-a'", (now,)),
                         "terminal has no scheduled work")

        insert(db, "anime_naming_plans", id="plan", subscription_id="subscription", revision=1,
               tmdb_id="900999", numbering_kind="default", target_snapshot_json="{}", template_version=1,
               fingerprint="v1:plan", created_at=now)
        for source in fixture["sources"]:
            insert(db, "anime_naming_mappings", plan_id="plan", source_id=source["id"], namespace_id=source["id"],
                   source_revision=1, mapping_json=json.dumps({"sourceBase": source["sourceBase"], "targetSeason": 2, "targetBase": 1}))
        insert(db, "anime_postprocess_jobs", id="postprocess", download_job_id="job-resource-a", plan_id="plan",
               application_key="explicit-example", account_id="primary-example", account_revision=1, uid="90000001",
               target_cid="900400", snapshot_json="{}", state="blocked", revision=1, generation=1, created_at=now, updated_at=now)
        insert(db, "anime_postprocess_items", id="item", postprocess_job_id="postprocess", file_id="file-a",
               expected_json="{}", target_directories_json='["Example (2026) {tmdb-900999}","Season 02"]',
               target_name="Example.S02E06.GroupA.mkv", state="conflict", reason_code="TARGET_NAME_CONFLICT", updated_at=now)
        assert db.execute("SELECT parent_cid,name FROM anime_files WHERE id='file-a'").fetchone() == ("900100", "original.mkv")
        assert db.execute("SELECT workflow_state FROM anime_download_jobs WHERE id='job-resource-a'").fetchone()[0] == "original_saved"

        insert(db, "anime_request_keys", actor_key="admin-example", operation="POST:/subscriptions",
               request_key="request-example", request_digest="digest-a", http_status=201, receipt_json="{}", created_at=now)
        expect_integrity(db, lambda: insert(db, "anime_request_keys", actor_key="admin-example", operation="POST:/subscriptions",
                         request_key="request-example", request_digest="digest-b", http_status=201, receipt_json="{}", created_at=now),
                         "durable request key scope")
        assert db.execute("UPDATE anime_subscriptions SET revision=revision+1 WHERE id='subscription' AND revision=1").rowcount == 1
        assert db.execute("UPDATE anime_subscriptions SET revision=revision+1 WHERE id='subscription' AND revision=1").rowcount == 0
        expect_integrity(db, lambda: db.execute("DELETE FROM anime_entries WHERE id='entry'"), "no cascading deletion of history")
        assert db.execute("PRAGMA foreign_key_check").fetchall() == []
        assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
        table_count = db.execute("SELECT count(*) FROM sqlite_master WHERE type='table'").fetchone()[0]
        assert table_count == 24
        db.commit()
    print(f"PASS: SQLite {sqlite3.sqlite_version}; {table_count} contract tables; 15 negative constraint checks.")
    print("PASS: GUID collision, same-episode multi-version records, multi-source links, distinct UID claims,")
    print("      explicit attempt history, terminal scheduling, revision CAS, conflict preservation, FK/integrity checks.")
    print(f"Fixtures: {len(case_ids)} future service/API scenarios are specified, NOT executed against production code.")
    print("Scope: :memory: database only; no application migration, HTTP requests, cloud writes or Go/page acceptance.")


if __name__ == "__main__":
    run()
