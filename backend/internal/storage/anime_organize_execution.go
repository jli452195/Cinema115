package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"time"

	"strm115/backend/internal/animeorganize"
	"strm115/backend/internal/model"
)

const animeOrganizeColumns = `id,download_job_id,generation,request_json,result_json,updated_at`

func scanAnimeOrganize(row animeScanner) (model.AnimeOrganizeOperation, error) {
	var op model.AnimeOrganizeOperation
	var request, progress string
	if err := row.Scan(&op.ID, &op.JobID, &op.Generation, &request, &progress, &op.UpdatedAt); err != nil {
		return op, err
	}
	if err := decodeAnimeJSON(request, &op.Request); err != nil {
		return op, err
	}
	return op, decodeAnimeJSON(progress, &op.Progress)
}

func (s *Store) GetAnimeOrganization(ctx context.Context, jobID string) (model.AnimeOrganizeOperation, error) {
	if !model.ValidAnimeID(jobID) {
		return model.AnimeOrganizeOperation{}, ErrAnimeInvalid
	}
	return scanAnimeOrganize(s.db.QueryRowContext(ctx, `SELECT `+animeOrganizeColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='organize'`, jobID))
}

func (s *Store) PrepareAnimeOrganization(ctx context.Context, expected model.AnimeDownloadJob, token string) (model.AnimeOrganizeOperation, error) {
	var empty model.AnimeOrganizeOperation
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err := checkAnimeLeaseTx(ctx, tx, expected.ID, token); err != nil {
		return empty, err
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, expected.ID))
	if err != nil {
		return empty, err
	}
	if job.Generation != expected.Generation || job.WorkflowState != "active" || job.FileState != "verified" || job.DownloadState != "completed" || job.Snapshot.Organization == nil {
		return empty, ErrAnimeConflict
	}
	op, err := scanAnimeOrganize(tx.QueryRowContext(ctx, `SELECT `+animeOrganizeColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='organize'`, job.ID))
	if err == nil {
		if op.Generation != job.Generation || op.Request.Target != job.Snapshot.Organization.Target {
			return empty, ErrAnimeConflict
		}
		return op, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	if err := checkAnimeDownloadTargetTx(ctx, tx, job.Snapshot.Target, false); err != nil {
		return empty, err
	}
	if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err != nil {
		return empty, err
	}
	plan, err := animeorganize.Build(job)
	if err != nil {
		return empty, err
	}
	id, err := newAnimeID()
	if err != nil {
		return empty, err
	}
	op = model.AnimeOrganizeOperation{ID: id, JobID: job.ID, Generation: job.Generation, UpdatedAt: time.Now().UnixMilli(), Request: model.AnimeOrganizeRequest{SchemaVersion: 1, Target: job.Snapshot.Organization.Target, Files: []model.AnimeOrganizeFile{}}, Progress: model.AnimeOrganizeProgress{State: "working", Files: []model.AnimeOrganizeFileProgress{}, Directories: []model.AnimeOrganizeDirectory{}}}
	entries := make(map[string]model.AnimeDriveEntry, len(job.Files.Entries))
	for _, entry := range job.Files.Entries {
		entries[entry.ID] = entry
	}
	for _, item := range plan.Items {
		if item.Status != "ready" {
			continue
		}
		entry := entries[item.ID]
		op.Request.Files = append(op.Request.Files, model.AnimeOrganizeFile{ID: item.ID, SourceCID: entry.ParentCID, OriginalName: entry.Name, TargetPath: strings.TrimPrefix(item.TargetPath, strings.TrimRight(op.Request.Target.ParentPath, "/")+"/")})
		op.Progress.Files = append(op.Progress.Files, model.AnimeOrganizeFileProgress{ID: item.ID, Renamed: entry.Name == path.Base(item.TargetPath)})
	}
	state := "prepared"
	if plan.NeedsReview > 0 || plan.Ready == 0 {
		op.Progress.State, op.Progress.Code, state = "needs_review", "ORGANIZE_MAPPING_REQUIRED", "failed"
	}
	raw, err := animeJSON(op.Request)
	if err != nil {
		return empty, err
	}
	progress, err := animeJSON(op.Progress)
	if err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO anime_drive_operations(id,download_job_id,operation_key,kind,state,generation,request_json,result_json,error_code,updated_at) VALUES(?,?,?,'organize',?,?,?,?,NULLIF(?,''),?)`, id, job.ID, "organize:"+job.ID, state, job.Generation, raw, progress, op.Progress.Code, op.UpdatedAt); err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code=NULLIF(?,''),revision=revision+1,updated_at=? WHERE id=?`, op.Progress.Code, op.UpdatedAt, job.ID); err != nil {
		return empty, err
	}
	return op, tx.Commit()
}

// Checkpoint commits intent BEFORE external writes. A pending step can only be
// resolved by observation, never dispatched a second time. Receipts are facts
// and may be saved after pause; new writes require fresh authorization.
func (s *Store) CheckpointAnimeOrganization(ctx context.Context, expected model.AnimeOrganizeOperation, next model.AnimeOrganizeProgress, dispatch bool, token string) (model.AnimeOrganizeOperation, error) {
	var empty model.AnimeOrganizeOperation
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err := checkAnimeLeaseTx(ctx, tx, expected.JobID, token); err != nil {
		return empty, err
	}
	op, err := scanAnimeOrganize(tx.QueryRowContext(ctx, `SELECT `+animeOrganizeColumns+` FROM anime_drive_operations WHERE id=? AND kind='organize'`, expected.ID))
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(op, expected) || op.Progress.State == "done" {
		return empty, ErrAnimeConflict
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, op.JobID))
	if err != nil {
		return empty, err
	}
	if job.Generation != op.Generation || job.WorkflowState != "active" || job.FileState != "verified" || job.Snapshot.Organization == nil || op.Request.Target != job.Snapshot.Organization.Target {
		return empty, ErrAnimeConflict
	}
	if dispatch {
		if op.Progress.Step != nil || next.Step == nil || next.State != "working" {
			return empty, ErrAnimeConflict
		}
		valid := validAnimeOrganizeStep(op.Request, op.Progress, *next.Step)
		if next.Step.Kind == "cleanup" {
			valid = validAnimeCleanupStep(job, op, *next.Step)
		}
		if !valid {
			return empty, ErrAnimeConflict
		}
		unchanged := next
		unchanged.Step = nil
		old := op.Progress
		old.Code = ""
		old.State = "working"
		old.RetryAt = 0
		if !reflect.DeepEqual(unchanged, old) {
			return empty, ErrAnimeInvalid
		}
		if err := checkAnimeDownloadTargetTx(ctx, tx, job.Snapshot.Target, false); err != nil {
			return empty, err
		}
		if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err != nil {
			return empty, err
		}
	}
	if len(next.Files) != len(op.Progress.Files) || len(next.Directories) < len(op.Progress.Directories) {
		return empty, ErrAnimeInvalid
	}
	if !dispatch && !validAnimeOrganizeProgress(op, next) {
		return empty, ErrAnimeInvalid
	}
	all := len(next.Files) > 0
	for i, f := range next.Files {
		old := op.Progress.Files[i]
		if f.ID != old.ID || old.Renamed && !f.Renamed || old.Moved && !f.Moved || f.Moved && !f.Renamed {
			return empty, ErrAnimeInvalid
		}
		all = all && f.Moved
	}
	if next.State == "done" && (!all || next.Step != nil || next.Code != "") {
		return empty, ErrAnimeInvalid
	}
	state := "prepared"
	switch next.State {
	case "working":
		if next.Step != nil {
			state = "dispatched"
		}
	case "unknown":
		if next.Step == nil {
			return empty, ErrAnimeInvalid
		}
		state = "unknown"
	case "needs_review":
		state = "failed"
	case "done":
		state = "confirmed"
	default:
		return empty, ErrAnimeInvalid
	}
	raw, err := animeJSON(next)
	if err != nil {
		return empty, err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE anime_drive_operations SET state=?,result_json=?,error_code=NULLIF(?,''),authorized_at=CASE WHEN ? THEN ? ELSE authorized_at END,updated_at=? WHERE id=?`, state, raw, next.Code, dispatch, now, now, op.ID); err != nil {
		return empty, err
	}
	workflow := "active"
	var due *int64
	if next.State == "done" {
		workflow = "organized"
	} else if next.State == "working" || next.State == "unknown" {
		at := now
		if next.State == "unknown" {
			at += (5 * time.Second).Milliseconds()
		}
		at = max(at, next.RetryAt)
		due = &at
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET workflow_state=?,block_code=NULLIF(?,''),next_run_at=?,lease_token=CASE WHEN ?='organized' THEN NULL ELSE lease_token END,lease_until=CASE WHEN ?='organized' THEN NULL ELSE lease_until END,revision=revision+1,updated_at=? WHERE id=?`, workflow, next.Code, due, workflow, workflow, now, op.JobID); err != nil {
		return empty, err
	}
	op.Progress, op.UpdatedAt = next, now
	if workflow == "organized" {
		if err := evaluateAnimeJobLifecycleTx(ctx, tx, op.JobID, now); err != nil {
			return empty, fmt.Errorf("evaluate organized anime lifecycle: %w", err)
		}
	}
	return op, tx.Commit()
}

func validAnimeOrganizeProgress(op model.AnimeOrganizeOperation, next model.AnimeOrganizeProgress) bool {
	if next.RecoveryChecks < 0 || next.RecoveryChecks > 2 || next.Step == nil && next.RecoveryChecks != 0 {
		return false
	}
	if next.ReceiptIssue != "" {
		if next.Step == nil || (next.Step.Kind != "rename" && next.Step.Kind != "move") {
			return false
		}
		switch next.ReceiptIssue {
		case "invalid_envelope", "error_field", "unexpected_field", "data_object", "data_array", "data_string", "data_boolean", "data_other":
		default:
			return false
		}
	}
	switch next.Code {
	case "", "ORGANIZE_MAPPING_REQUIRED", "ORGANIZE_TARGET_CONFLICT", "ORGANIZE_UNCERTAIN", "ORGANIZE_READ_FAILED", "ORGANIZE_RESULT_UNRESOLVED", "TASK_RATE_LIMITED", "TASK_ACCESS_DENIED":
	default:
		return false
	}
	previous := op.Progress
	if next.Cleanup != previous.Cleanup {
		if previous.Cleanup != "" || next.State != "done" || next.Step != nil {
			return false
		}
		switch next.Cleanup {
		case "recycled", "uncertain":
			if previous.Step == nil || previous.Step.Kind != "cleanup" {
				return false
			}
		case "skipped":
			if previous.Step != nil && previous.Step.Kind != "move" {
				return false
			}
		default:
			return false
		}
	}
	if previous.Step != nil && previous.Step.Kind == "cleanup" {
		return next.State == "done" && next.Step == nil && next.Code == "" &&
			(next.Cleanup == "recycled" || next.Cleanup == "uncertain") &&
			reflect.DeepEqual(next.Files, previous.Files) && reflect.DeepEqual(next.Directories, previous.Directories)
	}
	if next.Step != nil && !reflect.DeepEqual(previous.Step, next.Step) {
		return false
	}
	for i, f := range next.Files {
		old := previous.Files[i]
		if f.Renamed == old.Renamed && f.Moved == old.Moved {
			continue
		}
		if previous.Step == nil || previous.Step.Kind == "mkdir" {
			return false
		}
		found := false
		for _, id := range previous.Step.IDs {
			if id == f.ID {
				found = true
			}
		}
		if !found || f.Moved != old.Moved && previous.Step.Kind != "move" {
			return false
		}
	}
	for i, d := range previous.Directories {
		if next.Directories[i] != d {
			return false
		}
	}
	if len(next.Directories) > len(previous.Directories)+1 {
		return false
	}
	if len(next.Directories) > len(previous.Directories) {
		d := next.Directories[len(previous.Directories)]
		if !model.ValidAnimeExternalID(d.CID) || d.CID == op.Request.Target.ParentCID {
			return false
		}
		found := false
		for _, f := range op.Request.Files {
			if strings.HasPrefix(f.TargetPath, d.Path+"/") {
				found = true
			}
		}
		if !found || d.Path == "" || path.Clean(d.Path) != d.Path {
			return false
		}
		for _, old := range previous.Directories {
			if old.Path == d.Path {
				return false
			}
		}
		if previous.Step != nil && (previous.Step.Kind != "mkdir" || previous.Step.Path != d.Path) {
			return false
		}
	}
	if previous.Step != nil && next.Step == nil {
		if previous.Step.Kind == "mkdir" {
			return len(next.Directories) == len(previous.Directories)+1
		}
		for _, id := range previous.Step.IDs {
			found := false
			for _, f := range next.Files {
				if f.ID == id {
					found = f.Renamed
					if previous.Step.Kind == "move" {
						found = f.Moved
					}
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

func validAnimeCleanupStep(job model.AnimeDownloadJob, op model.AnimeOrganizeOperation, step model.AnimeOrganizeStep) bool {
	if step.Kind != "cleanup" || job.DirectoryCID == nil || !model.ValidAnimeExternalID(*job.DirectoryCID) || step.CID != *job.DirectoryCID || step.CID == job.ParentCID || step.CID == op.Request.Target.ParentCID || step.Path != "" || len(step.IDs) != 0 || op.Progress.Cleanup != "" || job.DirectoryName != "anime-"+job.ID || job.Files == nil || len(op.Progress.Files) == 0 {
		return false
	}
	for _, f := range op.Progress.Files {
		if !f.Moved || !f.Renamed {
			return false
		}
	}
	for _, d := range op.Progress.Directories {
		if d.CID == step.CID {
			return false
		}
	}
	resourcePath := strings.TrimRight(job.Snapshot.Target.ParentPath, "/") + "/" + job.DirectoryName
	if strings.HasPrefix(strings.TrimRight(op.Request.Target.ParentPath, "/")+"/", resourcePath+"/") {
		return false
	}
	for _, entry := range job.Files.Entries {
		if entry.ID == step.CID {
			return entry.Kind == "directory" && entry.ParentCID == job.ParentCID && entry.Name == job.DirectoryName
		}
	}
	return false
}

func validAnimeOrganizeStep(req model.AnimeOrganizeRequest, p model.AnimeOrganizeProgress, step model.AnimeOrganizeStep) bool {
	if !model.ValidAnimeExternalID(step.CID) && step.CID != "0" {
		return false
	}
	dirs := map[string]string{"": req.Target.ParentCID}
	for _, d := range p.Directories {
		dirs[d.Path] = d.CID
	}
	if step.Kind == "mkdir" {
		parent := path.Dir(step.Path)
		if parent == "." {
			parent = ""
		}
		if step.Path == "" || dirs[step.Path] != "" || dirs[parent] != step.CID || len(step.IDs) != 0 {
			return false
		}
		for _, f := range req.Files {
			if strings.HasPrefix(f.TargetPath, step.Path+"/") {
				return true
			}
		}
		return false
	}
	if step.Kind != "rename" && step.Kind != "move" || len(step.IDs) == 0 || len(step.IDs) > 50 {
		return false
	}
	seen := make(map[string]bool, len(step.IDs))
	for _, id := range step.IDs {
		if seen[id] {
			return false
		}
		seen[id] = true
		found := false
		for i, f := range req.Files {
			if f.ID != id {
				continue
			}
			found = true
			if p.Files[i].Moved {
				return false
			}
			if step.Kind == "rename" && (f.SourceCID != step.CID || p.Files[i].Renamed) {
				return false
			}
			if step.Kind == "move" && (!p.Files[i].Renamed || dirs[path.Dir(f.TargetPath)] != step.CID) {
				return false
			}
		}
		if !found {
			return false
		}
	}
	return true
}
