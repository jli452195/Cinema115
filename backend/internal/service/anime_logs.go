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

// Reuse the shared log scrubber even for upstream titles and publisher names.
func animeLogText(value string, limit int) string {
	value = monitorLogDetail(value, "")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit]) + "…"
	}
	return value
}

func (s *AnimeAdminService) logAnime(id, source, level, message string) {
	if s.Logs != nil {
		s.Logs.Append("订阅", id, animeLogText(source, 160), level, animeLogText(message, 1000))
	}
}

func animeLogTitle(title string) string {
	if title == "" {
		return "当前订阅"
	}
	return "《" + animeLogText(title, 120) + "》"
}

func (s *AnimeAdminService) logAnimeRSS(poll storage.AnimeSourcePoll, level, message string) {
	provider := poll.Source.Provider
	for _, p := range AnimeProviders() {
		if p.ID == provider {
			provider = p.Name
			break
		}
	}
	source := "动漫 RSS · " + provider
	if poll.Source.GroupName != nil {
		source += " · " + *poll.Source.GroupName
	}
	s.logAnime(poll.Subscription.ID, source, level, animeLogTitle(poll.Entry.Title)+"："+message)
}

func (s *AnimeAdminService) logAnimeRSSResult(poll storage.AnimeSourcePoll, page AnimeSourceResult, stats storage.AnimeDiscoveryStats, requests int, elapsed time.Duration) {
	if s.Logs == nil {
		return
	}
	level, action := "DEBUG", "轮询完成"
	if poll.Initialize {
		level, action = "INFO", "首次检查完成"
	} else if poll.Source.Health == "error" || poll.Source.Health == "cooldown" {
		level, action = "INFO", "RSS 读取已恢复"
	}
	if stats.Selected > 0 {
		level = "INFO"
	}
	if stats.Review > 0 {
		level = "WARNING"
	}
	message := fmt.Sprintf("%s，读取 %d 条，选中 %d 条待下载，待确认 %d 条", action, page.RawCount, stats.Selected, stats.Review)
	if level != "DEBUG" {
		s.logAnimeRSS(poll, level, message)
		message = action
	}
	message += fmt.Sprintf("；上游请求 %d 次，耗时 %.2f 秒", requests, elapsed.Seconds())
	if page.Cached {
		message += "（命中缓存）"
	}
	s.logAnimeRSS(poll, "DEBUG", message)
}

func (s *AnimeAdminService) logAnimeRSSFailure(poll storage.AnimeSourcePoll, code string, retryAt int64, unavailable bool) {
	if s.Logs == nil {
		return
	}
	reason := map[string]string{
		"SOURCE_READ_FAILED": "读取失败", "SOURCE_READ_BUDGET": "达到本次读取预算", "SOURCE_RATE_LIMITED": "站点限流",
		"SOURCE_UNAVAILABLE": "来源访问受限", "SOURCE_BINDING_CHANGED": "字幕组关联已变化", "DOWNLOAD_TARGET_CHANGED": "下载账号或目录配置已变化",
		"SOURCE_RESPONSE_INVALID": "来源响应校验未通过", "POLL_CANCELLED": "本次轮询已取消",
	}[code]
	level, next := "WARNING", "将按退避计划重试"
	if unavailable {
		next = "已停止自动轮询，请检查来源或下载设置后保存"
	} else if retryAt > 0 {
		next = "等待站点冷却后重试"
	}
	if poll.Source.ErrorCode != nil && *poll.Source.ErrorCode == code || code == "POLL_CANCELLED" {
		level = "DEBUG"
	}
	if code == "POLL_CANCELLED" {
		next = "保留原订阅，等待后续调度"
	}
	s.logAnimeRSS(poll, level, reason+"；"+next+"（"+code+"）")
}

// These lookups are only for explicit user operations, never a polling loop.
// Capture the title before removal so the log survives deletion of local data.
func (s *AnimeAdminService) animeSubscriptionLogTitle(ctx context.Context, sub model.AnimeSubscription) string {
	entry, err := s.Store.GetAnimeEntry(ctx, sub.EntryID)
	if err != nil {
		return "当前订阅"
	}
	return animeLogTitle(entry.Title)
}

func animeLogOperationError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "操作已取消"
	case errors.Is(err, context.DeadlineExceeded):
		return "请求超时，请核对当前状态"
	case errors.Is(err, storage.ErrAnimeConflict):
		return "状态已变化，请刷新后重试"
	case errors.Is(err, storage.ErrAnimeDeleteBusy):
		return "任务正在执行，请稍后移除"
	case errors.Is(err, storage.ErrAnimeAccountChanged), errors.Is(err, storage.ErrAnimeDirectoryRequired), errors.Is(err, storage.ErrAnimeConfigurationRequired):
		return "请检查下载账号及目录设置"
	case errors.Is(err, storage.ErrAnimeTransition):
		return "当前状态不允许此操作"
	}
	return "操作未完成，请检查配置或服务状态"
}

// Observe existing I/O only. One DEBUG summary replaces paired start/end rows;
// ending a measurement does not claim that a business operation succeeded.
func (s *AnimeAdminService) observeAnimeStage(ctx context.Context, jobID, stage string) (context.Context, func()) {
	if s.Logs == nil {
		return ctx, func() {}
	}
	started := time.Now()
	var mu sync.Mutex
	var once sync.Once
	var total p115.RequestTiming
	label := map[string]string{"subscription": "保存订阅", "submit": "提交离线任务", "status": "查询离线进度", "files": "读取下载文件", "organize": "整理文件"}[stage]
	ctx = p115.WithRequestTiming(ctx, func(t p115.RequestTiming) {
		mu.Lock()
		defer mu.Unlock()
		total.Requests += t.Requests
		total.Writes += t.Writes
		total.Retries += t.Retries
		total.Wait += t.Wait
		total.HTTP += t.HTTP
	})
	return ctx, func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			message := fmt.Sprintf("%s执行结束；耗时 %.2f 秒", label, time.Since(started).Seconds())
			if total.Requests > 0 {
				message += fmt.Sprintf("；115 请求 %d 次（读取 %d，写入 %d，重试 %d）；限速等待 %.2f 秒，HTTP %.2f 秒", total.Requests, total.Requests-total.Writes, total.Writes, total.Retries, total.Wait.Seconds(), total.HTTP.Seconds())
			}
			s.logAnime(jobID, "动漫："+label, "DEBUG", message)
		})
	}
}
