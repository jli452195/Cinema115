package service

import (
	"context"
	"errors"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

var (
	ErrAnimeOfflineUnavailable = errors.New("anime offline gateway unavailable")
	ErrAnimeOfflineRead        = errors.New("anime offline task read failed")
)

// LookupDownload reads one persisted job's exact resource under the same gate
// as manual downloads. It neither deletes/resubmits completed tasks nor marks
// a missing task safe to submit. State normalization and durable submit intent
// belong to later P2 steps; this read does not update any download state.
func (s *AnimeAdminService) LookupDownload(ctx context.Context, session *AnimeDownloadSession, jobID string) (p115.CloudTask, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var task p115.CloudTask
	var found bool
	err := s.withDownloadResource(ctx, session, jobID, func(ctx context.Context, job model.AnimeDownloadJob) error {
		if s.Offline == nil {
			return ErrAnimeOfflineUnavailable
		}
		var err error
		task, found, err = s.Offline.LookupTask(ctx, session.cookie, p115.CloudResource{Protocol: "magnet", Hash: job.Snapshot.InfoHash})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			for _, safe := range []error{context.Canceled, context.DeadlineExceeded} {
				if errors.Is(err, safe) {
					return safe
				}
			}
			return ErrAnimeOfflineRead
		}
		if found && !(p115.CloudResource{Protocol: "magnet", Hash: job.Snapshot.InfoHash}).MatchesTaskHash(task.InfoHash) {
			return ErrCloudTaskUnclear
		}
		return session.Validate(ctx)
	})
	if err != nil {
		return p115.CloudTask{}, false, err
	}
	return task, found, nil
}

// Later submission stages must use this same boundary and additionally recheck
// persistent write authorization inside it. The callback receives a fresh job
// after waiting; network I/O never holds a SQLite transaction.
func (s *AnimeAdminService) withDownloadResource(ctx context.Context, session *AnimeDownloadSession, jobID string, run func(context.Context, model.AnimeDownloadJob) error) error {
	if s == nil || s.Store == nil || session == nil || session.store != s.Store || !model.ValidAnimeID(jobID) {
		return storage.ErrAnimeInvalid
	}
	if s.DownloadCoordinator == nil {
		return ErrAnimeOfflineUnavailable
	}
	if err := session.Validate(ctx); err != nil {
		return err
	}
	job, err := s.Store.GetAnimeDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if !animeDownloadJobMatches(job, session.Target()) {
		return storage.ErrAnimeResourceTarget
	}
	release, err := s.DownloadCoordinator.Acquire(ctx, job.UID, job.Snapshot.Protocol, job.Snapshot.InfoHash)
	if err != nil {
		return err
	}
	defer release()
	if err := session.Validate(ctx); err != nil {
		return err
	}
	current, err := s.Store.GetAnimeDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if !animeDownloadJobMatches(current, session.Target()) || current.Snapshot.InfoHash != job.Snapshot.InfoHash || current.Generation != job.Generation {
		return storage.ErrAnimeConflict
	}
	return run(ctx, current)
}

func animeDownloadJobMatches(job model.AnimeDownloadJob, target model.AnimeDownloadTarget) bool {
	return target.Validate() == nil && job.Snapshot.SchemaVersion == model.AnimeSchemaVersion && job.Snapshot.Target == target && job.AccountID == target.AccountID && job.SettingsRevision == target.SettingsRevision && job.ParentCID == target.ParentCID && job.Snapshot.Protocol == "bt_v1"
}
