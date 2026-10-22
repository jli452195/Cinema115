package service

import (
	"context"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type AnimeControlInput struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

// ControlSubscription is local only. Resume reuses confirmed choices; it is
// separate from first enable, which still needs an explicit activation preview.
func (s *AnimeAdminService) ControlSubscription(ctx context.Context, actor, key, id, action string, in AnimeControlInput) (out model.AnimeControlReceipt, resultErr error) {
	if !model.ValidAnimeID(id) {
		return out, storage.ErrAnimeInvalid
	}
	switch action {
	case "pause", "resume", "archive", "remove", "unarchive":
	default:
		return out, storage.ErrAnimeInvalid
	}
	k, err := animeRequestKey(actor, "POST /api/admin/anime/subscriptions/"+id+"/"+action, key, in)
	if err != nil {
		return out, err
	}
	if s.Logs != nil {
		prior, readErr := s.Store.GetAnimeSubscription(ctx, id, false)
		if readErr == nil && prior.Revision == in.ExpectedRevision {
			title := s.animeSubscriptionLogTitle(ctx, prior)
			defer func() {
				actionName := map[string]string{"pause": "暂停订阅", "resume": "恢复订阅", "archive": "归档订阅", "unarchive": "取消归档", "remove": "移除订阅"}[action]
				if resultErr != nil {
					s.logAnime(id, "动漫订阅", "WARNING", title+"："+actionName+"未完成；"+animeLogOperationError(resultErr))
				} else if out.Revision > prior.Revision {
					s.logAnime(id, "动漫订阅", "INFO", title+"："+actionName+"已完成")
				}
			}()
		}
	}
	defer s.replayAnimeConcurrentWrite(ctx, k, &out, &resultErr)
	defer func() {
		if resultErr == nil {
			s.wakeDownloads()
		}
	}()
	return s.Store.ControlAnimeSubscription(ctx, id, action, in.ExpectedRevision, &k)
}

func (s *AnimeAdminService) SubscriptionJobs(ctx context.Context, id string, page, size int) (model.AnimeDownloadJobPage, error) {
	return s.Store.ListAnimeDownloadJobs(ctx, id, page, size)
}

func (s *AnimeAdminService) DownloadJob(ctx context.Context, id string) (model.AnimeDownloadJob, error) {
	return s.Store.GetAnimeDownloadJob(ctx, id)
}
