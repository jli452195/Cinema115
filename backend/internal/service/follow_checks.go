package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

type FollowSchedules interface {
	Schedule(context.Context, int, int) (*tmdb.Schedule, error)
}

func (s *FollowService) EvaluateFollow(ctx context.Context, job model.FollowCheck, now time.Time) (model.FollowProgress, error) {
	p := model.FollowProgress{State: "unknown", CheckedAt: now.Unix()}
	matches, err := s.Library.Matches(ctx, []model.EmbyMatch{{Kind: job.Kind, TMDB: strconv.Itoa(job.TMDBID)}}, true)
	if err != nil {
		return p, fmt.Errorf("读取入库索引: %w", err)
	}
	if len(matches) != 1 || (matches[0].State != "present" && matches[0].State != "absent") {
		return p, ErrFollowLibrary
	}
	if matches[0].State == "absent" {
		p.State = "absent"
		return p, nil
	}
	if job.Kind == "movie" {
		p.State = "completed"
		return p, nil
	}
	owned := make(map[[2]int]bool, len(matches[0].Items))
	for _, item := range matches[0].Items {
		if item.Type != "Episode" || item.Season <= 0 || item.Episode <= 0 {
			continue
		}
		end := item.EndEpisode
		if end < item.Episode || end-item.Episode > 100 {
			end = item.Episode
		}
		for n := item.Episode; n <= end; n++ {
			owned[[2]int{item.Season, n}] = true
		}
	}
	// 空剧集条目和特别篇不视为正片入库，也无需请求播出进度。
	if len(owned) == 0 {
		p.State = "absent"
		return p, nil
	}
	schedules, ok := s.TMDB.(FollowSchedules)
	if !ok {
		return p, ErrFollowMetadata
	}
	// 后台只刷新已过期缓存，前端读取仍保持立即返回缓存的行为。
	ctx = context.WithValue(ctx, tmdbPrewarmKey{}, true)
	detail, err := s.TMDB.Details(ctx, "tv", job.TMDBID)
	if err != nil {
		return p, fmt.Errorf("读取追更剧集详情: %w", err)
	}
	if detail == nil || detail.ID != job.TMDBID || detail.CacheStale || len(detail.Seasons) == 0 {
		return p, ErrFollowMetadata
	}
	seen := make(map[[2]int]bool)
	regular, allAired := 0, true
	today := now.UTC().Format("2006-01-02")
	for _, season := range detail.Seasons {
		if season.Number <= 0 {
			continue
		}
		regular++
		// 尚未开播的新季不影响当前追平，也不拉取空的季资料。
		if date, err := time.Parse("2006-01-02", season.Date); err == nil && date.Format("2006-01-02") > today {
			allAired = false
			continue
		}
		schedule, err := schedules.Schedule(ctx, job.TMDBID, season.Number)
		if err != nil {
			return p, fmt.Errorf("读取追更季进度: %w", err)
		}
		if schedule == nil || schedule.Number != season.Number || schedule.CacheStale || season.EpisodeCount < 1 || len(schedule.Episodes) != season.EpisodeCount {
			return p, ErrFollowMetadata
		}
		for _, episode := range schedule.Episodes {
			key := [2]int{season.Number, episode.Number}
			if episode.Number <= 0 || seen[key] {
				return p, ErrFollowMetadata
			}
			seen[key] = true
			date, err := time.Parse("2006-01-02", episode.Date)
			if err != nil {
				return p, ErrFollowMetadata
			}
			if date.Format("2006-01-02") > today {
				allAired = false
				continue
			}
			p.Aired++
			if owned[key] {
				p.Owned++
			}
		}
	}
	if regular == 0 {
		return p, ErrFollowMetadata
	}
	p.State = "tracking"
	if p.Aired == 0 {
		p.State = "waiting"
		return p, nil
	}
	if p.Owned == p.Aired {
		p.State = "caught_up"
		if (detail.Status == "Ended" || detail.Status == "Canceled") && allAired {
			p.State = "completed"
		}
	}
	return p, nil
}

// 唯一后台消费者，复用 SQLite 队列；关闭网页不会终止检查，服务退出会取消网络请求。
func (s *FollowService) RunChecks(ctx context.Context) error {
	if err := s.Store.RecheckFollows(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		jobs, err := s.Store.DueFollowChecks(ctx, time.Now().Unix())
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, job := range jobs {
			work, cancel := context.WithTimeout(ctx, 2*time.Minute)
			p, checkErr := s.EvaluateFollow(work, job, time.Now())
			cancel()
			if ctx.Err() != nil {
				return nil
			}
			delay := time.Hour
			if job.Kind == "movie" {
				delay = 6 * time.Hour
			}
			if checkErr != nil {
				// 不把外部服务错误原文或配置暴露给普通用户。
				p.Error = "入库索引或 TMDB 信息暂未就绪，等待自动复核"
				delay = 5 * time.Minute
			}
			if err := s.Store.SaveFollowCheck(ctx, job, p, time.Now().Add(delay).Unix()); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (s *FollowService) attachProgress(ctx context.Context, items []model.Follow) error {
	progress, err := s.Store.FollowProgress(ctx, items)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Progress = progress[fmt.Sprintf("%s:%d", items[i].Kind, items[i].TMDBID)]
	}
	return nil
}
