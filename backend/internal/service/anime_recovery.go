package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/storage"
)

// execute resumes the saved stage under the existing row lease. A bounded
// execution is shorter than its lease, so no heartbeat or second queue is used.
func (r *AnimeStatusRunner) execute(ctx context.Context, work storage.AnimeDownloadWork) error {
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	session, runErr := r.Admin.ResumeDownloadTarget(runCtx, work.Job.Snapshot.Target)
	if runErr == nil {
		session.work = &work
		if work.Job.FileState == "verified" && work.Job.Snapshot.Organization != nil {
			runErr = r.Admin.OrganizeDownloadedFiles(runCtx, session, work.Job.ID)
		} else if work.Job.DownloadState == "completed" {
			// Completion already persisted: read the task directory directly,
			// without asking the offline service for the same state again.
			_, runErr = r.Admin.CollectDownloadedFiles(runCtx, session, work.Job.ID)
		} else {
			_, runErr = r.Admin.EnsureDownload(runCtx, session, work.Job.ID)
		}
	}
	code := ""
	switch {
	case runErr == nil, errors.Is(runErr, ErrAnimeDirectoryPending), errors.Is(runErr, ErrAnimeFilesIncomplete), errors.Is(runErr, ErrCloudTaskBusy), errors.Is(runErr, storage.ErrAnimeConflict):
	case errors.Is(runErr, storage.ErrAnimeAccountChanged):
		code = "DOWNLOAD_ACCOUNT_CHANGED"
	case errors.Is(runErr, storage.ErrAnimeDirectoryRequired), errors.Is(runErr, ErrAnimeSubmitDirectory):
		code = "DOWNLOAD_DIRECTORY_REQUIRED"
	case errors.Is(runErr, storage.ErrAnimeWriteUnauthorized):
		code = "DOWNLOAD_PAUSED"
	case errors.Is(runErr, context.Canceled):
		code = "TASK_POLL_CANCELLED"
	case errors.Is(runErr, context.DeadlineExceeded), errors.Is(runErr, ErrAnimeOfflineRead), errors.Is(runErr, ErrAnimeDirectoryRead):
		code = "TASK_READ_FAILED"
		if work.Job.FileState == "verified" {
			code = "ORGANIZE_READ_FAILED"
		}
	default:
		// Database/programming failures must reach the owner of this loop.
		// The durable lease expires; any dispatched write remains read-only.
		return fmt.Errorf("execute anime download stage: %w", runErr)
	}
	writeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	err := r.Admin.Store.CompleteAnimeDownloadWork(writeCtx, work, code, r.timestamp())
	if errors.Is(err, storage.ErrAnimeConflict) {
		// File collection or a concurrent cancellation already retired it.
		return nil
	}
	return err
}
