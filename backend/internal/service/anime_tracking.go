package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// Deliberately read-only: status polling cannot invoke submit/delete/redo.
type AnimeStatusReader interface {
	ReadAnimeTaskStatus(context.Context, string, string) (model.AnimeOfflineStatus, error)
}

type AnimeStatusRunner struct {
	Admin    *AnimeAdminService
	mu       sync.Mutex
	now      func() time.Time
	interval time.Duration
}

func (r *AnimeStatusRunner) timestamp() int64 {
	if r.now != nil {
		return r.now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func (r *AnimeStatusRunner) valid() bool {
	return r != nil && r.Admin != nil && r.Admin.Store != nil && r.Admin.StatusGateway != nil && r.Admin.DownloadCoordinator != nil
}

// Run owns one sequential, bounded loop for recovery and status reads. Shutdown
// cancels and waits for current I/O and its synchronous receipt cleanup.
func (r *AnimeStatusRunner) Run(ctx context.Context) error {
	if !r.valid() {
		return storage.ErrAnimeInvalid
	}
	interval := r.interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for ctx.Err() == nil {
		if err := r.PollOnce(ctx); err != nil && ctx.Err() == nil {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

// Four UIDs per pass, at most one job per UID; the durable per-job schedule
// coalesces repeated source selections into the same Hash task. No full lists.
func (r *AnimeStatusRunner) PollOnce(ctx context.Context) error {
	if !r.valid() {
		return storage.ErrAnimeInvalid
	}
	if !r.mu.TryLock() {
		return nil
	}
	defer r.mu.Unlock()
	uids := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.Admin.FileGateway != nil && r.Admin.SubmitGateway != nil {
			work, found, err := r.Admin.Store.ReserveAnimeDownloadWork(ctx, r.timestamp(), uids, r.Admin.OrganizationGateway != nil)
			if err != nil {
				return fmt.Errorf("reserve anime download stage: %w", err)
			}
			if found {
				uids = append(uids, work.Job.UID)
				if err := r.execute(ctx, work); err != nil {
					return err
				}
				continue
			}
		}
		poll, found, err := r.Admin.Store.ReserveAnimeStatusPoll(ctx, r.timestamp(), uids)
		if err != nil {
			return fmt.Errorf("reserve anime status read: %w", err)
		}
		if !found {
			return nil
		}
		uids = append(uids, poll.Job.UID)
		if err := r.poll(ctx, poll); err != nil {
			return err
		}
	}
	return nil
}

func (r *AnimeStatusRunner) poll(ctx context.Context, poll storage.AnimeStatusPoll) error {
	readCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var observation *model.AnimeOfflineStatus
	session, readErr := r.Admin.restoreAnimeStatusSession(readCtx, poll.Job.Snapshot.Target)
	if readErr == nil {
		session.work = &storage.AnimeDownloadWork{Job: poll.Job, Token: poll.Token, Until: poll.Until}
		readErr = r.Admin.withDownloadResource(readCtx, session, poll.Job.ID, func(ctx context.Context, job model.AnimeDownloadJob) error {
			// Recheck after the shared UID/resource gate. Another stage may have
			// completed file verification while this request was queued.
			if job.Generation != poll.Job.Generation || job.Revision != poll.Job.Revision || job.WorkflowState != "active" || job.FileState == "verified" {
				return storage.ErrAnimeConflict
			}
			remote, err := r.Admin.StatusGateway.ReadAnimeTaskStatus(ctx, session.cookie, job.Snapshot.InfoHash)
			if err != nil {
				return err
			}
			if err := session.Validate(ctx); err != nil {
				return err
			}
			if !remote.Valid() {
				return p115.ErrAnimeOfflineResponse
			}
			observation = &remote
			return nil
		})
	}
	code, retryAt := "", int64(0)
	if readErr != nil {
		observation = nil
		code, retryAt = animeStatusReadFailure(readErr, r.timestamp())
	}
	// Only persistence outlives request cancellation, with a five-second bound.
	writeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	err := r.Admin.Store.CompleteAnimeStatusPoll(writeCtx, poll, observation, code, retryAt, r.timestamp())
	if errors.Is(err, storage.ErrAnimeAccountChanged) {
		err = r.Admin.Store.CompleteAnimeStatusPoll(writeCtx, poll, nil, "DOWNLOAD_ACCOUNT_CHANGED", 0, r.timestamp())
	}
	if errors.Is(err, storage.ErrAnimeConflict) {
		return nil
	}
	if err == nil && r.Admin.FileGateway != nil && observation != nil && observation.State == "completed" && ctx.Err() == nil {
		session.work = nil // status receipt released its lease before collection
		_, verifyErr := r.Admin.CollectDownloadedFiles(ctx, session, poll.Job.ID)
		if errors.Is(verifyErr, ErrAnimeFilesIncomplete) || errors.Is(verifyErr, storage.ErrAnimeConflict) || errors.Is(verifyErr, storage.ErrAnimeAccountChanged) || errors.Is(verifyErr, ErrCloudTaskBusy) {
			return nil
		}
		return verifyErr
	}
	return err
}

// A read of an already-owned offline task needs fixed credentials, not another
// directory listing. File/parent accessibility is checked by the file stage.
// Avoiding that extra request also keeps a directory endpoint's failure from
// hiding the exact status endpoint's authentication/rate-limit response.
func (s *AnimeAdminService) restoreAnimeStatusSession(ctx context.Context, target model.AnimeDownloadTarget) (*AnimeDownloadSession, error) {
	return s.ResumeDownloadTarget(ctx, target)
}

func animeStatusReadFailure(err error, now int64) (string, int64) {
	if errors.Is(err, storage.ErrAnimeAccountChanged) {
		return "DOWNLOAD_ACCOUNT_CHANGED", 0
	}
	if errors.Is(err, context.Canceled) {
		return "TASK_POLL_CANCELLED", 0
	}
	var response *p115.HTTPError
	if errors.As(err, &response) {
		if response.Status == 401 || response.Status == 403 {
			return "TASK_ACCESS_DENIED", 0
		}
		if response.Status == 429 || response.Status == 503 && response.RetryAfter > 0 {
			return "TASK_RATE_LIMITED", now + max(int64(response.RetryAfter/time.Millisecond), 0)
		}
	}
	return "TASK_READ_FAILED", 0
}
