package service

import (
	"context"
	"errors"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

// The automatic gateway has no delete/redo method. Its absence observation is
// stricter than the legacy manual task parser and never comes from a list page.
type AnimeOfflineGateway interface {
	InspectAnimeTask(context.Context, string, string) (p115.AnimeTaskObservation, error)
	SubmitAnimeTask(context.Context, string, string, string) error
}

var ErrAnimeSubmitDirectory = errors.New("anime submit directory identity is unverified")

// EnsureDownload is a single, request-scoped execution step, not a scheduler.
// A durable dispatched/unknown operation is reconciled without another submit.
// P2-07 must prepare and bind the resource subdirectory before the first call.
func (s *AnimeAdminService) EnsureDownload(ctx context.Context, session *AnimeDownloadSession, jobID string) (model.AnimeEnsureDownloadResult, error) {
	ctx, cancel := context.WithTimeout(ctx, CloudDownloadTimeout)
	defer cancel()
	var out model.AnimeEnsureDownloadResult
	if s != nil && s.FileGateway != nil {
		job, err := s.EnsureDownloadDirectory(ctx, session, jobID)
		if err != nil {
			return model.AnimeEnsureDownloadResult{Job: job}, err
		}
	}
	err := s.withDownloadResource(ctx, session, jobID, func(ctx context.Context, job model.AnimeDownloadJob) error {
		if s.SubmitGateway == nil {
			return ErrAnimeOfflineUnavailable
		}
		op, err := s.Store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, session.Target(), session.leaseToken())
		if err != nil {
			return err
		}
		out.Job, out.Operation = job, op
		if op.State == "confirmed" || op.State == "failed" {
			return nil
		}
		if op.State != "prepared" {
			return s.reconcileAnimeSubmit(ctx, session, op)
		}
		if job.Snapshot.Redownload {
			return s.submitAnimeRedownload(ctx, session, op)
		}
		observation, err := s.SubmitGateway.InspectAnimeTask(ctx, session.cookie, op.Request.InfoHash)
		if err != nil {
			return animeSubmitReadError(ctx, err)
		}
		if err := session.Validate(ctx); err != nil {
			return err
		}
		if observation.Present {
			// A task predating dispatch is not ours, even in the same directory.
			return s.recordAnimeSubmit(ctx, session, op.ID, "failed", "TASK_TARGET_CONFLICT", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "conflict"})
		}
		if observation.InfoHash != "" || observation.TargetCID != "" || observation.ResultID != "" {
			return ErrAnimeOfflineRead
		}
		created := job.DirectoryOperation
		if created == nil || created.State != "confirmed" || created.CID != op.Request.DirectoryCID || created.Generation != job.Generation {
			directory, err := session.directories.ReadDirectory(ctx, op.Request.DirectoryCID, session.cookie)
			if err != nil {
				return animeDownloadDirectoryError(ctx, err)
			}
			if directory.CID != op.Request.DirectoryCID || directory.ParentCID != job.ParentCID || directory.Name != job.DirectoryName {
				return ErrAnimeSubmitDirectory
			}
		}
		if err := session.Validate(ctx); err != nil {
			return err
		}
		// This transaction rechecks persisted primary account, current selection,
		// source/subscription generations and the immutable resource/target.
		if err := s.Store.DispatchAnimeSubmit(ctx, op.ID, session.leaseToken()); err != nil {
			return err
		}
		// No retries inside the gateway. Permission was committed immediately
		// above; a later pause may stop the next operation, not undo this one.
		submitErr := s.SubmitGateway.SubmitAnimeTask(ctx, session.cookie, op.Request.InfoHash, op.Request.DirectoryCID)
		// Persist acceptance without a verification GET. The runner can then
		// check completion once and continue directly into file organization.
		// Uncertain writes use scheduled read-only recovery, never resubmission.
		receipt := model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "unreadable"}
		state, code := "unknown", "SUBMIT_UNCERTAIN"
		checkCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if err := session.Validate(checkCtx); err == nil {
			if submitErr == nil {
				state, code, receipt.Kind = "confirmed", "", "accepted"
				receipt.TargetCID = op.Request.DirectoryCID
			} else if errors.Is(submitErr, p115.ErrAnimeSubmitRejected) {
				state, code, receipt.Kind = "failed", "SUBMIT_REJECTED", "rejected"
			}
		}
		return s.recordAnimeSubmit(ctx, session, op.ID, state, code, receipt)
	})
	// Receipt persistence is synchronous and bounded even after a disconnect.
	// If it fails, dispatched remains durable and the next call only reconciles.
	if out.Operation.ID != "" {
		readCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		job, jobErr := s.Store.GetAnimeDownloadJob(readCtx, jobID)
		op, opErr := s.Store.GetAnimeSubmitOperation(readCtx, jobID)
		if jobErr == nil && opErr == nil {
			s.logAnimeJobResult(out.Job, job)
			out.Job, out.Operation = job, op
		}
		if err == nil && jobErr != nil {
			err = jobErr
		}
		if err == nil && opErr != nil {
			err = opErr
		}
	}
	return out, err
}

func animeSubmitReadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ErrAnimeOfflineRead
}

func (s *AnimeAdminService) recordAnimeSubmit(ctx context.Context, session *AnimeDownloadSession, id, state, code string, observation model.AnimeSubmitObservation) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.Store.RecordAnimeSubmitOutcome(writeCtx, id, state, code, observation, session.leaseToken())
}

func (s *AnimeAdminService) reconcileAnimeSubmit(ctx context.Context, session *AnimeDownloadSession, op model.AnimeSubmitOperation) error {
	observation := model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "unreadable"}
	state, code := "unknown", "TASK_READ_FAILED"
	if err := session.Validate(ctx); err != nil {
		return s.recordAnimeSubmit(ctx, session, op.ID, state, "SUBMIT_UNCERTAIN", observation)
	}
	remote, err := s.SubmitGateway.InspectAnimeTask(ctx, session.cookie, op.Request.InfoHash)
	if err != nil {
		return s.recordAnimeSubmit(ctx, session, op.ID, state, code, observation)
	}
	if err := session.Validate(ctx); err != nil {
		return s.recordAnimeSubmit(ctx, session, op.ID, state, "SUBMIT_UNCERTAIN", observation)
	}
	if !remote.Present {
		if remote.InfoHash != "" || remote.TargetCID != "" || remote.ResultID != "" {
			return s.recordAnimeSubmit(ctx, session, op.ID, state, code, observation)
		}
		observation.Kind, code = "missing", "TASK_MISSING"
		return s.recordAnimeSubmit(ctx, session, op.ID, state, code, observation)
	}
	if remote.InfoHash != op.Request.InfoHash || remote.TargetCID != "" && remote.TargetCID != op.Request.DirectoryCID {
		observation.Kind = "conflict"
		return s.recordAnimeSubmit(ctx, session, op.ID, state, "TASK_TARGET_CONFLICT", observation)
	}
	matched := remote.TargetCID == op.Request.DirectoryCID
	if !matched && model.ValidAnimeExternalID(remote.ResultID) {
		// Exact lookup can omit wp_path_id. Only an exact directory breadcrumb
		// can supply the missing relation here; file result resolution is P2-07.
		dir, readErr := session.directories.ReadDirectory(ctx, remote.ResultID, session.cookie)
		if readErr == nil && dir.CID == remote.ResultID {
			matched = dir.ParentCID == op.Request.DirectoryCID
			if remote.ResultID == op.Request.DirectoryCID {
				matched = dir.ParentCID == op.Request.Target.ParentCID && dir.Name == "anime-"+op.JobID
			}
		}
	}
	if !matched {
		return s.recordAnimeSubmit(ctx, session, op.ID, state, "TASK_TARGET_CONFLICT", observation)
	}
	if err := session.Validate(ctx); err != nil {
		return s.recordAnimeSubmit(ctx, session, op.ID, state, "SUBMIT_UNCERTAIN", observation)
	}
	observation.Kind, observation.TargetCID = "matched", op.Request.DirectoryCID
	if model.ValidAnimeExternalID(remote.ResultID) {
		observation.ResultID = remote.ResultID
	}
	return s.recordAnimeSubmit(ctx, session, op.ID, "confirmed", "", observation)
}
