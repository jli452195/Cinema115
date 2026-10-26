package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func animeSourceRemovalInput(t *testing.T, f *downloadClaimFixture, id string) (model.AnimeSubscriptionConfig, AnimeConfigGuard) {
	t.Helper()
	ctx := context.Background()
	sub, err := f.store.GetAnimeSubscription(ctx, f.sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := f.store.GetAnimeEntry(ctx, sub.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := f.store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := model.AnimeSubscriptionConfig{ID: sub.ID, EntryID: sub.EntryID, ExpectedRevision: sub.Revision, Status: sub.Status, Form: sub.Form, Naming: sub.Naming}
	for _, source := range sub.Sources {
		if source.ID == id {
			c.RemovedSources = append(c.RemovedSources, model.AnimeSourceRemoval{ID: id, ExpectedRevision: source.Revision})
		} else {
			c.Sources = append(c.Sources, sourceEdit(source, source.ID))
		}
	}
	return c, AnimeConfigGuard{Entry: entry, Settings: settings, RequireAccount: true, Runtime: true}
}

func TestAnimeSourceDeletePurgesHistoryAndResubscribeCreatesNewJob(t *testing.T) {
	f, mainJob := animeSubmitStoreFixture(t)
	ctx := context.Background()
	source := f.sub.Sources[1]
	release, verified := f.selectRelease(t, 1, "rel_backup_delete", strings.Repeat("b", 40))
	job := f.claim(t, 1, release, verified).Job
	beforeMain, err := f.store.GetAnimeDownloadJob(ctx, mainJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Isolated preview records cover dependent FK cleanup, including a batch
	// containing a previously saved source and its globally stored release.
	batchID, previewID := strings.Repeat("c", 32), strings.Repeat("d", 32)
	if _, err := f.store.db.Exec(`INSERT INTO anime_preview_batches(id,actor_key,subscription_id,expected_revision,settings_revision,config_fingerprint,created_at) VALUES(?,'admin',?,1,1,'fixture',1)`, batchID, f.sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`INSERT INTO anime_source_previews(id,batch_id,source_ref,source_id,source_revision,fingerprint,load_revision,t0,generated_at,expires_at,snapshot_observed_at,raw_count,requested_take,completeness) VALUES(?,?,?, ?,1,'fixture',1,1,1,900001,1,1,1,'visible_end')`, previewID, batchID, source.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`INSERT INTO anime_preview_items(preview_id,release_id,ordinal,selectable,default_selected,decision,snapshot_json) VALUES(?,?,0,1,1,'matched','{}')`, previewID, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`INSERT INTO anime_source_activity VALUES(?,1,1,1)`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`INSERT INTO anime_drive_operations(id,download_job_id,operation_key,kind,state,generation,request_json,result_json,updated_at) VALUES(?,?,?,'organize','failed',1,'{}','{}',1)`, strings.Repeat("e", 32), job.ID, "fixture_organize"); err != nil {
		t.Fatal(err)
	}
	c, guard := animeSourceRemovalInput(t, f, source.ID)
	key := animeAdminKey("source_delete_replay")
	first, err := f.store.SaveAnimeConfiguration(ctx, c, key, guard)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.store.SaveAnimeConfiguration(ctx, c, key, guard)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("lost save receipt cannot replay", again, err)
	}
	if _, err := f.store.GetAnimeSource(ctx, source.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted source remains", err)
	}
	if _, err := f.store.GetAnimeDownloadJob(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted source task remains", err)
	}
	for _, table := range []string{"anime_source_progress", "anime_source_activity", "anime_job_selections", "anime_release_decisions"} {
		var count int
		if err := f.store.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE source_id=?`, source.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("source history remains", table, count, err)
		}
	}
	for _, table := range []string{"anime_drive_operations", "anime_preview_batches", "anime_source_previews", "anime_preview_items"} {
		var count int
		if err := f.store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("source dependent records remain", table, count, err)
		}
	}
	assertDownloadCounts(t, f.store, 1, 1, 1, 1)
	afterMain, err := f.store.GetAnimeDownloadJob(ctx, mainJob.ID)
	if err != nil || !reflect.DeepEqual(beforeMain, afterMain) {
		t.Fatal("unrelated task changed", afterMain, err)
	}
	page, err := f.store.ListAnimeDownloadJobs(ctx, f.sub.ID, 1, 20)
	if err != nil || page.Total != 1 || page.Items[0].ID != mainJob.ID {
		t.Fatal("deleted task still listed", page, err)
	}
	editAnimeDefinition(t, f, func(c *model.AnimeSubscriptionConfig) {
		replacement := sourceEdit(source, "replacement")
		replacement.ID, replacement.ExpectedRevision = "", 0
		c.Sources = append(c.Sources, replacement)
	})
	f.sub, err = f.store.GetAnimeSubscription(ctx, f.sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	release, verified = f.selectRelease(t, 1, "rel_backup_delete", strings.Repeat("b", 40))
	next := f.claim(t, 1, release, verified)
	if !next.Created || next.Job.ID == job.ID || next.Job.Snapshot.Source.NamespaceID == source.NamespaceID {
		t.Fatal("resubscription inherited the deleted job", next)
	}
	assertDownloadCounts(t, f.store, 2, 2, 2, 2)
	rows, err := f.store.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("foreign key violation", rows.Err())
	}
}

func TestAnimeSourceDeletePreservesSharedTaskAndAuthorization(t *testing.T) {
	for _, crossSubscription := range []bool{false, true} {
		t.Run(map[bool]string{false: "same subscription", true: "other subscription"}[crossSubscription], func(t *testing.T) {
			f := newDownloadClaimFixture(t)
			ctx := context.Background()
			source := f.sub.Sources[1]
			release, verified := f.selectRelease(t, 1, "rel_shared_original", strings.Repeat("a", 40))
			job := f.claim(t, 1, release, verified).Job
			other := f
			if crossSubscription {
				entry := createAnimeEntry(t, f.store, nil)
				config := animeConfig(entry.ID)
				config.Sources = []model.AnimeSourceConfig{sourceEdit(f.sub.Sources[0], "main")}
				config.Sources[0].ID, config.Sources[0].ExpectedRevision, config.Sources[0].Status = "", 0, "paused"
				sub := saveAnimeConfig(t, f.store, config)
				if _, err := f.store.db.Exec(`UPDATE anime_subscriptions SET status='enabled',first_enabled_at=1 WHERE id=?`, sub.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='enabled' WHERE subscription_id=?`, sub.ID); err != nil {
					t.Fatal(err)
				}
				sub, err := f.store.GetAnimeSubscription(ctx, sub.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				other = &downloadClaimFixture{store: f.store, target: f.target, sub: sub}
			}
			release, verified = other.selectRelease(t, 0, "rel_shared_remaining", job.Snapshot.InfoHash)
			if other.claim(t, 0, release, verified).Job.ID != job.ID {
				t.Fatal("fixture did not share a task")
			}
			if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET directory_cid='200' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			c, guard := animeSourceRemovalInput(t, f, source.ID)
			if _, err := f.store.SaveAnimeConfiguration(ctx, c, animeAdminKey("delete_shared_source"), guard); err != nil {
				t.Fatal(err)
			}
			after, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("shared task or frozen settings changed", after, err)
			}
			assertDownloadCounts(t, f.store, 1, 1, 1, 1)
			if err := f.store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
				t.Fatal("remaining source lost write authorization", err)
			}
			page, err := f.store.ListAnimeDownloadJobs(ctx, other.sub.ID, 1, 20)
			if err != nil || page.Total != 1 {
				t.Fatal("remaining source lost its task", page, err)
			}
			if crossSubscription {
				page, err = f.store.ListAnimeDownloadJobs(ctx, f.sub.ID, 1, 20)
				if err != nil || page.Total != 0 {
					t.Fatal("deleted source still exposes another subscription's task", page, err)
				}
			}
		})
	}
}

func TestAnimeSourceDeleteBusyAndFailedReceiptRollback(t *testing.T) {
	for _, mode := range []string{"worker lease", "resource verification", "receipt failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newDownloadClaimFixture(t)
			ctx := context.Background()
			source := f.sub.Sources[1]
			release, verified := f.selectRelease(t, 1, "rel_delete_rollback", strings.Repeat("a", 40))
			job := f.claim(t, 1, release, verified).Job
			if mode == "worker lease" {
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET lease_token='fixture_busy',lease_until=? WHERE id=?`, time.Now().Add(time.Minute).UnixMilli(), job.ID); err != nil {
					t.Fatal(err)
				}
			} else if mode == "resource verification" {
				if _, err := f.store.db.Exec(`UPDATE anime_release_decisions SET lease_token='fixture_verifying',lease_until=? WHERE source_id=?`, time.Now().Add(time.Minute).UnixMilli(), source.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.store.db.Exec(`CREATE TRIGGER source_delete_receipt_failure BEFORE INSERT ON anime_request_keys BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			c, guard := animeSourceRemovalInput(t, f, source.ID)
			key := animeAdminKey("delete_source_rollback")
			if _, err := f.store.SaveAnimeConfiguration(ctx, c, key, guard); err == nil || mode != "receipt failure" && !errors.Is(err, ErrAnimeDeleteBusy) {
				t.Fatal("unsafe/failed save was accepted", err)
			}
			sub, err := f.store.GetAnimeSubscription(ctx, f.sub.ID, true)
			if err != nil || !reflect.DeepEqual(sub, f.sub) {
				t.Fatal("failed deletion changed configuration", sub, err)
			}
			after, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed deletion erased the job", after, err)
			}
			assertDownloadCounts(t, f.store, 1, 1, 1, 1)
			if mode == "worker lease" {
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET lease_token=NULL,lease_until=NULL WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			} else if mode == "resource verification" {
				if _, err := f.store.db.Exec(`UPDATE anime_release_decisions SET lease_token=NULL,lease_until=NULL WHERE source_id=?`, source.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.store.db.Exec(`DROP TRIGGER source_delete_receipt_failure`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.store.SaveAnimeConfiguration(ctx, c, key, guard); err != nil {
				t.Fatal("original save cannot retry after recovery", err)
			}
		})
	}
}

func TestAnimeSourceDeleteFencesLateRSSResponses(t *testing.T) {
	f, now := discoveryFixture(t)
	ctx := context.Background()
	sourceID := f.sub.Sources[1].ID
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id<>?`, now+3600000, sourceID); err != nil {
		t.Fatal(err)
	}
	poll := discoveryPoll(t, f.store, now)
	if poll.Source.ID != sourceID {
		t.Fatal("wrong source reserved", poll.Source.ID)
	}
	c, guard := animeSourceRemovalInput(t, f, sourceID)
	if _, err := f.store.SaveAnimeConfiguration(ctx, c, animeAdminKey("delete_polling_source"), guard); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now, discoveryRow("rel_deleted_late", now)), now); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("late RSS response survived deletion", err)
	}
	if err := f.store.FailAnimeSourcePoll(ctx, poll, "SOURCE_READ_FAILED", 0, false, now); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("late failure survived deletion", err)
	}
	var count int
	if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_releases WHERE post_key='rel_deleted_late'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("late response recreated deleted history", count, err)
	}
}
