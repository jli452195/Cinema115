package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
)

// Shared by the local preview and the write transaction. A download finishing
// is not terminal while its file collection/organization is still running.
const animeRedownloadEligible = `(j.workflow_state IN ('organized','original_saved','cancelled') OR j.download_state IN ('failed','cancelled') OR (j.download_state='blocked' AND EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind='submit' AND o.state='failed')))
 AND (j.lease_until IS NULL OR j.lease_until<=?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs newer WHERE newer.claim_id=j.claim_id AND newer.attempt>j.attempt)
 AND NOT EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.state IN ('dispatched','unknown'))
 AND COALESCE(json_extract(j.directory_operation_json,'$.state'),'') NOT IN ('dispatched','unknown')`

type AnimeRedownloadItem struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type AnimeRedownloadInput struct {
	ExpectedRevision int64                 `json:"expectedRevision"`
	SettingsRevision int64                 `json:"settingsRevision"`
	SourceID         string                `json:"sourceId"`
	Items            []AnimeRedownloadItem `json:"items"`
}

type AnimeRedownloadResult struct {
	ID    string `json:"id"`
	JobID string `json:"jobId,omitempty"`
	Error string `json:"error,omitempty"`
}

type AnimeRedownloadReceipt struct {
	ID       string                  `json:"id"`
	Revision int64                   `json:"revision"`
	Results  []AnimeRedownloadResult `json:"results"`
}

// Scheduling is local and idempotent. Each accepted resource gets a fresh
// attempt under its existing claim, without copying file IDs or checkpoints.
// Normal discovery continues linking the latest attempt instead of reissuing it.
func (s *Store) RedownloadAnime(ctx context.Context, id string, in AnimeRedownloadInput, key AnimeWriteKey) (AnimeRedownloadReceipt, error) {
	out := AnimeRedownloadReceipt{ID: id, Results: make([]AnimeRedownloadResult, 0, len(in.Items))}
	if !model.ValidAnimeID(id) || !model.ValidAnimeID(in.SourceID) || in.ExpectedRevision < 1 || in.ExpectedRevision >= model.AnimeMaxRevision || in.SettingsRevision < 1 || in.SettingsRevision >= model.AnimeMaxRevision || len(in.Items) == 0 || len(in.Items) > 2000 {
		return out, ErrAnimeInvalid
	}
	seen := make(map[string]bool, len(in.Items))
	for _, item := range in.Items {
		if !model.ValidAnimeID(item.ID) || item.Revision < 1 || seen[item.ID] {
			return out, ErrAnimeInvalid
		}
		seen[item.ID] = true
	}
	if err := key.validate(); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if found, err := readAnimeActionReceiptTx(ctx, tx, key, &out); found || err != nil {
		return out, err
	}
	sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, id))
	if err != nil {
		return out, err
	}
	if sub.Revision != in.ExpectedRevision {
		return out, ErrAnimeConflict
	}
	source, err := scanAnimeSource(tx.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=? AND subscription_id=? AND status<>'removed'`, in.SourceID, id))
	if err != nil {
		return out, err
	}
	settings, err := readAnimeSettingsTx(ctx, tx)
	if err != nil {
		return out, err
	}
	if settings.Revision != in.SettingsRevision {
		return out, ErrAnimeSettingsChanged
	}
	target, err := animeTargetFromSettings(settings)
	if err != nil {
		return out, err
	}
	if err := checkAnimeDownloadTargetTx(ctx, tx, target, true); err != nil {
		return out, err
	}
	template, err := readNamingTemplate(ctx, tx, naming.TV)
	if err != nil {
		return out, err
	}
	organization, err := model.FreezeAnimeOrganization(sub.Naming, settings, sub.Revision, target, template)
	if err != nil {
		return out, ErrAnimeDirectoryRequired
	}
	now := time.Now().UnixMilli()
	for _, item := range in.Items {
		result := AnimeRedownloadResult{ID: item.ID}
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT snapshot_json FROM anime_job_selections WHERE job_id=? AND source_id=? AND status='authorized' LIMIT 1`, item.ID, source.ID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			result.Error = "资源不属于当前来源，或已取消关联"
			out.Results = append(out.Results, result)
			continue
		}
		if err != nil {
			return out, err
		}
		var snapshot model.AnimeDownloadSnapshot
		if err := decodeAnimeJSON(raw, &snapshot); err != nil {
			return out, err
		}
		old, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs j WHERE j.id=? AND j.revision=? AND `+animeRedownloadEligible, item.ID, item.Revision, now))
		if errors.Is(err, sql.ErrNoRows) {
			result.Error = "任务仍在处理、结果待核对或记录已变化，请刷新后重试"
			out.Results = append(out.Results, result)
			continue
		}
		if err != nil {
			return out, err
		}
		if !sameAnimeDownloadBinding(old.Snapshot.Target, target) || source.ItemID == nil || source.GroupID == nil || source.GroupName == nil || snapshot.Source.Provider != source.Provider || snapshot.Source.ItemID != *source.ItemID || snapshot.Source.GroupID != *source.GroupID || snapshot.InfoHash != old.Snapshot.InfoHash || snapshot.Source.GroupName != old.Snapshot.Source.GroupName {
			result.Error = "主账号、下载目录或来源已变化，不能沿用原资源记录"
			out.Results = append(out.Results, result)
			continue
		}
		snapshot.Redownload, snapshot.Target, snapshot.Naming, snapshot.Organization = true, target, sub.Naming, organization
		snapshot.Source.SubscriptionRevision, snapshot.Source.SubscriptionGeneration = sub.Revision, sub.RunGeneration
		snapshot.Source.SourceRevision, snapshot.Source.SourceGeneration = source.Revision, source.RunGeneration
		encoded, err := animeJSON(snapshot)
		if err != nil {
			return out, err
		}
		jobID, err := newAnimeID()
		if err != nil {
			return out, err
		}
		// Retire an ended failed/blocked attempt before reserving the same claim.
		if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET workflow_state='cancelled',generation=generation+1,next_run_at=NULL,lease_token=NULL,lease_until=NULL,revision=revision+1,updated_at=? WHERE id=? AND workflow_state='active'`, now, old.ID); err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_download_jobs(id,claim_id,attempt,account_id,uid,settings_revision,parent_cid,directory_name,snapshot_json,download_state,file_state,workflow_state,revision,generation,created_at,updated_at,next_run_at) VALUES(?,?,?,?,?,?,?,?,?,'queued','pending','active',1,1,?,?,?)`, jobID, old.ClaimID, old.Attempt+1, target.AccountID, old.UID, target.SettingsRevision, target.ParentCID, "anime-"+jobID, encoded, now, now, now); err != nil {
			return out, animeDBError("create anime redownload attempt", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_job_selections(job_id,source_id,release_id,status,source_revision,snapshot_json,created_at) SELECT ?,source_id,release_id,status,source_revision,snapshot_json,? FROM anime_job_selections WHERE job_id=? AND status='authorized'`, jobID, now, old.ID); err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE anime_job_selections SET source_revision=?,snapshot_json=? WHERE job_id=? AND source_id=?`, source.Revision, encoded, jobID, source.ID); err != nil {
			return out, err
		}
		result.JobID = jobID
		out.Results = append(out.Results, result)
	}
	out.Revision = sub.Revision
	if err := writeAnimeActionReceiptTx(ctx, tx, key, out, now); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit anime redownload: %w", err)
	}
	return out, nil
}
