package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// ClaimDownload verifies a server-observed resource, then atomically associates
// an already authorized pending choice with one local intent. Form saves only
// update rules; discovery owns resource selection and verification.
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
	manifest := verified.Files()
	if d.Snapshot.Parse.Kind == "batch" && len(manifest) == 0 && release.Provider == "garden" {
		if s.Bindings == nil || s.Bindings.Garden == nil {
			return empty, btresource.ErrUnsupported
		}
		files, err := s.Bindings.Garden.Files(ctx, DownloadResourceInput{Source: "garden", Provider: observation.Origin, ProviderID: observation.OriginID, Magnet: observation.MagnetHint})
		if err != nil {
			if errors.Is(err, garden.ErrInvalid) || errors.Is(err, ErrOrganizeInput) {
				return empty, btresource.ErrInvalid
			}
			return empty, btresource.ErrRead
		}
		if files.Reason != "" || len(files.Files) == 0 || files.Hash != verified.Hash() {
			return empty, btresource.ErrUnsupported
		}
		manifest = files.manifest
	}
	result, err := s.Store.ClaimAnimeDownload(ctx, sourceID, releaseID, generation, verified, session.Target(), manifest)
	if err == nil {
		if !result.Skipped {
			s.wakeDownloads()
		} else if s.Logs != nil {
			message := "此集已由合集补齐或正在处理，已跳过"
			switch result.SkipReason {
			case "BATCH_NO_MATCHING_FILES":
				message = "合集内没有通过规则且集号明确的视频，已跳过"
			case "BATCH_NO_MISSING_FILES":
				message = "合集匹配集数已有任务覆盖，无需重复下载"
			case "BELOW_SOURCE_MINIMUM":
				message = "合集匹配集数均低于补番起点，已跳过"
			}
			s.logAnime(sub.ID, animeLogSource("动漫 RSS", source.Provider, *source.GroupName), "DEBUG", animeLogTitle(release.Title)+"："+message)
		}
		if result.Created && s.Logs != nil {
			message := "已加入下载队列"
			if len(result.Job.Snapshot.BatchFiles) > 0 {
				message += fmt.Sprintf("，合集补缺 %d 集", len(result.Job.Snapshot.BatchFiles))
			}
			s.logAnimeJob(result.Job, "INFO", message)
		}
	}
	return result, err
}
