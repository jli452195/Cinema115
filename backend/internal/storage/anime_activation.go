package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
)

type AnimeActivationSource struct {
	AnimePreviewRef
	SelectedIDs  []string `json:"selectedReleaseIds"`
	DismissedIDs []string `json:"dismissedReleaseIds"`
}

// ActivationConfig is built from saved configuration, never from browser rules.
func ActivationConfig(sub model.AnimeSubscription, entry model.AnimeEntry, settings model.AnimeSettings) animepreview.Config {
	c := animepreview.Config{SubscriptionID: sub.ID, Revision: sub.Revision, Settings: settings,
		Chapter: animepreview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision},
		Sources: make([]model.AnimeSourceConfig, 0, len(sub.Sources))}
	for _, source := range sub.Sources {
		if source.Status == "removed" {
			continue
		}
		c.Sources = append(c.Sources, model.AnimeSourceConfig{Ref: source.ID, ID: source.ID, ExpectedRevision: source.Revision,
			Role: source.Role, Position: source.Position, Status: source.Status, RunWhenEnabled: source.RunWhenEnabled,
			Provider: source.Provider, ItemID: source.ItemID, GroupID: source.GroupID, GroupName: source.GroupName, Binding: source.Binding, Rules: source.Rules})
	}
	return c
}

// EnableAnimeSubscription atomically freezes all previewed history and grants
// only explicit selections. The transaction performs no upstream requests.
func (s *Store) EnableAnimeSubscription(ctx context.Context, id string, revision int64, target model.AnimeDownloadTarget, refs []AnimeActivationSource, key AnimeWriteKey) (model.AnimeControlReceipt, error) {
	out := model.AnimeControlReceipt{ID: id, ResumedSourceIDs: []string{}, UnchangedSources: []model.AnimeUnchangedSource{}}
	if !model.ValidAnimeID(id) || revision < 1 || revision >= model.AnimeMaxRevision || target.Validate() != nil || len(refs) < 1 || len(refs) > model.AnimeMaxSources {
		return out, ErrAnimeInvalid
	}
	if err := key.validate(); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = enableAnimeSubscriptionTx(ctx, tx, id, revision, target, refs, key, nil)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit anime activation: %w", err)
	}
	return out, nil
}

type animeActivationPreview func(context.Context, *sql.Tx, model.AnimeSource, AnimeActivationSource) (animepreview.Source, error)

func enableAnimeSubscriptionTx(ctx context.Context, tx *sql.Tx, id string, revision int64, target model.AnimeDownloadTarget, refs []AnimeActivationSource, key AnimeWriteKey, resolve animeActivationPreview) (model.AnimeControlReceipt, error) {
	out := model.AnimeControlReceipt{ID: id, ResumedSourceIDs: []string{}, UnchangedSources: []model.AnimeUnchangedSource{}}
	if found, err := readAnimeActionReceiptTx(ctx, tx, key, &out); found || err != nil {
		return out, err
	}
	sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, id))
	if err != nil {
		return out, err
	}
	if sub.Revision != revision {
		return out, ErrAnimeConflict
	}
	if sub.FirstEnabledAt != nil || (sub.Status != "draft" && sub.Status != "paused") {
		return out, ErrAnimeTransition
	}
	if err := checkAnimeDownloadTargetTx(ctx, tx, target, true); err != nil {
		return out, err
	}
	sub.Sources, err = readAnimeSources(ctx, tx, id, false)
	if err != nil {
		return out, err
	}
	entry, err := scanAnimeEntry(tx.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, sub.EntryID))
	if err != nil {
		return out, err
	}
	settings, err := readAnimeSettingsTx(ctx, tx)
	if err != nil {
		return out, err
	}
	c := ActivationConfig(sub, entry, settings)
	fingerprint, err := animepreview.ConfigFingerprint(c)
	if err != nil {
		return out, err
	}
	byID := make(map[string]AnimeActivationSource, len(refs))
	for _, ref := range refs {
		if byID[ref.SourceRef].PreviewID != "" || ref.SelectedIDs == nil || ref.DismissedIDs == nil {
			return out, ErrAnimeInvalid
		}
		byID[ref.SourceRef] = ref
	}
	now, active, main := time.Now().UnixMilli(), 0, false
	loadedRows := 0
	for _, source := range sub.Sources {
		if !source.RunWhenEnabled {
			continue
		}
		if source.Status != "paused" || source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil || source.GroupName == nil || source.Revision >= model.AnimeMaxRevision {
			return out, ErrAnimeReconfirm
		}
		ref, ok := byID[source.ID]
		if !ok {
			return out, ErrAnimeInvalid
		}
		var resolved animepreview.Source
		if resolve != nil {
			resolved, err = resolve(ctx, tx, source, ref)
		} else {
			var b animepreview.Batch
			b, err = readAnimePreviewTx(ctx, tx, key.ActorKey, ref.BatchID)
			if err == nil {
				fp, e := animepreview.ConfigFingerprint(b.Config)
				if e != nil || fp != fingerprint {
					return out, animepreview.ErrConflict
				}
				resolved, err = previewReferencedSource(b, ref.AnimePreviewRef)
			}
		}
		if err != nil {
			return out, err
		}
		preview := &resolved
		if preview.SourceID != source.ID || preview.SourceRevision != source.Revision || preview.LoadRevision != ref.LoadRevision {
			return out, animepreview.ErrConflict
		}
		if !preview.ValidAt(now) {
			return out, animepreview.ErrExpired
		}
		if len(preview.Pending) != 0 {
			return out, animepreview.ErrReview
		}
		loadedRows += preview.Page.RawCount
		if loadedRows > 2000 {
			return out, ErrAnimeInvalid
		}
		choices := make(map[string]string, len(ref.SelectedIDs)+len(ref.DismissedIDs))
		for _, pair := range []struct {
			ids   []string
			state string
		}{{ref.SelectedIDs, "selected"}, {ref.DismissedIDs, "dismissed"}} {
			for _, rid := range pair.ids {
				if !model.ValidAnimeID(rid) || choices[rid] != "" {
					return out, ErrAnimeInvalid
				}
				choices[rid] = pair.state
			}
		}
		baselineID, err := newAnimeID()
		if err != nil {
			return out, err
		}
		raw, err := animeJSON(model.AnimeExecutionBaseline{SchemaVersion: model.AnimeSchemaVersion, Target: target, RuleVersion: animepreview.RuleVersion, ParserVersion: animeparse.ParserVersion, TimeVersion: animeparse.TimeVersion})
		if err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_source_baselines(id,source_id,source_revision,t0,confirmed_at,fingerprint,snapshot_json) VALUES(?,?,?,?,?,?,?)`, baselineID, source.ID, source.Revision+1, preview.T0, now, preview.Fingerprint, raw); err != nil {
			return out, err
		}
		for _, item := range preview.Items {
			r := item.Release
			current, err := scanAnimeRelease(tx.QueryRowContext(ctx, `SELECT `+animeReleaseColumns+` FROM anime_releases WHERE id=?`, r.ID))
			if err != nil {
				return out, err
			}
			if current.ContentFingerprint != r.ContentFingerprint || current.ParserVersion != animeparse.ParserVersion || current.TimeVersion != animeparse.TimeVersion {
				return out, animepreview.ErrReview
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO anime_baseline_releases(baseline_id,release_id) VALUES(?,?)`, baselineID, r.ID); err != nil {
				return out, err
			}
			state, reason := "history_skipped", "BASELINE_HISTORY"
			if item.Decision == "filtered" || item.Decision == "needs_confirmation" {
				state, reason = item.Decision, item.ReasonCode
			}
			choice := choices[r.ID]
			if choice != "" {
				if choice == "selected" && !item.Selectable {
					return out, animepreview.ErrReview
				}
				state, reason = choice, "INITIAL_HISTORY_CONFIRMED"
				delete(choices, r.ID)
			}
			snapshot := model.AnimeDecisionSnapshot{SchemaVersion: model.AnimeSchemaVersion, Title: r.Title, Binding: source.Binding, Rules: source.Rules, Parse: r.Parse, RuleVersion: animepreview.RuleVersion, ParserVersion: r.ParserVersion, TimeVersion: r.TimeVersion}
			var observation, next any
			if state == "selected" {
				snapshot.Selection = &model.AnimeSelectionBasis{Kind: "initial_history", BaselineID: baselineID, SelectedAt: now, Target: target, SubscriptionGeneration: sub.RunGeneration, SourceGeneration: source.RunGeneration}
				observation, err = animeJSON(item.Observation)
				if err != nil {
					return out, err
				}
				next = now
			}
			raw, err := animeJSON(snapshot)
			if err != nil {
				return out, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO anime_release_decisions(source_id,release_id,disposition,reason_code,source_revision,release_fingerprint,snapshot_json,observation_json,next_verify_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, source.ID, r.ID, state, reason, source.Revision+1, r.ContentFingerprint, raw, observation, next, now); err != nil {
				return out, err
			}
		}
		if len(choices) != 0 {
			return out, animepreview.ErrReview
		}
		if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET status='enabled',revision=revision+1,active_baseline_id=?,next_check_at=?,updated_at=? WHERE id=?`, baselineID, now+int64(15*time.Minute/time.Millisecond)+animeDiscoveryJitter(source.ID), now, source.ID); err != nil {
			return out, err
		}
		out.ResumedSourceIDs = append(out.ResumedSourceIDs, source.ID)
		active++
		main = main || source.Role == "main"
	}
	if active != len(refs) || !main {
		return out, ErrAnimeReconfirm
	}
	out.Revision, out.Status = revision+1, "enabled"
	if _, err := tx.ExecContext(ctx, `UPDATE anime_subscriptions SET status='enabled',revision=revision+1,first_enabled_at=?,updated_at=? WHERE id=?`, now, now, id); err != nil {
		return out, err
	}
	if resolve == nil {
		if err := writeAnimeActionReceiptTx(ctx, tx, key, out, now); err != nil {
			return out, err
		}
	}
	return out, nil
}

func readAnimeActionReceiptTx(ctx context.Context, tx *sql.Tx, key AnimeWriteKey, out any) (bool, error) {
	var digest, raw string
	err := tx.QueryRowContext(ctx, `SELECT request_digest,receipt_json FROM anime_request_keys WHERE actor_key=? AND operation=? AND request_key=?`, key.ActorKey, key.Operation, key.Key).Scan(&digest, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digest != key.Digest {
		return false, ErrAnimeIdempotency
	}
	return true, decodeAnimeJSON(raw, out)
}

func writeAnimeActionReceiptTx(ctx context.Context, tx *sql.Tx, key AnimeWriteKey, out any, now int64) error {
	raw, err := animeJSON(out)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO anime_request_keys(actor_key,operation,request_key,request_digest,http_status,receipt_json,created_at) VALUES(?,?,?,?,200,?,?)`, key.ActorKey, key.Operation, key.Key, key.Digest, raw, now)
	return err
}
