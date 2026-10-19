package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/model"
)

var (
	ErrAnimeSelectionWithdrawn = errors.New("anime release selection withdrawn")
	ErrAnimeResourceTarget     = errors.New("anime resource target conflict")
	ErrAnimeResourceSource     = errors.New("anime resource publisher conflict")
)

const animeJobColumns = `id,claim_id,attempt,account_id,account_revision,uid,settings_revision,parent_cid,directory_name,directory_cid,snapshot_json,download_state,file_state,workflow_state,block_code,revision,generation,created_at,updated_at,raw_status_json,result_object_id,result_object_kind,next_run_at,directory_operation_json,files_json`

// FindAnimeVerifiedRelease reuses only an exact, committed observation proof.
// New content or a new verifier version cannot inherit an old post's identity.
func (s *Store) FindAnimeVerifiedRelease(ctx context.Context, releaseID, fingerprint string) (animeresource.Verified, bool, error) {
	if !model.ValidAnimeID(releaseID) || len(fingerprint) != 67 {
		return animeresource.Verified{}, false, ErrAnimeInvalid
	}
	var raw, hash string
	var verifiedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT js.snapshot_json,r.info_hash,r.verified_at FROM anime_releases rel JOIN anime_resources r ON r.id=rel.resource_id JOIN anime_job_selections js ON js.release_id=rel.id WHERE rel.id=? AND rel.content_fingerprint=? AND json_extract(js.snapshot_json,'$.evidence.contentFingerprint')=? AND json_extract(js.snapshot_json,'$.verificationVersion')=? ORDER BY js.created_at LIMIT 1`, releaseID, fingerprint, fingerprint, animeresource.VerificationVersion).Scan(&raw, &hash, &verifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return animeresource.Verified{}, false, nil
	}
	if err != nil {
		return animeresource.Verified{}, false, fmt.Errorf("read anime verification cache: %w", err)
	}
	var snapshot model.AnimeDownloadSnapshot
	if err := decodeAnimeJSON(raw, &snapshot); err != nil {
		return animeresource.Verified{}, false, fmt.Errorf("decode anime verification cache: %w", err)
	}
	if snapshot.Protocol != "bt_v1" || snapshot.InfoHash != hash || snapshot.Release.ID != releaseID || snapshot.Release.ContentFingerprint != fingerprint {
		return animeresource.Verified{}, false, ErrAnimeIdentity
	}
	v, err := animeresource.Restore(hash, snapshot.VerificationVersion, verifiedAt, snapshot.Evidence)
	return v, err == nil, err
}

func (s *Store) GetAnimeSource(ctx context.Context, id string) (model.AnimeSource, error) {
	if !model.ValidAnimeID(id) {
		return model.AnimeSource{}, ErrAnimeInvalid
	}
	source, err := scanAnimeSource(s.db.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=?`, id))
	if err != nil {
		return source, fmt.Errorf("get anime source: %w", err)
	}
	return source, nil
}

func scanAnimeDownloadJob(row animeScanner) (model.AnimeDownloadJob, error) {
	var job model.AnimeDownloadJob
	var raw string
	var tracking *string
	var directory, files *string
	err := row.Scan(&job.ID, &job.ClaimID, &job.Attempt, &job.AccountID, &job.AccountRevision, &job.UID, &job.SettingsRevision, &job.ParentCID, &job.DirectoryName, &job.DirectoryCID, &raw, &job.DownloadState, &job.FileState, &job.WorkflowState, &job.BlockCode, &job.Revision, &job.Generation, &job.CreatedAt, &job.UpdatedAt, &tracking, &job.ResultObjectID, &job.ResultObjectKind, &job.NextRunAt, &directory, &files)
	if err != nil {
		return job, err
	}
	if err := decodeAnimeJSON(raw, &job.Snapshot); err != nil {
		return job, fmt.Errorf("decode anime download snapshot: %w", err)
	}
	if tracking != nil {
		job.Tracking = &model.AnimeDownloadTracking{}
		if err := decodeAnimeJSON(*tracking, job.Tracking); err != nil {
			return job, fmt.Errorf("decode anime download tracking: %w", err)
		}
	}
	if directory != nil {
		job.DirectoryOperation = &model.AnimeDirectoryOperation{}
		if err := decodeAnimeJSON(*directory, job.DirectoryOperation); err != nil {
			return job, fmt.Errorf("decode anime directory intent: %w", err)
		}
	}
	if files != nil {
		job.Files = &model.AnimeFileManifest{}
		if err := decodeAnimeJSON(*files, job.Files); err != nil {
			return job, fmt.Errorf("decode anime file manifest: %w", err)
		}
	}
	return job, nil
}

func (s *Store) GetAnimeDownloadJob(ctx context.Context, id string) (model.AnimeDownloadJob, error) {
	if !model.ValidAnimeID(id) {
		return model.AnimeDownloadJob{}, ErrAnimeInvalid
	}
	job, err := scanAnimeDownloadJob(s.db.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, id))
	if err != nil {
		return job, fmt.Errorf("get anime download job: %w", err)
	}
	return job, nil
}

func (s *Store) GetAnimeReleaseDecision(ctx context.Context, sourceID, releaseID string) (model.AnimeReleaseDecision, error) {
	if !model.ValidAnimeID(sourceID) || !model.ValidAnimeID(releaseID) {
		return model.AnimeReleaseDecision{}, ErrAnimeInvalid
	}
	return readAnimeReleaseDecision(ctx, s.db, sourceID, releaseID)
}

func readAnimeReleaseDecision(ctx context.Context, r animeReader, sourceID, releaseID string) (model.AnimeReleaseDecision, error) {
	var d model.AnimeReleaseDecision
	var raw string
	err := r.QueryRowContext(ctx, `SELECT source_id,release_id,disposition,reason_code,source_revision,release_fingerprint,snapshot_json,generation,updated_at FROM anime_release_decisions WHERE source_id=? AND release_id=?`, sourceID, releaseID).Scan(&d.SourceID, &d.ReleaseID, &d.Disposition, &d.ReasonCode, &d.SourceRevision, &d.ReleaseFingerprint, &raw, &d.Generation, &d.UpdatedAt)
	if err != nil {
		return d, fmt.Errorf("read anime selected release: %w", err)
	}
	if err := decodeAnimeJSON(raw, &d.Snapshot); err != nil {
		return d, fmt.Errorf("decode anime selected release: %w", err)
	}
	return d, nil
}

// ClaimAnimeDownload consumes only an authoritative pending execution choice,
// never a P1 form selection. Verification must come from the controlled source
// observation. No network operation or automatic retry takes place here.
func (s *Store) ClaimAnimeDownload(ctx context.Context, sourceID, releaseID string, decisionGeneration int64, verified animeresource.Verified, target model.AnimeDownloadTarget) (model.AnimeDownloadClaimResult, error) {
	var out model.AnimeDownloadClaimResult
	if !model.ValidAnimeID(sourceID) || !model.ValidAnimeID(releaseID) || decisionGeneration < 1 || decisionGeneration >= model.AnimeMaxRevision || !verified.Valid() || target.Validate() != nil {
		return out, ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin anime resource claim: %w", err)
	}
	defer tx.Rollback()
	out, err = claimAnimeDownloadTx(ctx, tx, sourceID, releaseID, decisionGeneration, verified, target)
	if err != nil {
		return model.AnimeDownloadClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.AnimeDownloadClaimResult{}, animeDBError("commit anime resource claim", err)
	}
	return out, nil
}

func claimAnimeDownloadTx(ctx context.Context, tx *sql.Tx, sourceID, releaseID string, generation int64, verified animeresource.Verified, target model.AnimeDownloadTarget) (model.AnimeDownloadClaimResult, error) {
	var out model.AnimeDownloadClaimResult
	if err := checkAnimeDownloadTargetTx(ctx, tx, target, true); err != nil {
		return out, err
	}
	d, err := readAnimeReleaseDecision(ctx, tx, sourceID, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrAnimeConflict
	}
	if err != nil {
		return out, err
	}
	if d.Disposition == "dismissed" {
		return out, ErrAnimeSelectionWithdrawn
	}
	if d.Generation != generation || d.Disposition != "selected" && d.Disposition != "linked" {
		return out, ErrAnimeConflict
	}
	source, err := scanAnimeSource(tx.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=?`, sourceID))
	if err != nil {
		return out, fmt.Errorf("read anime claim source: %w", err)
	}
	sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, source.SubscriptionID))
	if err != nil {
		return out, fmt.Errorf("read anime claim subscription: %w", err)
	}
	if sub.Status != "enabled" || sub.FirstEnabledAt == nil || source.Status != "enabled" || !source.RunWhenEnabled || source.ActiveBaselineID == nil || source.Revision != d.SourceRevision {
		return out, ErrAnimeConflict
	}
	basis := d.Snapshot.Selection
	if basis == nil || !model.ValidAnimeID(basis.BaselineID) || basis.BaselineID != *source.ActiveBaselineID || basis.SelectedAt <= 0 || basis.Kind != "initial_history" && basis.Kind != "automatic" {
		return out, ErrAnimeInvalid
	}
	if basis.Target != target || basis.SubscriptionGeneration != sub.RunGeneration || basis.SourceGeneration != source.RunGeneration {
		return out, ErrAnimeConflict
	}
	var baselineOK bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_source_baselines WHERE id=? AND source_id=? AND source_revision<=?)`, basis.BaselineID, sourceID, source.Revision).Scan(&baselineOK); err != nil {
		return out, fmt.Errorf("check anime selection baseline: %w", err)
	}
	if !baselineOK {
		return out, ErrAnimeConflict
	}
	var initial bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_baseline_releases WHERE baseline_id=? AND release_id=?)`, basis.BaselineID, releaseID).Scan(&initial); err != nil {
		return out, fmt.Errorf("check anime initial selection: %w", err)
	}
	if initial != (basis.Kind == "initial_history") {
		return out, ErrAnimeConflict
	}
	release, err := scanAnimeRelease(tx.QueryRowContext(ctx, `SELECT `+animeReleaseColumns+` FROM anime_releases WHERE id=?`, releaseID))
	if err != nil {
		return out, fmt.Errorf("read anime claim release: %w", err)
	}
	evidence := verified.Evidence()
	if release.Provider != source.Provider || evidence.Provider != release.Provider || evidence.PostKey != release.PostKey || evidence.ContentFingerprint != release.ContentFingerprint || d.ReleaseFingerprint != release.ContentFingerprint {
		return out, ErrAnimeIdentity
	}
	if d.Snapshot.SchemaVersion != model.AnimeSchemaVersion || !reflect.DeepEqual(d.Snapshot.Binding, source.Binding) || !reflect.DeepEqual(d.Snapshot.Rules, source.Rules) || !reflect.DeepEqual(d.Snapshot.Parse, release.Parse) || d.Snapshot.Title != release.Title || source.ItemID == nil || source.GroupID == nil || source.GroupName == nil {
		return out, ErrAnimeConflict
	}
	if d.Snapshot.RuleVersion != animepreview.RuleVersion || d.Snapshot.ParserVersion != release.ParserVersion || d.Snapshot.TimeVersion != release.TimeVersion || release.ParserVersion != animeparse.ParserVersion || release.TimeVersion != animeparse.TimeVersion {
		return out, ErrAnimeConflict
	}
	if source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil {
		return out, ErrAnimeInvalid
	}
	match, err := animepreview.MatchesRelease(source.Binding, source.Rules, release)
	if err != nil {
		return out, fmt.Errorf("check anime selected rules: %w", err)
	}
	if !match {
		return out, ErrAnimeConflict
	}
	now := time.Now().UTC().UnixMilli()
	resourceID, err := saveAnimeVerifiedResourceTx(ctx, tx, verified)
	if err != nil {
		return out, err
	}
	var oldResource *string
	if err := tx.QueryRowContext(ctx, `SELECT resource_id FROM anime_releases WHERE id=?`, releaseID).Scan(&oldResource); err != nil {
		return out, err
	}
	if oldResource != nil && *oldResource != resourceID {
		return out, ErrAnimeIdentity
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_releases SET resource_id=? WHERE id=?`, resourceID, releaseID); err != nil {
		return out, animeDBError("associate anime verified resource", err)
	}
	var claimID, claimAccount, claimCID string
	err = tx.QueryRowContext(ctx, `SELECT id,account_id,target_cid FROM anime_resource_claims WHERE uid=? AND resource_id=?`, target.UID, resourceID).Scan(&claimID, &claimAccount, &claimCID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("find anime resource claim: %w", err)
	}
	if err == nil && (claimAccount != target.AccountID || claimCID != target.ParentCID) {
		return out, ErrAnimeResourceTarget
	}
	if errors.Is(err, sql.ErrNoRows) {
		claimID, err = newAnimeID()
		if err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_resource_claims(id,uid,resource_id,account_id,target_cid,created_at) VALUES(?,?,?,?,?,?)`, claimID, target.UID, resourceID, target.AccountID, target.ParentCID, now); err != nil {
			return out, animeDBError("claim anime resource", err)
		}
		out.Created = true
	}
	snapshot := model.AnimeDownloadSnapshot{
		SchemaVersion: model.AnimeSchemaVersion, Target: target, Protocol: "bt_v1", InfoHash: verified.Hash(), VerificationVersion: animeresource.VerificationVersion,
		Evidence: evidence, Release: release, Decision: d.Snapshot, Naming: sub.Naming,
		Source: model.AnimeDownloadSource{SubscriptionID: sub.ID, SubscriptionRevision: sub.Revision, SubscriptionGeneration: sub.RunGeneration, SourceID: source.ID, SourceRevision: source.Revision, SourceGeneration: source.RunGeneration, Provider: source.Provider, ItemID: *source.ItemID, GroupID: *source.GroupID, GroupName: *source.GroupName, NamespaceID: source.NamespaceID},
	}
	raw, err := animeJSON(snapshot)
	if err != nil {
		return out, err
	}
	if out.Created {
		id, err := newAnimeID()
		if err != nil {
			return out, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO anime_download_jobs(id,claim_id,attempt,account_id,account_revision,uid,settings_revision,parent_cid,directory_name,snapshot_json,download_state,file_state,workflow_state,revision,generation,created_at,updated_at) VALUES(?,?,1,?,?,?,?,?,?,?,'queued','pending','active',1,1,?,?)`, id, claimID, target.AccountID, target.AccountRevision, target.UID, target.SettingsRevision, target.ParentCID, "anime-"+id, raw, now, now)
		if err != nil {
			return out, animeDBError("create anime download intent", err)
		}
	}
	// Always link the latest attempt, including failed/cancelled/completed ones.
	// Discovery never silently retries a claim whose old result needs review.
	out.Job, err = scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE claim_id=? ORDER BY attempt DESC LIMIT 1`, claimID))
	if err != nil {
		return out, fmt.Errorf("read anime claimed job: %w", err)
	}
	job := out.Job
	if job.AccountID != target.AccountID || job.AccountRevision != target.AccountRevision || job.UID != target.UID || job.ParentCID != target.ParentCID || job.Snapshot.InfoHash != verified.Hash() || job.Snapshot.Protocol != "bt_v1" {
		return out, ErrAnimeResourceTarget
	}
	if job.Snapshot.Source.GroupName != *source.GroupName {
		return out, ErrAnimeResourceSource
	}
	var existingStatus string
	err = tx.QueryRowContext(ctx, `SELECT status FROM anime_job_selections WHERE job_id=? AND source_id=? AND release_id=?`, job.ID, sourceID, releaseID).Scan(&existingStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("check existing anime authorization: %w", err)
	}
	if existingStatus == "withdrawn" {
		return out, ErrAnimeSelectionWithdrawn
	}
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_job_selections(job_id,source_id,release_id,status,source_revision,snapshot_json,created_at) VALUES(?,?,?,'authorized',?,?,?)`, job.ID, sourceID, releaseID, source.Revision, raw, now); err != nil {
			return out, animeDBError("authorize anime job selection", err)
		}
		if !out.Created && d.Disposition != "linked" {
			if _, err := tx.ExecContext(ctx, `UPDATE anime_release_decisions SET disposition='linked',generation=generation+1,updated_at=? WHERE source_id=? AND release_id=? AND generation=?`, now, sourceID, releaseID, generation); err != nil {
				return out, animeDBError("link anime resource decision", err)
			}
			d.Generation++
		}
	}
	out.DecisionGeneration = d.Generation
	return out, nil
}

func saveAnimeVerifiedResourceTx(ctx context.Context, tx *sql.Tx, verified animeresource.Verified) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM anime_resources WHERE protocol='bt_v1' AND info_hash=?`, verified.Hash()).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("find anime verified identity: %w", err)
	}
	id, err = newAnimeID()
	if err != nil {
		return "", err
	}
	raw, err := animeJSON(verified.Evidence())
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO anime_resources(id,protocol,info_hash,verification_version,verified_at,evidence_json) VALUES(?,'bt_v1',?,?,?,?)`, id, verified.Hash(), animeresource.VerificationVersion, verified.VerifiedAt(), raw); err != nil {
		return "", animeDBError("save anime verified identity", err)
	}
	return id, nil
}
