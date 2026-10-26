package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

const animeLifecycleSchema = `
CREATE TABLE IF NOT EXISTS anime_rss_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>0),
 poll_minutes INTEGER NOT NULL CHECK(poll_minutes BETWEEN 10 AND 1440),
 auto_archive INTEGER NOT NULL CHECK(auto_archive IN (0,1)),
 inactive_archive_days INTEGER NOT NULL CHECK(inactive_archive_days=0 OR inactive_archive_days BETWEEN 30 AND 365)
);
INSERT OR IGNORE INTO anime_rss_settings VALUES(1,1,15,1,60);
CREATE TABLE IF NOT EXISTS anime_source_activity (
 source_id TEXT PRIMARY KEY REFERENCES anime_subscription_sources(id),
 source_revision INTEGER NOT NULL,
 quiet_since INTEGER NOT NULL,
 last_success_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS anime_subscription_archives (
 subscription_id TEXT PRIMARY KEY REFERENCES anime_subscriptions(id),
 reason TEXT NOT NULL CHECK(reason IN ('complete','inactive','manual')),
 archived_at INTEGER NOT NULL
);`

const animeDay = int64(24 * time.Hour / time.Millisecond)

// Activity uses discovery time, not an RSS publication date. Failed reads and
// long offline gaps restart the quiet period instead of proving abandonment.
func recordAnimeSourceActivityTx(ctx context.Context, tx *sql.Tx, source model.AnimeSource, matched bool, now int64) (int64, error) {
	quiet, success, revision := now, int64(0), int64(0)
	err := tx.QueryRowContext(ctx, `SELECT source_revision,quiet_since,last_success_at FROM anime_source_activity WHERE source_id=?`, source.ID).Scan(&revision, &quiet, &success)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read anime activity: %w", err)
	}
	if revision != source.Revision || matched || success == 0 || now-success > 2*animeDay {
		quiet = now
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO anime_source_activity(source_id,source_revision,quiet_since,last_success_at) VALUES(?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET source_revision=excluded.source_revision,quiet_since=excluded.quiet_since,last_success_at=excluded.last_success_at`, source.ID, source.Revision, quiet, now)
	return quiet, err
}

// The first observed ordinary release anchors the total; the download minimum
// excludes intentionally skipped episodes without shifting the completion end.
func animeCompletionRange(source model.AnimeSource, first, total int) (int, int, bool) {
	if first < 0 || source.Rules.MinimumEpisode == nil {
		return -1, 0, false
	}
	last, lower := first+total-1, max(first, *source.Rules.MinimumEpisode)
	if total < 1 || last > 99999 {
		return lower, 0, false
	}
	return lower, last, lower <= last
}

func activeAnimeSources(sub model.AnimeSubscription) []model.AnimeSource {
	out := make([]model.AnimeSource, 0, len(sub.Sources))
	for _, source := range sub.Sources {
		if source.RunWhenEnabled && source.Status == "enabled" {
			out = append(out, source)
		}
	}
	return out
}

func readAnimeLifecycle(ctx context.Context, q animeReader, sub model.AnimeSubscription, entry model.AnimeEntry, now int64) (model.AnimeLifecycle, bool, error) {
	var out model.AnimeLifecycle
	settings, err := readAnimeRSSSettings(ctx, q)
	if err != nil {
		return out, false, err
	}
	sub.Form = sub.Form.WithRSSDefaults(settings)
	err = q.QueryRowContext(ctx, `SELECT reason,archived_at FROM anime_subscription_archives WHERE subscription_id=?`, sub.ID).Scan(&out.ArchiveReason, &out.ArchivedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, false, err
	}
	if sub.Status != "enabled" {
		return out, false, nil
	}
	sources := activeAnimeSources(sub)
	if len(sources) == 0 {
		return out, false, nil
	}
	total := 0
	if entry.Metadata.TotalEpisodes != nil {
		total = *entry.Metadata.TotalEpisodes
	}
	if sub.Form.TotalEpisodes != nil {
		total = *sub.Form.TotalEpisodes
	}
	if total < 1 {
		out.CompletionIssue = "TOTAL_UNKNOWN"
	}
	quiet, healthy := int64(0), true
	for _, source := range sources {
		var since, success, revision int64
		var complete bool
		err := q.QueryRowContext(ctx, `SELECT a.source_revision,a.quiet_since,a.last_success_at,`+animeSourceComplete+` FROM anime_source_activity a JOIN anime_subscription_sources s ON s.id=a.source_id WHERE a.source_id=?`, source.ID).Scan(&revision, &since, &success, &complete)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, false, err
		}
		if source.Health != "ok" || source.CheckToken != nil || revision != source.Revision || success <= 0 || !complete && now-success > 2*animeDay || since > now {
			healthy = false
		}
		quiet = max(quiet, since)
	}
	if healthy {
		out.QuietSince = &quiet
	}
	return out, healthy, nil
}

func (s *Store) AnimeLifecycle(ctx context.Context, sub model.AnimeSubscription, entry model.AnimeEntry, now int64) (model.AnimeLifecycle, error) {
	out, _, err := readAnimeLifecycle(ctx, s.db, sub, entry, now)
	return out, err
}

// No network or cloud location checks. Both completion and inactivity require
// settled jobs and decisions, so archiving cannot revoke an unfinished write.
func evaluateAnimeLifecycleTx(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, id))
	if err != nil {
		return err
	}
	if sub.Status != "enabled" || sub.Revision >= model.AnimeMaxRevision {
		return nil
	}
	settings, err := readAnimeRSSSettings(ctx, tx)
	if err != nil {
		return err
	}
	sub.Form = sub.Form.WithRSSDefaults(settings)
	sub.Sources, err = readAnimeSources(ctx, tx, id, false)
	if err != nil {
		return err
	}
	entry, err := scanAnimeEntry(tx.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, sub.EntryID))
	if err != nil {
		return err
	}
	info, healthy, err := readAnimeLifecycle(ctx, tx, sub, entry, now)
	if err != nil || !healthy {
		return err
	}
	// Include paused sources' unfinished jobs too: restoring one later must not
	// leave it silently frozen behind an automatically archived parent.
	var pending bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_download_jobs j JOIN anime_job_selections x ON x.job_id=j.id JOIN anime_subscription_sources s ON s.id=x.source_id WHERE s.subscription_id=? AND x.status='authorized' AND j.workflow_state<>'organized' AND j.workflow_state<>'cancelled' AND NOT EXISTS(SELECT 1 FROM anime_download_jobs newer WHERE newer.claim_id=j.claim_id AND newer.attempt>j.attempt)) OR EXISTS(SELECT 1 FROM anime_release_decisions d JOIN anime_subscription_sources s ON s.id=d.source_id WHERE s.subscription_id=? AND s.status='enabled' AND s.run_when_enabled=1 AND d.disposition='selected' AND NOT EXISTS(SELECT 1 FROM anime_job_selections x WHERE x.source_id=d.source_id AND x.release_id=d.release_id AND x.status='authorized'))`, id, id).Scan(&pending)
	if err != nil || pending {
		return err
	}
	reason := ""
	if sub.Form.ArchiveOnCompletion() && info.CompletionIssue == "" {
		// The summary worker owns parsing. Dirty or absent summaries cannot
		// archive a subscription, even if their previous value was complete.
		var complete bool
		if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM anime_subscription_sources s WHERE s.subscription_id=? AND s.status='enabled' AND s.run_when_enabled=1 AND NOT `+animeSourceComplete+`)`, sub.ID).Scan(&complete); err != nil {
			return err
		}
		if complete {
			reason = "complete"
		}
	}
	if reason == "" && sub.Form.InactiveDays() > 0 && info.QuietSince != nil && now-*info.QuietSince >= int64(sub.Form.InactiveDays())*animeDay {
		reason = "inactive"
	}
	if reason == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_subscriptions SET status='archived',revision=revision+1,updated_at=? WHERE id=? AND status='enabled' AND revision=?`, now, id, sub.Revision); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO anime_subscription_archives(subscription_id,reason,archived_at) VALUES(?,?,?) ON CONFLICT(subscription_id) DO UPDATE SET reason=excluded.reason,archived_at=excluded.archived_at`, id, reason, now)
	return err
}
