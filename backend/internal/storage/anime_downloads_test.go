package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
)

type downloadClaimFixture struct {
	store  *Store
	target model.AnimeDownloadTarget
	sub    model.AnimeSubscription
}

func newDownloadClaimFixture(t *testing.T) *downloadClaimFixture {
	t.Helper()
	s, target := animeDownloadTargetFixture(t)
	e := createAnimeEntry(t, s, nil)
	c := animeConfig(e.ID)
	// Distinct provider sources can report the same publisher and BT resource.
	c.Sources[0].Provider = "garden"
	c.Sources[0].ItemID = animePtr("123")
	c.Sources[0].GroupID = animePtr("dmhy_f45")
	c.Sources[0].GroupName = animePtr("字幕组")
	c.Sources[0].Rules.Match = []string{"(?i)hevc"}
	backup := c.Sources[0]
	backup.Ref = "backup"
	backup.Role = "standby"
	backup.Position = 1
	backup.GroupID = animePtr("mikan_f45")
	c.Sources = append(c.Sources, backup)
	sub := saveAnimeConfig(t, s, c)
	ctx := context.Background()
	// These disposable fixtures isolate authoritative pending selections from
	// the background discovery worker.
	for _, source := range sub.Sources {

		if _, err := s.db.ExecContext(ctx, `UPDATE anime_subscription_sources SET status='enabled' WHERE id=?`, source.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE anime_subscriptions SET status='enabled',first_enabled_at=1 WHERE id=?`, sub.ID); err != nil {
		t.Fatal(err)
	}
	sub, err := s.GetAnimeSubscription(ctx, sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return &downloadClaimFixture{store: s, target: target, sub: sub}
}

func (f *downloadClaimFixture) selectRelease(t *testing.T, index int, post, hash string) (model.AnimeRelease, animeresource.Verified) {
	return f.selectTitle(t, index, post, hash, "[字幕组] 测试 - 30 [1080p][HEVC]")
}

func (f *downloadClaimFixture) selectTitle(t *testing.T, index int, post, hash, title string) (model.AnimeRelease, animeresource.Verified) {
	t.Helper()
	ctx := context.Background()
	source := f.sub.Sources[index]
	row := animefeed.SourceRelease{PostKey: post, Title: title, OriginalGUID: "shared-guid", MagnetHint: "magnet:?xt=urn:btih:" + hash}
	parsed, err := animeparse.Title(row.Title)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := animeresource.Fingerprint(row)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := f.store.SaveAnimeReleases(ctx, []model.AnimeRelease{{Provider: "garden", PostKey: post, Title: row.Title, OriginalGUID: animePtr(row.OriginalGUID), Parse: parsed, ParserVersion: animeparse.ParserVersion, TimeVersion: animeparse.TimeVersion, ContentFingerprint: fp, ObservedAt: time.Now().UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	release := releases[0]
	d := animeDecision(source, release, "selected")
	d.Snapshot.Selection = &model.AnimeSelectionBasis{SelectedAt: time.Now().UnixMilli(), Target: f.target, SubscriptionGeneration: f.sub.RunGeneration, SourceGeneration: source.RunGeneration}
	d.Snapshot.RuleVersion, d.Snapshot.ParserVersion, d.Snapshot.TimeVersion = animepreview.RuleVersion, release.ParserVersion, release.TimeVersion
	raw, err := animeJSON(d.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `INSERT INTO anime_release_decisions(source_id,release_id,disposition,source_revision,release_fingerprint,snapshot_json,generation,updated_at) VALUES(?,?,'selected',?,?,?,1,1) ON CONFLICT(source_id,release_id) DO NOTHING`, source.ID, release.ID, source.Revision, release.ContentFingerprint, raw); err != nil {
		t.Fatal(err)
	}
	v, err := animeresource.Verify(ctx, "garden", row, nil)
	if err != nil {
		t.Fatal(err)
	}
	return release, v
}

func (f *downloadClaimFixture) claim(t *testing.T, index int, r model.AnimeRelease, v animeresource.Verified) model.AnimeDownloadClaimResult {
	t.Helper()
	source := f.sub.Sources[index]
	d, err := f.store.GetAnimeReleaseDecision(context.Background(), source.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.store.ClaimAnimeDownload(context.Background(), source.ID, r.ID, d.Generation, v, f.target)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertDownloadCounts(t *testing.T, s *Store, resources, claims, jobs, selections int) {
	t.Helper()
	for table, want := range map[string]int{"anime_resources": resources, "anime_resource_claims": claims, "anime_download_jobs": jobs, "anime_job_selections": selections} {
		var n int
		if err := s.db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != want {
			t.Fatal(table, n, want, err)
		}
	}
}

func TestAnimeDownloadClaimConcurrentSourcesSameHash(t *testing.T) {
	f := newDownloadClaimFixture(t)
	hash := strings.Repeat("a", 40)
	r0, v0 := f.selectRelease(t, 0, "rel_dmhy_1", hash)
	r1, v1 := f.selectRelease(t, 1, "rel_mikan_2", hash)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan model.AnimeDownloadClaimResult, 24)
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			index, r, v := 0, r0, v0
			if i%2 == 1 {
				index, r, v = 1, r1, v1
			}
			// Same source choice may already have transitioned to linked; read its
			// current CAS generation. Losing an older generation is a safe conflict.
			d, err := f.store.GetAnimeReleaseDecision(context.Background(), f.sub.Sources[index].ID, r.ID)
			if err != nil {
				errs <- err
				return
			}
			out, err := f.store.ClaimAnimeDownload(context.Background(), f.sub.Sources[index].ID, r.ID, d.Generation, v, f.target)
			if errors.Is(err, ErrAnimeConflict) {
				return
			}
			if err != nil {
				errs <- err
				return
			}
			results <- out
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	created := 0
	var id string
	for out := range results {
		if out.Created {
			created++
		}
		if id != "" && id != out.Job.ID {
			t.Fatal("duplicate job")
		}
		id = out.Job.ID
	}
	if created != 1 {
		t.Fatal("not exactly one new intent", created)
	}
	assertDownloadCounts(t, f.store, 1, 1, 1, 2)
	if _, err := f.store.db.ExecContext(context.Background(), `DELETE FROM anime_resources`); err == nil {
		t.Fatal("resource history lost through FK bypass")
	}
}

func TestAnimeDownloadClaimIndependentGroupsButNeverReplacesEdition(t *testing.T) {
	f := newDownloadClaimFixture(t)
	// Different groups remain independent; a later correction of the first
	// group's established edition cannot create another download.
	if _, err := f.store.db.ExecContext(context.Background(), `UPDATE anime_subscription_sources SET group_name='另一字幕组' WHERE id=?`, f.sub.Sources[1].ID); err != nil {
		t.Fatal(err)
	}
	f.sub.Sources[1].GroupName = animePtr("另一字幕组")
	var jobs []string
	for i := 0; i < 2; i++ {
		r, v := f.selectRelease(t, i%2, fmt.Sprintf("rel_dmhy_%d", i+1), strings.Repeat(fmt.Sprint(i+1), 40))
		out := f.claim(t, i%2, r, v)
		if !out.Created {
			t.Fatal("episode exclusivity rejected version")
		}
		jobs = append(jobs, out.Job.ID)
	}
	if jobs[0] == jobs[1] {
		t.Fatal("different hash reused job")
	}
	r, v := f.selectTitle(t, 0, "rel_dmhy_v3", strings.Repeat("3", 40), "[字幕组] 测试 - 30v3 [1080p][HEVC]")
	if _, err := f.store.ClaimAnimeDownload(context.Background(), f.sub.Sources[0].ID, r.ID, 1, v, f.target); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("later revision created another download", err)
	}
	assertDownloadCounts(t, f.store, 2, 2, 2, 2)
}

func TestAnimeDownloadClaimNeverReopensKnownResource(t *testing.T) {
	for _, state := range []string{"queued", "unknown", "failed", "cancelled", "original_saved"} {
		t.Run(state, func(t *testing.T) {
			f := newDownloadClaimFixture(t)
			r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
			first := f.claim(t, 0, r, v)
			query := `UPDATE anime_download_jobs SET download_state=? WHERE id=?`
			value := state
			if state == "original_saved" {
				query = `UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state=? WHERE id=?`
			}
			if _, err := f.store.db.ExecContext(context.Background(), query, value, first.Job.ID); err != nil {
				t.Fatal(err)
			}
			second := f.claim(t, 0, r, v)
			if second.Created || second.Job.ID != first.Job.ID || second.Job.Attempt != 1 {
				t.Fatal("automatically retried old claim")
			}
			assertDownloadCounts(t, f.store, 1, 1, 1, 1)
		})
	}
}

func TestAnimeDownloadClaimRollbackAfterPartialWrite(t *testing.T) {
	f := newDownloadClaimFixture(t)
	r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
	if _, err := f.store.db.ExecContext(context.Background(), `CREATE TRIGGER fail_anime_selection BEFORE INSERT ON anime_job_selections BEGIN SELECT RAISE(ABORT,'synthetic selection failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.ClaimAnimeDownload(context.Background(), f.sub.Sources[0].ID, r.ID, 1, v, f.target)
	if err == nil {
		t.Fatal("injected failure did not fail")
	}
	assertDownloadCounts(t, f.store, 0, 0, 0, 0)
	var resource *string
	if err := f.store.db.QueryRowContext(context.Background(), `SELECT resource_id FROM anime_releases WHERE id=?`, r.ID).Scan(&resource); err != nil || resource != nil {
		t.Fatal("release association escaped transaction", err)
	}
	d, err := f.store.GetAnimeReleaseDecision(context.Background(), f.sub.Sources[0].ID, r.ID)
	if err != nil || d.Disposition != "selected" || d.Generation != 1 {
		t.Fatal("selection lost on failed commit", err)
	}
}

func TestAnimeDownloadClaimRejectsStaleOrWithdrawnAuthorization(t *testing.T) {
	for _, change := range []string{
		`UPDATE anime_subscriptions SET status='paused'`,
		`UPDATE anime_subscriptions SET run_generation=run_generation+1`,
		`UPDATE anime_subscription_sources SET status='paused'`,
		`UPDATE anime_subscription_sources SET run_when_enabled=0`,
		`UPDATE anime_subscription_sources SET revision=revision+1`,
		`UPDATE anime_subscription_sources SET run_generation=run_generation+1`,
		`UPDATE anime_release_decisions SET disposition='dismissed'`,
		`UPDATE anime_release_decisions SET generation=generation+1`,
		`UPDATE anime_release_decisions SET disposition='seen'`,
		`UPDATE anime_releases SET content_fingerprint='changed'`,
		`UPDATE anime_settings SET revision=revision+1,download_cid='999'`,
		`UPDATE p115_accounts SET revision=revision+1`,
	} {
		t.Run(change, func(t *testing.T) {
			f := newDownloadClaimFixture(t)
			r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
			if _, err := f.store.db.ExecContext(context.Background(), change); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ClaimAnimeDownload(context.Background(), f.sub.Sources[0].ID, r.ID, 1, v, f.target); err == nil {
				t.Fatal("stale execution authorized")
			}
			assertDownloadCounts(t, f.store, 0, 0, 0, 0)
		})
	}
}

func TestAnimeDownloadClaimConflictsPreserveOriginal(t *testing.T) {
	for _, conflict := range []string{"directory", "account config", "publisher", "identity"} {
		t.Run(conflict, func(t *testing.T) {
			f := newDownloadClaimFixture(t)
			r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
			first := f.claim(t, 0, r, v)
			r2, v2 := f.selectRelease(t, 1, "rel_mikan_2", strings.Repeat("a", 40))
			ctx := context.Background()
			switch conflict {
			case "directory":
				if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_resource_claims SET target_cid='999'`); err != nil {
					t.Fatal(err)
				}
			case "account config":
				if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_resource_claims SET account_id='other-config'`); err != nil {
					t.Fatal(err)
				}
			case "publisher":
				if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_subscription_sources SET group_name='冲突字幕组' WHERE id=?`, f.sub.Sources[1].ID); err != nil {
					t.Fatal(err)
				}
			case "identity":
				r2 = r
				_, v2 = f.selectRelease(t, 1, "rel_mikan_3", strings.Repeat("b", 40))
			}
			if _, err := f.store.ClaimAnimeDownload(ctx, f.sub.Sources[1].ID, r2.ID, 1, v2, f.target); err == nil {
				t.Fatal("conflict silently linked")
			}
			assertDownloadCounts(t, f.store, 1, 1, 1, 1)
			got, err := f.store.GetAnimeDownloadJob(ctx, first.Job.ID)
			if err != nil || !reflect.DeepEqual(got, first.Job) {
				t.Fatal("canonical task changed", err)
			}
		})
	}
}

func TestAnimeDownloadClaimRulesUpgradePreservesOldTask(t *testing.T) {
	f := newDownloadClaimFixture(t)
	r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
	first := f.claim(t, 0, r, v)
	ctx := context.Background()
	source := f.sub.Sources[0]
	source.Revision++
	source.Rules.MinimumEpisode = animePtr(25)
	raw, _ := animeJSON(source.Rules)
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_subscription_sources SET revision=?,rules_json=? WHERE id=?`, source.Revision, raw, source.ID); err != nil {
		t.Fatal(err)
	}
	d, err := f.store.GetAnimeReleaseDecision(ctx, source.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Snapshot.Rules = source.Rules
	raw, _ = animeJSON(d.Snapshot)
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_release_decisions SET source_revision=?,snapshot_json=?,generation=generation+1 WHERE source_id=? AND release_id=?`, source.Revision, raw, source.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	out, err := f.store.ClaimAnimeDownload(ctx, source.ID, r.ID, 2, v, f.target)
	if err != nil || out.Created || !reflect.DeepEqual(out.Job, first.Job) {
		t.Fatal("rule upgrade changed fixed task", err)
	}
	assertDownloadCounts(t, f.store, 1, 1, 1, 1)
}

func TestAnimeDownloadClaimExplicitDismissalAndWithdrawnLink(t *testing.T) {
	f := newDownloadClaimFixture(t)
	r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
	f.claim(t, 0, r, v)
	if _, err := f.store.db.ExecContext(context.Background(), `UPDATE anime_job_selections SET status='withdrawn'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimAnimeDownload(context.Background(), f.sub.Sources[0].ID, r.ID, 1, v, f.target); !errors.Is(err, ErrAnimeSelectionWithdrawn) {
		t.Fatal("rediscovery reauthorized withdrawn source", err)
	}
	assertDownloadCounts(t, f.store, 1, 1, 1, 1)
}

func TestAnimeDownloadClaimDatabaseReopenAndIndependentUID(t *testing.T) {
	f := newDownloadClaimFixture(t)
	r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
	first := f.claim(t, 0, r, v)
	var path string
	rows, err := f.store.db.Query(`PRAGMA database_list`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			path = file
		}
	}
	rows.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	f.store = reopened
	second := f.claim(t, 0, r, v)
	if second.Created || !reflect.DeepEqual(first.Job, second.Job) {
		t.Fatal("restart recreated resource")
	}
	cached, found, err := reopened.FindAnimeVerifiedRelease(context.Background(), r.ID, r.ContentFingerprint)
	if err != nil || !found || cached.Hash() != v.Hash() || cached.Evidence() != v.Evidence() {
		t.Fatal("committed verification did not survive restart", err)
	}
	// A different actual UID has its own claim; no data/target migration occurs.
	if _, err := reopened.db.ExecContext(context.Background(), `UPDATE p115_accounts SET uid='789'; UPDATE anime_settings SET uid='789'`); err != nil {
		t.Fatal(err)
	}
	f.target.UID = "789"
	d, err := reopened.GetAnimeReleaseDecision(context.Background(), f.sub.Sources[0].ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Snapshot.Selection.Target = f.target
	raw, _ := json.Marshal(d.Snapshot)
	if _, err := reopened.db.ExecContext(context.Background(), `UPDATE anime_release_decisions SET snapshot_json=?,generation=generation+1`, string(raw)); err != nil {
		t.Fatal(err)
	}
	out, err := reopened.ClaimAnimeDownload(context.Background(), f.sub.Sources[0].ID, r.ID, 2, v, f.target)
	if err != nil || !out.Created || out.Job.ID == first.Job.ID {
		t.Fatal("UID capacity became global resource exclusivity", err)
	}
	assertDownloadCounts(t, reopened, 1, 2, 2, 2)
}

func TestAnimeDownloadClaimGenerationAndMissingProof(t *testing.T) {
	for _, mode := range []string{"valid", "wrong generation", "no proof"} {
		t.Run(mode, func(t *testing.T) {
			f := newDownloadClaimFixture(t)
			r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
			source := f.sub.Sources[0]
			ctx := context.Background()
			d, err := f.store.GetAnimeReleaseDecision(ctx, source.ID, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "no proof" {
				if mode == "wrong generation" {
					d.Snapshot.Selection.SourceGeneration++
				}
				raw, _ := animeJSON(d.Snapshot)
				if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_release_decisions SET snapshot_json=? WHERE source_id=? AND release_id=?`, raw, source.ID, r.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				v = animeresource.Verified{}
			}
			out, err := f.store.ClaimAnimeDownload(ctx, source.ID, r.ID, 1, v, f.target)
			if mode == "valid" {
				if err != nil || !out.Created {
					t.Fatal("confirmed history failed", err)
				}
			} else {
				if err == nil {
					t.Fatal("invalid history/proof authorized")
				}
				assertDownloadCounts(t, f.store, 0, 0, 0, 0)
			}
		})
	}
}

func TestAnimeDownloadVerificationCacheRequiresExactObservation(t *testing.T) {
	f := newDownloadClaimFixture(t)
	r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
	f.claim(t, 0, r, v)
	ctx := context.Background()
	if _, found, err := f.store.FindAnimeVerifiedRelease(ctx, r.ID, "v1:"+strings.Repeat("0", 64)); err != nil || found {
		t.Fatal("different content inherited verification", err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_job_selections SET snapshot_json=json_set(snapshot_json,'$.verificationVersion',0)`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.FindAnimeVerifiedRelease(ctx, r.ID, r.ContentFingerprint); err != nil || found {
		t.Fatal("obsolete proof version accepted", err)
	}
}
