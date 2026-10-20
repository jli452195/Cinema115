package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
)

const animeDiscoveryLease = int64(time.Minute / time.Millisecond)

type AnimeSourcePoll struct {
	Source       model.AnimeSource
	Subscription model.AnimeSubscription
	Entry        model.AnimeEntry
	Token        string
	Until        int64
	T0           int64
	Baseline     model.AnimeExecutionBaseline
	IssueCode    string
}

type AnimeDiscoveryStats struct{ Observed, Selected, Review int }

// Stable per-source spreading survives restarts without extra random state.
func animeDiscoveryJitter(id string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int64(h.Sum32() % 60001)
}

func animeDiscoveryBackoff(failures int) int64 {
	delays := [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}
	return int64(delays[min(max(failures, 1), len(delays))-1] / time.Millisecond)
}

const animePollEligible = `s.status='enabled' AND s.run_when_enabled=1 AND p.status='enabled' AND p.first_enabled_at IS NOT NULL AND s.active_baseline_id IS NOT NULL AND s.health<>'unavailable'`

// ReserveAnimeSourcePoll gives one bounded metadata read permission. It is not
// a download lease. No network runs inside this transaction. Unscheduled feeds
// are initialized in bounded batches and spread across the first minute.
func (s *Store) ReserveAnimeSourcePoll(ctx context.Context, now int64) (AnimeSourcePoll, bool, error) {
	var out AnimeSourcePoll
	if now <= 0 {
		return out, false, ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT s.id FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE `+animePollEligible+` AND s.next_check_at IS NULL ORDER BY s.id LIMIT 32`)
	if err != nil {
		return out, false, err
	}
	ids := make([]string, 0, 32)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, false, err
	}
	if err := rows.Close(); err != nil {
		return out, false, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET next_check_at=? WHERE id=? AND next_check_at IS NULL`, now+animeDiscoveryJitter(id), id); err != nil {
			return out, false, err
		}
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT s.id FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE `+animePollEligible+` AND s.next_check_at<=? AND (s.retry_after IS NULL OR s.retry_after<=?) AND (s.check_token IS NULL OR s.check_until IS NULL OR s.check_until<=?) ORDER BY s.next_check_at,s.id LIMIT 1`, now, now, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, tx.Commit()
	}
	if err != nil {
		return out, false, err
	}
	out.Source, err = scanAnimeSource(tx.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=?`, id))
	if err != nil {
		return out, false, err
	}
	out.Subscription, err = scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, out.Source.SubscriptionID))
	if err != nil {
		return out, false, err
	}
	out.Entry, err = scanAnimeEntry(tx.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, out.Subscription.EntryID))
	if err != nil {
		return out, false, err
	}
	var raw string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT t0,source_revision,snapshot_json FROM anime_source_baselines WHERE id=? AND source_id=?`, out.Source.ActiveBaselineID, id).Scan(&out.T0, &revision, &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, false, err
	}
	if decodeAnimeJSON(raw, &out.Baseline) != nil || out.Baseline.SchemaVersion != model.AnimeSchemaVersion || out.Baseline.Target.Validate() != nil || out.T0 <= 0 || out.T0 > now || revision > out.Source.Revision || out.Baseline.RuleVersion != animepreview.RuleVersion || out.Baseline.ParserVersion != animeparse.ParserVersion || out.Baseline.TimeVersion != animeparse.TimeVersion || out.Source.Binding.Validate(true) != nil || out.Source.Rules.Validate(true) != nil {
		out.IssueCode = "BASELINE_RECONFIRM_REQUIRED"
	} else if err := checkAnimeDownloadTargetTx(ctx, tx, out.Baseline.Target, true); err != nil {
		if !errors.Is(err, ErrAnimeAccountChanged) && !errors.Is(err, ErrAnimeSettingsChanged) {
			return out, false, err
		}
		out.IssueCode = "DOWNLOAD_TARGET_CHANGED"
	}
	out.Token, err = newAnimeID()
	if err != nil {
		return out, false, err
	}
	out.Until = now + animeDiscoveryLease
	if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET check_token=?,check_until=?,attempted_at=? WHERE id=?`, out.Token, out.Until, now, id); err != nil {
		return out, false, err
	}
	if err := tx.Commit(); err != nil {
		return AnimeSourcePoll{}, false, err
	}
	return out, true, nil
}

func checkAnimeSourcePollTx(ctx context.Context, tx *sql.Tx, poll AnimeSourcePoll, now int64) error {
	if !model.ValidAnimeID(poll.Source.ID) || !model.ValidAnimeID(poll.Token) || now <= 0 {
		return ErrAnimeInvalid
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE s.id=? AND s.check_token=? AND s.check_until>? AND s.revision=? AND s.run_generation=? AND p.run_generation=? AND s.active_baseline_id=? AND `+animePollEligible+`)`, poll.Source.ID, poll.Token, now, poll.Source.Revision, poll.Source.RunGeneration, poll.Subscription.RunGeneration, poll.Source.ActiveBaselineID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrAnimeConflict
	}
	return nil
}

func animePollConfig(p AnimeSourcePoll) animepreview.Config {
	s := p.Source
	t := p.Baseline.Target
	return animepreview.Config{Chapter: animepreview.Chapter{ID: p.Entry.ID, BangumiID: p.Entry.BangumiID, EditionKey: p.Entry.EditionKey, Revision: p.Entry.Revision}, SubscriptionID: p.Subscription.ID, Revision: p.Subscription.Revision,
		Settings: model.AnimeSettings{Revision: t.SettingsRevision, AccountID: &t.AccountID, AccountRevision: &t.AccountRevision, UID: &t.UID, DownloadCID: &t.ParentCID, DownloadPath: &t.ParentPath},
		Sources:  []model.AnimeSourceConfig{{Ref: "poll", ID: s.ID, ExpectedRevision: s.Revision, Role: "main", Status: "paused", RunWhenEnabled: true, Provider: s.Provider, ItemID: s.ItemID, GroupID: s.GroupID, GroupName: s.GroupName, Binding: s.Binding, Rules: s.Rules}}}
}

// CompleteAnimeSourcePoll commits observations, source-specific decisions and
// next schedule atomically. It never refreshes T0 or expands initial history.
func (s *Store) CompleteAnimeSourcePoll(ctx context.Context, poll AnimeSourcePoll, page animefeed.SourcePage, now int64) (AnimeDiscoveryStats, error) {
	var stats AnimeDiscoveryStats
	if poll.IssueCode != "" || page.Kind != "rss" {
		return stats, ErrAnimeInvalid
	}
	parsed, err := animepreview.Build(animePollConfig(poll), "poll", page, now)
	if err != nil {
		return stats, fmt.Errorf("%w: interpret anime RSS: %w", ErrAnimeInvalid, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()
	if err := checkAnimeSourcePollTx(ctx, tx, poll, now); err != nil {
		return stats, err
	}
	if err := checkAnimeDownloadTargetTx(ctx, tx, poll.Baseline.Target, true); err != nil {
		return stats, err
	}
	input := make([]model.AnimeRelease, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		input = append(input, item.Release)
	}
	releases, err := saveAnimeReleasesTx(ctx, tx, input)
	if err != nil {
		return stats, err
	}
	frozen, err := animeAuthorizedEditionsTx(ctx, tx, poll.Source.ID)
	if err != nil {
		return stats, err
	}
	candidates := make([]animepreview.Item, 0, len(releases))
	generations := make(map[string]int64, len(releases))
	for i, r := range releases {
		item := parsed.Items[i]
		stats.Observed++
		if r.ContentFingerprint != item.Release.ContentFingerprint || !reflect.DeepEqual(r.Parse, item.Release.Parse) {
			continue
		}
		old, err := readAnimeReleaseDecision(ctx, tx, poll.Source.ID, r.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return stats, err
		}
		// Neither a new window nor edited rules silently restore a previous
		// exclusion, retry a selected resource or replace a frozen snapshot.
		if err == nil && old.Disposition != "seen" {
			continue
		}
		generation := int64(1)
		if err == nil {
			generation = old.Generation + 1
		}
		if generation >= model.AnimeMaxRevision {
			return stats, ErrAnimeConflict
		}
		var initial bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_baseline_releases WHERE baseline_id=? AND release_id=?)`, poll.Source.ActiveBaselineID, r.ID).Scan(&initial); err != nil {
			return stats, err
		}
		disposition, reason := item.Decision, item.ReasonCode
		if disposition == "matched" {
			disposition = "selected"
		}
		if initial {
			disposition, reason = "history_skipped", "BASELINE_HISTORY"
		} else if timing := animepreview.ClassifyTime(item.Publication, poll.T0, page.ObservedAt.UnixMilli()); timing != "post_preview_new" {
			disposition, reason = "needs_confirmation", "PUBLICATION_REVIEW_REQUIRED"
		}
		if disposition == "selected" && frozen[animepreview.EpisodeEdition(r.Parse)] {
			disposition, reason = "filtered", "RELEASE_EDITION_ALREADY_SELECTED"
		}
		item.Release, item.Decision, item.ReasonCode = r, disposition, reason
		item.Selectable = disposition == "selected"
		candidates = append(candidates, item)
		generations[r.ID] = generation
	}
	// Only eligible, genuinely new candidates compete. A filtered or undated
	// higher version cannot hide an otherwise eligible lower version.
	animepreview.PreferCurrentVersions(candidates)
	for _, item := range candidates {
		r, disposition, reason := item.Release, item.Decision, item.ReasonCode
		generation := generations[r.ID]
		snapshot := model.AnimeDecisionSnapshot{SchemaVersion: model.AnimeSchemaVersion, Title: r.Title, Binding: poll.Source.Binding, Rules: poll.Source.Rules, Parse: r.Parse, RuleVersion: animepreview.RuleVersion, ParserVersion: r.ParserVersion, TimeVersion: r.TimeVersion}
		var observation any
		var next any
		if disposition == "selected" {
			snapshot.Selection = &model.AnimeSelectionBasis{Kind: "automatic", BaselineID: *poll.Source.ActiveBaselineID, SelectedAt: now, Target: poll.Baseline.Target, SubscriptionGeneration: poll.Subscription.RunGeneration, SourceGeneration: poll.Source.RunGeneration}
			observation, err = animeJSON(item.Observation)
			if err != nil {
				return stats, err
			}
			next = now
			stats.Selected++
		} else if disposition == "needs_confirmation" {
			stats.Review++
		}
		raw, err := animeJSON(snapshot)
		if err != nil {
			return stats, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO anime_release_decisions(source_id,release_id,disposition,reason_code,source_revision,release_fingerprint,snapshot_json,generation,observation_json,next_verify_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id,release_id) DO UPDATE SET disposition=excluded.disposition,reason_code=excluded.reason_code,source_revision=excluded.source_revision,release_fingerprint=excluded.release_fingerprint,snapshot_json=excluded.snapshot_json,generation=excluded.generation,observation_json=excluded.observation_json,next_verify_at=excluded.next_verify_at,updated_at=excluded.updated_at`, poll.Source.ID, r.ID, disposition, reason, poll.Source.Revision, r.ContentFingerprint, raw, generation, observation, next, now)
		if err != nil {
			return AnimeDiscoveryStats{}, err
		}
	}
	window, err := animeJSON(model.AnimeRSSWindow{SchemaVersion: model.AnimeSchemaVersion, ObservedAt: page.ObservedAt.UnixMilli(), RawCount: page.RawCount, ReleaseCount: len(parsed.Items)})
	if err != nil {
		return AnimeDiscoveryStats{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET health='ok',checked_at=?,next_check_at=?,retry_after=NULL,error_code=NULL,poll_failures=0,check_token=NULL,check_until=NULL,poll_snapshot_json=?,updated_at=? WHERE id=?`, now, now+int64(15*time.Minute/time.Millisecond)+animeDiscoveryJitter(poll.Source.ID), window, now, poll.Source.ID)
	if err != nil {
		return AnimeDiscoveryStats{}, err
	}
	if err := tx.Commit(); err != nil {
		return AnimeDiscoveryStats{}, err
	}
	return stats, nil
}

func (s *Store) FailAnimeSourcePoll(ctx context.Context, poll AnimeSourcePoll, code string, retryAt int64, unavailable bool, now int64) error {
	switch code {
	case "SOURCE_READ_FAILED", "SOURCE_READ_BUDGET", "SOURCE_RATE_LIMITED", "SOURCE_UNAVAILABLE", "SOURCE_BINDING_CHANGED", "BASELINE_RECONFIRM_REQUIRED", "DOWNLOAD_TARGET_CHANGED", "POLL_CANCELLED", "SOURCE_RESPONSE_INVALID":
	default:
		return ErrAnimeInvalid
	}
	if retryAt < 0 {
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkAnimeSourcePollTx(ctx, tx, poll, now); err != nil {
		return err
	}
	var failures int
	if err := tx.QueryRowContext(ctx, `SELECT poll_failures FROM anime_subscription_sources WHERE id=?`, poll.Source.ID).Scan(&failures); err != nil {
		return err
	}
	health := "error"
	var next any
	var retry any
	if code == "POLL_CANCELLED" {
		health = poll.Source.Health
		next = now + 5000
	} else if unavailable {
		health = "unavailable"
	} else {
		failures = min(failures+1, 4)
		at := max(now+animeDiscoveryBackoff(failures)+animeDiscoveryJitter(poll.Source.ID), retryAt)
		next = at
		if retryAt > now {
			health, retry = "cooldown", retryAt
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET health=?,next_check_at=?,retry_after=?,error_code=?,poll_failures=?,check_token=NULL,check_until=NULL,updated_at=? WHERE id=?`, health, next, retry, code, failures, now, poll.Source.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
