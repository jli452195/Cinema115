package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"strm115/backend/internal/anime/preview"
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
	Target       model.AnimeDownloadTarget
	IssueCode    string
	Initialize   bool
	LocalOnly    bool // Complete coverage needs lifecycle checks, not another RSS read.
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

const animePollEligible = `s.status='enabled' AND s.run_when_enabled=1 AND p.status='enabled' AND p.first_enabled_at IS NOT NULL AND s.health<>'unavailable'`

// Keep recovery reads after resume/configuration changes or an unfinished poll.
const animePollSettled = animeSourceComplete + ` AND s.health='ok' AND s.check_token IS NULL AND EXISTS(SELECT 1 FROM anime_source_activity a WHERE a.source_id=s.id AND a.source_revision=s.revision)`

// ReserveAnimeSourcePoll gives one bounded metadata read permission. It is not
// a download lease. No network runs inside this transaction. Unscheduled feeds
// are initialized in bounded batches and spread across the first minute.
func (s *Store) ReserveAnimeSourcePoll(ctx context.Context, now int64, providers ...string) (AnimeSourcePoll, bool, error) {
	var out AnimeSourcePoll
	if now <= 0 || len(providers) > 1 {
		return out, false, ErrAnimeInvalid
	}
	provider := ""
	if len(providers) == 1 {
		provider = providers[0]
		if provider != "" && !animefeed.ValidProvider(provider) {
			return out, false, ErrAnimeInvalid
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT s.id FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE `+animePollEligible+` AND (?='' OR s.provider=?) AND s.next_check_at IS NULL ORDER BY s.id LIMIT 32`, provider, provider)
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
	err = tx.QueryRowContext(ctx, `SELECT s.id FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE `+animePollEligible+` AND (?='' OR s.provider=?) AND s.next_check_at<=? AND (s.retry_after IS NULL OR s.retry_after<=?) AND (s.check_token IS NULL OR s.check_until IS NULL OR s.check_until<=?) ORDER BY s.next_check_at,s.id LIMIT 1`, provider, provider, now, now, now).Scan(&id)
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
	if err := tx.QueryRowContext(ctx, `SELECT `+animePollSettled+` FROM anime_subscription_sources s WHERE s.id=?`, id).Scan(&out.LocalOnly); err != nil {
		return out, false, err
	}
	if out.LocalOnly {
		settings, err := readAnimeRSSSettings(ctx, tx)
		if err != nil {
			return out, false, err
		}
		if err := evaluateAnimeLifecycleTx(ctx, tx, out.Source.SubscriptionID, now); err != nil {
			return out, false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+int64(settings.PollMinutes)*60000+animeDiscoveryJitter(id), id); err != nil {
			return out, false, err
		}
		return out, true, tx.Commit()
	}
	out.Subscription, err = scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, out.Source.SubscriptionID))
	if err != nil {
		return out, false, err
	}
	out.Entry, err = scanAnimeEntry(tx.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, out.Subscription.EntryID))
	if err != nil {
		return out, false, err
	}
	out.Initialize = out.Source.CheckedAt == nil
	settings, err := readAnimeSettingsTx(ctx, tx)
	if err != nil {
		return out, false, err
	}
	out.Target, err = animeTargetFromSettings(settings)
	if err == nil {
		err = checkAnimeDownloadTargetTx(ctx, tx, out.Target, true)
	}
	if err != nil {
		if !errors.Is(err, ErrAnimeDirectoryRequired) && !errors.Is(err, ErrAnimeAccountChanged) && !errors.Is(err, ErrAnimeSettingsChanged) {
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
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE s.id=? AND s.check_token=? AND s.check_until>? AND s.revision=? AND s.run_generation=? AND p.run_generation=? AND `+animePollEligible+`)`, poll.Source.ID, poll.Token, now, poll.Source.Revision, poll.Source.RunGeneration, poll.Subscription.RunGeneration).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrAnimeConflict
	}
	return nil
}

func animePollConfig(p AnimeSourcePoll) preview.Config {
	s := p.Source
	t := p.Target
	return preview.Config{Chapter: preview.Chapter{ID: p.Entry.ID, BangumiID: p.Entry.BangumiID, EditionKey: p.Entry.EditionKey, Revision: p.Entry.Revision}, SubscriptionID: p.Subscription.ID, Revision: p.Subscription.Revision,
		Settings: model.AnimeSettings{Revision: t.SettingsRevision, AccountID: &t.AccountID, DownloadCID: &t.ParentCID, DownloadPath: &t.ParentPath},
		Sources:  []model.AnimeSourceConfig{{Ref: "poll", ID: s.ID, ExpectedRevision: s.Revision, Role: "main", Status: "paused", RunWhenEnabled: true, Provider: s.Provider, ItemID: s.ItemID, GroupID: s.GroupID, GroupName: s.GroupName, Binding: s.Binding, Rules: s.Rules}}}
}

// CompleteAnimeSourcePoll commits observations, source-specific decisions and
// next schedule atomically. All publications use the same saved download rules.
func (s *Store) CompleteAnimeSourcePoll(ctx context.Context, poll AnimeSourcePoll, page animefeed.SourcePage, now int64) (AnimeDiscoveryStats, error) {
	var stats AnimeDiscoveryStats
	if poll.IssueCode != "" || page.Kind != "rss" && page.Kind != "history" {
		return stats, ErrAnimeInvalid
	}
	parsed, err := preview.BuildCandidates(animePollConfig(poll), "poll", page, now)
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
	if err := checkAnimeDownloadTargetTx(ctx, tx, poll.Target, true); err != nil {
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
	covered, err := animeReservedEpisodesTx(ctx, tx, poll.Source.ID, "", true)
	if err != nil {
		return stats, err
	}
	candidates := make([]preview.Item, 0, len(releases))
	newMatch := false
	generations := make(map[string]int64, len(releases))
	for i, r := range releases {
		item := parsed.Items[i]
		stats.Observed++
		if r.ContentFingerprint != item.Release.ContentFingerprint {
			continue
		}
		// The shared post cache owns identity and content. Interpretation belongs
		// to this source: another source may use a different episode expression.
		r.Parse, r.ParserVersion, r.TimeVersion = item.Release.Parse, item.Release.ParserVersion, item.Release.TimeVersion
		old, err := readAnimeReleaseDecision(ctx, tx, poll.Source.ID, r.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return stats, err
		}
		found := err == nil
		if !found && item.Decision == "matched" {
			newMatch = true
		}
		// An authorized selection is frozen. Unselected interpretations are only
		// cached until the source rules or parsing engine changes.
		if found && (old.Disposition == "dismissed" || old.SourceRevision == poll.Source.Revision && (old.Snapshot.Selection != nil ||
			old.Disposition != "seen" && old.Snapshot.ParserVersion == r.ParserVersion &&
				old.Snapshot.TimeVersion == r.TimeVersion && old.Snapshot.RuleVersion == preview.RuleVersion)) {
			continue
		}
		committed, err := animeReleaseCommittedTx(ctx, tx, poll.Source.ID, r.ID)
		if err != nil {
			return stats, err
		}
		if committed {
			continue
		}
		generation := int64(1)
		if found {
			generation = old.Generation + 1
		}
		if generation >= model.AnimeMaxRevision {
			return stats, ErrAnimeConflict
		}
		disposition, reason := item.Decision, item.ReasonCode
		if disposition == "matched" {
			disposition = "selected"
		}
		if disposition == "selected" && r.Parse.Kind == "episode" && r.Parse.Episode != nil && covered[*r.Parse.Episode] {
			disposition, reason = "filtered", "EPISODE_ALREADY_COVERED"
		}
		if disposition == "selected" && frozen[preview.EpisodeEdition(r.Parse)] {
			// A changed configuration may reauthorize this exact unsubmitted
			// release, but never replace its already chosen revision.
			pending, err := animeReleasePendingJobTx(ctx, tx, poll.Source.ID, r.ID)
			if err != nil {
				return stats, err
			}
			if !pending {
				disposition, reason = "filtered", "RELEASE_EDITION_ALREADY_SELECTED"
			}
		}
		item.Release, item.Decision, item.ReasonCode = r, disposition, reason
		item.Selectable = disposition == "selected"
		candidates = append(candidates, item)
		generations[r.ID] = generation
	}
	// Only eligible, unprocessed candidates compete. A filtered higher version
	// cannot hide an otherwise eligible lower version.
	preview.PreferCurrentVersions(candidates)
	for _, item := range candidates {
		r, disposition, reason := item.Release, item.Decision, item.ReasonCode
		generation := generations[r.ID]
		snapshot := model.AnimeDecisionSnapshot{SchemaVersion: model.AnimeSchemaVersion, Title: r.Title, Binding: poll.Source.Binding, Rules: poll.Source.Rules, Parse: r.Parse, RuleVersion: preview.RuleVersion, ParserVersion: r.ParserVersion, TimeVersion: r.TimeVersion}
		observation, err := animeJSON(item.Observation)
		if err != nil {
			return stats, err
		}
		var next any
		if disposition == "selected" {
			snapshot.Selection = &model.AnimeSelectionBasis{SelectedAt: now, Target: poll.Target, SubscriptionGeneration: poll.Subscription.RunGeneration, SourceGeneration: poll.Source.RunGeneration}
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
		if disposition == "selected" {
			if err := refreshAnimePendingJobTx(ctx, tx, poll, r, snapshot, now); err != nil {
				return stats, err
			}
		}
	}
	window, err := animeJSON(model.AnimeRSSWindow{SchemaVersion: model.AnimeSchemaVersion, ObservedAt: page.ObservedAt.UnixMilli(), RawCount: page.RawCount, ReleaseCount: len(parsed.Items)})
	if err != nil {
		return AnimeDiscoveryStats{}, err
	}
	quiet, err := recordAnimeSourceActivityTx(ctx, tx, poll.Source, newMatch, now)
	if err != nil {
		return stats, err
	}
	settings, err := readAnimeRSSSettings(ctx, tx)
	if err != nil {
		return stats, err
	}
	interval := int64(settings.PollMinutes) * 60000
	if now-quiet >= 30*animeDay {
		interval = max(interval, animeDay)
	}
	_, err = tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET health='ok',checked_at=?,next_check_at=?,retry_after=NULL,error_code=NULL,poll_failures=0,check_token=NULL,check_until=NULL,poll_snapshot_json=?,updated_at=? WHERE id=?`, now, now+interval+animeDiscoveryJitter(poll.Source.ID), window, now, poll.Source.ID)
	if err != nil {
		return AnimeDiscoveryStats{}, err
	}
	if err := evaluateAnimeLifecycleTx(ctx, tx, poll.Subscription.ID, now); err != nil {
		return stats, fmt.Errorf("evaluate anime lifecycle: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AnimeDiscoveryStats{}, err
	}
	return stats, nil
}

func (s *Store) FailAnimeSourcePoll(ctx context.Context, poll AnimeSourcePoll, code string, retryAt int64, unavailable bool, now int64) error {
	switch code {
	case "SOURCE_READ_FAILED", "SOURCE_READ_BUDGET", "SOURCE_RATE_LIMITED", "SOURCE_UNAVAILABLE", "SOURCE_BINDING_CHANGED", "DOWNLOAD_TARGET_CHANGED", "POLL_CANCELLED", "SOURCE_RESPONSE_INVALID":
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
	if code != "POLL_CANCELLED" {
		if _, err := tx.ExecContext(ctx, `UPDATE anime_source_activity SET quiet_since=? WHERE source_id=?`, now, poll.Source.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
