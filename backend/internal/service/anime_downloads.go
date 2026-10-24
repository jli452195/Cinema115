package service

import (
	"context"
	"errors"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// ClaimDownload verifies a server-observed resource, then atomically associates
// an already authorized pending choice with one local intent. P1 form saves
// cannot call this via HTTP; activation/worker integration comes later in P2.
// The caller must use the exact provider observation kept in its saved preview
// or controlled RSS read. Client-supplied resource hashes are never accepted.
func (s *AnimeAdminService) ClaimDownload(ctx context.Context, session *AnimeDownloadSession, sourceID, releaseID string, generation int64, observation animefeed.SourceRelease) (model.AnimeDownloadClaimResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var empty model.AnimeDownloadClaimResult
	if s == nil || s.Store == nil || session == nil || session.store != s.Store || !model.ValidAnimeID(sourceID) || !model.ValidAnimeID(releaseID) || generation < 1 || generation >= model.AnimeMaxRevision {
		return empty, storage.ErrAnimeInvalid
	}
	if err := session.Validate(ctx); err != nil {
		return empty, err
	}
	d, err := s.Store.GetAnimeReleaseDecision(ctx, sourceID, releaseID)
	if err != nil {
		return empty, err
	}
	if d.Disposition == "dismissed" {
		return empty, storage.ErrAnimeSelectionWithdrawn
	}
	if d.Generation != generation || d.Disposition != "selected" && d.Disposition != "linked" || d.Snapshot.Selection == nil {
		return empty, storage.ErrAnimeConflict
	}
	source, err := s.Store.GetAnimeSource(ctx, sourceID)
	if err != nil {
		return empty, err
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, source.SubscriptionID, false)
	if err != nil {
		return empty, err
	}
	if sub.Status != "enabled" || sub.FirstEnabledAt == nil || source.Status != "enabled" || !source.RunWhenEnabled || source.Revision != d.SourceRevision || d.Snapshot.Selection.SourceGeneration != source.RunGeneration || d.Snapshot.Selection.SubscriptionGeneration != sub.RunGeneration {
		return empty, storage.ErrAnimeConflict
	}
	release, err := s.Store.GetAnimeRelease(ctx, releaseID)
	if err != nil {
		return empty, err
	}
	fp, err := btresource.Fingerprint(observation)
	if err != nil || observation.PostKey != release.PostKey || fp != release.ContentFingerprint || fp != d.ReleaseFingerprint {
		return empty, storage.ErrAnimeIdentity
	}
	verified, cached, err := s.Store.FindAnimeVerifiedRelease(ctx, releaseID, fp)
	if err != nil {
		return empty, err
	}
	if !cached {
		reader := s.Torrents
		if reader == nil {
			reader = &btresource.HTTPReader{}
		}
		verified, err = btresource.Verify(ctx, release.Provider, observation, reader)
	}
	if err != nil {
		// Do not retain arbitrary dependency errors which might echo request data.
		for _, safe := range []error{context.Canceled, context.DeadlineExceeded, btresource.ErrInvalid, btresource.ErrUnsupported, btresource.ErrConflict} {
			if errors.Is(err, safe) {
				return empty, safe
			}
		}
		return empty, btresource.ErrRead
	}
	if err := session.Validate(ctx); err != nil {
		return empty, err
	}
	result, err := s.Store.ClaimAnimeDownload(ctx, sourceID, releaseID, generation, verified, session.Target())
	if err == nil {
		s.wakeDownloads()
		if result.Created && s.Logs != nil {
			s.logAnimeJob(result.Job, "INFO", "已加入下载队列")
		}
	}
	return result, err
}
