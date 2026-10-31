package service

import (
	"context"
	"errors"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func (s *AnimeAdminService) RedownloadAnime(ctx context.Context, actor, key, id string, in storage.AnimeRedownloadInput) (out storage.AnimeRedownloadReceipt, resultErr error) {
	if _, ok := s.Offline.(CloudURLAdder); !ok || s.DownloadCoordinator == nil {
		return out, storage.ErrAnimeCapability
	}
	k, err := animeRequestKey(actor, "POST /api/admin/anime/subscriptions/"+id+"/redownload", key, in)
	if err != nil {
		return out, err
	}
	defer s.replayAnimeConcurrentWrite(ctx, k, &out, &resultErr)
	defer func() {
		if resultErr == nil {
			s.wakeDownloads()
		}
	}()
	return s.Store.RedownloadAnime(ctx, id, in, k)
}

func (s *AnimeAdminService) submitAnimeRedownload(ctx context.Context, session *AnimeDownloadSession, op model.AnimeSubmitOperation) error {
	client, ok := s.Offline.(CloudURLAdder)
	if !ok {
		return ErrAnimeOfflineUnavailable
	}
	dispatched := false
	_, submitErr := submitCloudResource(ctx, client, session.cookie, p115.CloudResource{Protocol: "magnet", Hash: op.Request.InfoHash}, "magnet:?xt=urn:btih:"+op.Request.InfoHash, op.Request.DirectoryCID, func() error {
		if err := session.Validate(ctx); err != nil {
			return err
		}
		if dispatched {
			return nil
		}
		if err := s.Store.DispatchAnimeSubmit(ctx, op.ID, session.leaseToken()); err != nil {
			return err
		}
		dispatched = true
		return nil
	})
	if !dispatched {
		if errors.Is(submitErr, ErrCloudTaskRunning) || errors.Is(submitErr, ErrCloudTaskUnclear) {
			return s.recordAnimeSubmit(ctx, session, op.ID, "failed", "TASK_TARGET_CONFLICT", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "conflict"})
		}
		if errors.Is(submitErr, storage.ErrAnimeConflict) || errors.Is(submitErr, storage.ErrAnimeWriteUnauthorized) || errors.Is(submitErr, storage.ErrAnimeAccountChanged) {
			return submitErr
		}
		return animeSubmitReadError(ctx, submitErr)
	}
	// The durable dispatched state covers both delete and submit. A lost reply
	// in either phase only permits the usual exact read, never another write.
	observation := model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "unreadable"}
	state, code := "unknown", "SUBMIT_UNCERTAIN"
	if submitErr == nil {
		state, code, observation.Kind = "confirmed", "", "accepted"
		observation.TargetCID = op.Request.DirectoryCID
	}
	return s.recordAnimeSubmit(ctx, session, op.ID, state, code, observation)
}
