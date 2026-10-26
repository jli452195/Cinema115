package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"strm115/backend/internal/model"
)

// AnimeWriteKey is supplied by the authenticated service boundary. Digest must
// cover the canonical request (including expected revisions and entity ID).
// HTTP validation and authentication are introduced in P1-07.
type AnimeWriteKey struct{ ActorKey, Operation, Key, Digest string }

func (k AnimeWriteKey) validate() error {
	if len(k.ActorKey) == 0 || len(k.ActorKey) > 256 || len(k.Operation) == 0 || len(k.Operation) > 256 || len(k.Key) < 8 || len(k.Key) > 128 || len(k.Digest) != sha256.Size*2 {
		return ErrAnimeInvalid
	}
	for _, c := range k.Key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ErrAnimeInvalid
		}
	}
	if _, err := hex.DecodeString(k.Digest); err != nil {
		return ErrAnimeInvalid
	}
	return nil
}

// SaveAnimeSubscription atomically saves P1 configuration and an optional
// idempotency receipt. It never creates a baseline, selection, task or due time.
// The service must supply verified entry/group metadata and check readiness.
func (s *Store) SaveAnimeSubscription(ctx context.Context, in model.AnimeSubscriptionConfig, key *AnimeWriteKey) (model.AnimeConfigReceipt, error) {
	return s.saveAnimeConfiguration(ctx, in, key, nil)
}

func (s *Store) saveAnimeConfiguration(ctx context.Context, in model.AnimeSubscriptionConfig, key *AnimeWriteKey, guard *AnimeConfigGuard) (model.AnimeConfigReceipt, error) {
	var receipt model.AnimeConfigReceipt
	if key != nil {
		if err := key.validate(); err != nil {
			return receipt, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return receipt, fmt.Errorf("begin anime configuration: %w", err)
	}
	defer tx.Rollback()
	receipt, err = saveAnimeConfigurationTx(ctx, tx, in, key, guard)
	if err != nil {
		return receipt, err
	}
	if err := tx.Commit(); err != nil {
		return receipt, animeDBError("commit anime configuration", err)
	}
	return receipt, nil
}

// The caller owns the transaction, allowing initial configuration and activation
// to commit together without exposing an unstarted subscription.
func saveAnimeConfigurationTx(ctx context.Context, tx *sql.Tx, in model.AnimeSubscriptionConfig, key *AnimeWriteKey, guard *AnimeConfigGuard) (model.AnimeConfigReceipt, error) {
	var receipt model.AnimeConfigReceipt
	// Replay before revision validation so a lost successful response is safe.
	if key != nil {
		var digest, raw string
		err := tx.QueryRowContext(ctx, `SELECT request_digest,receipt_json FROM anime_request_keys WHERE actor_key=? AND operation=? AND request_key=?`, key.ActorKey, key.Operation, key.Key).Scan(&digest, &raw)
		if err == nil {
			if digest != key.Digest {
				return receipt, ErrAnimeIdempotency
			}
			if raw == animeDeletedReceipt {
				return receipt, sql.ErrNoRows
			}
			if err := decodeAnimeJSON(raw, &receipt); err != nil {
				return receipt, fmt.Errorf("read anime receipt: %w", err)
			}
			return receipt, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return receipt, fmt.Errorf("find anime receipt: %w", err)
		}
	}
	runtime := guard != nil && guard.Runtime
	if in.Status == "enabled" && !runtime {
		return receipt, ErrAnimeCapability
	}
	if guard != nil {
		entryID, err := prepareAnimeConfigurationTx(ctx, tx, *guard)
		if err != nil {
			return receipt, err
		}
		in.EntryID = entryID
	}
	for _, source := range in.Sources {
		if source.Status == "enabled" && !runtime {
			return receipt, ErrAnimeCapability
		}
	}
	validation := in
	if runtime {
		validation.Status = "paused"
		validation.Sources = append([]model.AnimeSourceConfig{}, in.Sources...)
		for i := range validation.Sources {
			validation.Sources[i].Status = "paused"
		}
	}
	if err := validation.Validate(); err != nil {
		return receipt, fmt.Errorf("%w: %v", ErrAnimeInvalid, err)
	}
	var entryExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_entries WHERE id=?)`, in.EntryID).Scan(&entryExists); err != nil {
		return receipt, fmt.Errorf("check anime entry: %w", err)
	}
	if !entryExists {
		return receipt, ErrAnimeInvalid
	}
	oldSources := make(map[string]model.AnimeSource, model.AnimeMaxSources)
	var oldNaming *model.AnimeNamingPlan
	resumeArchived := false
	if in.ID != "" {
		old, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, in.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return receipt, ErrAnimeConflict
		}
		if err != nil {
			return receipt, fmt.Errorf("read anime configuration: %w", err)
		}
		if old.Revision != in.ExpectedRevision || old.Status == "removed" {
			return receipt, ErrAnimeConflict
		}
		if old.RunGeneration >= model.AnimeMaxRevision {
			return receipt, ErrAnimeConflict
		}
		if old.EntryID != in.EntryID {
			return receipt, ErrAnimeIdentity
		}
		if !runtime && (old.FirstEnabledAt != nil || old.Status != "draft" && old.Status != "paused") {
			return receipt, ErrAnimeCapability
		}
		if runtime && !(old.Status == in.Status && (old.Status == "enabled" || old.Status == "paused" || old.Status == "archived") || old.Status == "archived" && in.Status == "enabled") {
			return receipt, ErrAnimeTransition
		}
		oldNaming = old.Naming
		resumeArchived = old.Status == "archived" && in.Status == "enabled"
		sources, err := readAnimeSources(ctx, tx, in.ID, false)
		if err != nil {
			return receipt, err
		}
		for _, source := range sources {
			oldSources[source.ID] = source
		}
	} else {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM anime_subscriptions WHERE entry_id=? AND status<>'removed'`, in.EntryID).Scan(&existing)
		if err == nil {
			return receipt, &AnimeExistsError{Kind: "subscription", ID: existing}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return receipt, fmt.Errorf("find anime subscription: %w", err)
		}
		in.ID, err = newAnimeID()
		if err != nil {
			return receipt, err
		}
	}
	final := make(map[string]model.AnimeSource, len(oldSources)+len(in.Sources))
	for id, source := range oldSources {
		final[id] = source
	}
	now := time.Now().UTC().UnixMilli()
	for _, removal := range in.RemovedSources {
		old, ok := final[removal.ID]
		if !ok || old.Revision != removal.ExpectedRevision {
			return receipt, ErrAnimeConflict
		}
		if old.Revision >= model.AnimeMaxRevision || old.RunGeneration >= model.AnimeMaxRevision {
			return receipt, ErrAnimeConflict
		}
		delete(final, removal.ID)
	}
	receipt = model.AnimeConfigReceipt{ID: in.ID, EntryID: in.EntryID, Revision: in.ExpectedRevision + 1, Status: in.Status, SourceIDs: make(map[string]string, len(in.Sources)), SourceRevisions: make(map[string]int64, len(in.Sources)), RequiresRepreview: true}
	changed := make([]model.AnimeSource, 0, len(in.Sources))
	for _, input := range in.Sources {
		var source model.AnimeSource
		if input.ID != "" {
			old, ok := oldSources[input.ID]
			if !ok || old.Revision != input.ExpectedRevision {
				return receipt, ErrAnimeConflict
			}
			if !old.AcceptsIdentity(input.Provider, input.ItemID, input.GroupID) {
				return receipt, ErrAnimeIdentity
			}
			if !runtime && (old.Status != "draft" && old.Status != "paused") {
				return receipt, ErrAnimeCapability
			}
			source = old
		} else {
			id, err := newAnimeID()
			if err != nil {
				return receipt, err
			}
			ns, err := newAnimeID()
			if err != nil {
				return receipt, err
			}
			source = model.AnimeSource{ID: id, SubscriptionID: in.ID, NamespaceID: ns, Health: "unknown", RunGeneration: 1, CreatedAt: now}
		}
		source.Role, source.Position, source.Status = input.Role, input.Position, input.Status
		if runtime {
			source.Status = "enabled"
		}
		source.RunWhenEnabled, source.Provider = input.RunWhenEnabled, input.Provider
		source.ItemID, source.GroupID, source.GroupName = input.ItemID, input.GroupID, input.GroupName
		source.Binding, source.Rules = input.Binding, input.Rules
		if input.ItemID != nil && input.GroupID != nil {
			key := input.Provider + ":" + *input.ItemID + ":" + *input.GroupID
			source.SourceKey = &key
		}
		old, exists := oldSources[source.ID]
		if !exists || !sameAnimeSourceConfig(old, source) || runtime && (animeSourceNamingChanged(oldNaming, in.Naming, source.ID, input.Ref)) {
			if source.Revision >= model.AnimeMaxRevision || source.RunGeneration >= model.AnimeMaxRevision {
				return receipt, ErrAnimeConflict
			}
			source.Revision++
			if exists {
				source.RunGeneration++
			}
			source.UpdatedAt = now
			changed = append(changed, source)
		}
		final[source.ID] = source
		receipt.SourceIDs[input.Ref] = source.ID
	}
	for id, source := range final {
		receipt.SourceRevisions[id] = source.Revision
	}
	validationSources := final
	if runtime {
		validationSources = make(map[string]model.AnimeSource, len(final))
		for id, source := range final {
			source.Status = "paused"
			validationSources[id] = source
		}
	}
	if err := validateAnimeSources(validationSources, validation.Status); err != nil {
		return receipt, err
	}
	if in.Naming != nil {
		// Copy before converting temporary client references to stable source IDs.
		plan := *in.Naming
		plan.Sources = append([]model.AnimeSourceMapping{}, plan.Sources...)
		seen := make(map[string]bool, len(plan.Sources))
		for i := range plan.Sources {
			m := &plan.Sources[i]
			if id, ok := receipt.SourceIDs[m.SourceRef]; ok {
				m.SourceRef = id
			}
			if _, ok := final[m.SourceRef]; !ok || seen[m.SourceRef] {
				return receipt, ErrAnimeInvalid
			}
			seen[m.SourceRef] = true
		}
		in.Naming = &plan
	}
	naming, err := animeJSON(in.Naming)
	if err != nil {
		return receipt, err
	}
	form, err := animeJSON(in.Form)
	if err != nil {
		return receipt, err
	}
	var result sql.Result
	if in.ExpectedRevision == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO anime_subscriptions(`+animeSubscriptionColumns+`) VALUES(?,?,?,1,1,NULL,?,?,?,?)`, in.ID, in.EntryID, in.Status, form, now, now, naming)
	} else if runtime {
		result, err = tx.ExecContext(ctx, `UPDATE anime_subscriptions SET status=?,revision=revision+1,form_json=?,naming_json=?,updated_at=? WHERE id=? AND revision=?`, in.Status, form, naming, now, in.ID, in.ExpectedRevision)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE anime_subscriptions SET status=?,revision=revision+1,run_generation=run_generation+1,form_json=?,naming_json=?,updated_at=? WHERE id=? AND revision=?`, in.Status, form, naming, now, in.ID, in.ExpectedRevision)
	}
	if err != nil {
		return receipt, animeDBError("save anime subscription", err)
	}
	if err := animeChanged(result); err != nil {
		return receipt, err
	}
	if len(in.RemovedSources) > 0 {
		ids := make([]string, 0, len(in.RemovedSources))
		for _, removal := range in.RemovedSources {
			ids = append(ids, removal.ID)
		}
		if err := deleteAnimeSourcesTx(ctx, tx, in.ID, ids); err != nil {
			return receipt, err
		}
	}
	// Demote an old main before promoting another; the partial unique index is
	// immediate, so an otherwise valid swap must not depend on input ordering.
	sort.SliceStable(changed, func(i, j int) bool { return changed[i].Role == "standby" && changed[j].Role == "main" })
	for _, source := range changed {
		if err := writeAnimeSource(ctx, tx, source, oldSources[source.ID].Revision); err != nil {
			return receipt, err
		}
		if runtime {
			if err := invalidateAnimeSourceConfigurationTx(ctx, tx, source.ID, now); err != nil {
				return receipt, err
			}
		}
	}
	if runtime {
		if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET health='unknown',error_code=NULL,retry_after=NULL,next_check_at=?,check_token=NULL,check_until=NULL,poll_failures=0 WHERE subscription_id=? AND status='enabled' AND run_when_enabled=1 AND health='unavailable'`, now, in.ID); err != nil {
			return receipt, fmt.Errorf("retry unavailable anime sources: %w", err)
		}
		if resumeArchived {
			if err := resetAnimeSubscriptionActivityTx(ctx, tx, in.ID, now); err != nil {
				return receipt, err
			}
			if _, err := controlAnimeQueuedJobsTx(ctx, tx, in.ID, true, now); err != nil {
				return receipt, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE anime_subscriptions SET first_enabled_at=COALESCE(first_enabled_at,?) WHERE id=?`, now, in.ID); err != nil {
			return receipt, err
		}
		receipt.RequiresRepreview = false
	}
	if key != nil {
		raw, err := animeJSON(receipt)
		if err != nil {
			return receipt, err
		}
		status := 200
		if in.ExpectedRevision == 0 {
			status = 201
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_request_keys(actor_key,operation,request_key,request_digest,http_status,receipt_json,created_at) VALUES(?,?,?,?,?,?,?)`, key.ActorKey, key.Operation, key.Key, key.Digest, status, raw, now); err != nil {
			return receipt, animeDBError("save anime receipt", err)
		}
	}
	return receipt, nil
}

func animeSourceNamingChanged(old, next *model.AnimeNamingPlan, id, ref string) bool {
	if old == nil || next == nil {
		return old != next
	}
	if !reflect.DeepEqual(old.Target, next.Target) {
		return true
	}
	var a, b *model.AnimeSourceMapping
	for i := range old.Sources {
		if old.Sources[i].SourceRef == id {
			value := old.Sources[i]
			value.SourceRef = id
			a = &value
		}
	}
	for i := range next.Sources {
		if next.Sources[i].SourceRef == ref || next.Sources[i].SourceRef == id {
			value := next.Sources[i]
			value.SourceRef = id
			b = &value
		}
	}
	return !reflect.DeepEqual(a, b)
}

func sameAnimeSourceConfig(a, b model.AnimeSource) bool {
	return a.Role == b.Role && a.Position == b.Position && a.Status == b.Status && a.RunWhenEnabled == b.RunWhenEnabled && a.Provider == b.Provider && sameAnimeString(a.ItemID, b.ItemID) && sameAnimeString(a.GroupID, b.GroupID) && sameAnimeString(a.GroupName, b.GroupName) && reflect.DeepEqual(a.Binding, b.Binding) && reflect.DeepEqual(a.Rules, b.Rules)
}

func validateAnimeSources(sources map[string]model.AnimeSource, status string) error {
	if len(sources) > model.AnimeMaxSources {
		return fmt.Errorf("%w: at most five sources", ErrAnimeInvalid)
	}
	mainCount, standbyCount, validMain := 0, 0, false
	keys := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.Role == "main" {
			mainCount++
			validMain = source.Status == "paused"
		} else {
			standbyCount++
		}
		if source.SourceKey != nil {
			if keys[*source.SourceKey] {
				return ErrAnimeExists
			}
			keys[*source.SourceKey] = true
		}
	}
	if mainCount > 1 || standbyCount > model.AnimeMaxSources-1 || status == "paused" && (mainCount != 1 || !validMain) {
		return fmt.Errorf("%w: paused configuration requires a complete main source", ErrAnimeInvalid)
	}
	return nil
}

func writeAnimeSource(ctx context.Context, tx *sql.Tx, source model.AnimeSource, previousRevision int64) error {
	binding, err := animeJSON(source.Binding)
	if err != nil {
		return err
	}
	rules, err := animeJSON(source.Rules)
	if err != nil {
		return err
	}
	var result sql.Result
	if previousRevision == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO anime_subscription_sources(id,subscription_id,role,position,status,run_when_enabled,provider,source_key,item_id,group_id,group_name,namespace_id,binding_json,rules_json,revision,run_generation,health,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, source.ID, source.SubscriptionID, source.Role, source.Position, source.Status, source.RunWhenEnabled, source.Provider, source.SourceKey, source.ItemID, source.GroupID, source.GroupName, source.NamespaceID, binding, rules, source.Revision, source.RunGeneration, source.Health, source.CreatedAt, source.UpdatedAt)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET role=?,position=?,status=?,run_when_enabled=?,source_key=?,item_id=?,group_id=?,group_name=?,binding_json=?,rules_json=?,revision=?,run_generation=?,updated_at=? WHERE id=? AND subscription_id=? AND revision=?`, source.Role, source.Position, source.Status, source.RunWhenEnabled, source.SourceKey, source.ItemID, source.GroupID, source.GroupName, binding, rules, source.Revision, source.RunGeneration, source.UpdatedAt, source.ID, source.SubscriptionID, previousRevision)
	}
	if err != nil {
		return animeDBError("save anime source", err)
	}
	return animeChanged(result)
}
