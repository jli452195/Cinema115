package service

import (
	"context"
	"errors"
	"fmt"

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

func animeLogSource(kind, provider, group string) string {
	for _, p := range AnimeProviders() {
		if p.ID == provider {
			provider = p.Name
			break
		}
	}
	source := kind
	if provider != "" {
		source += " · " + provider
	}
	if group != "" {
		source += " · " + group
	}
	return source
}

func (s *AnimeAdminService) logAnimeRSS(poll storage.AnimeSourcePoll, level, message string) {
	group := ""
	if poll.Source.GroupName != nil {
		group = *poll.Source.GroupName
	}
	s.logAnime(poll.Subscription.ID, animeLogSource("动漫 RSS", poll.Source.Provider, group), level, animeLogTitle(poll.Entry.Title)+"："+message)
}

func (s *AnimeAdminService) logAnimeRSSResult(poll storage.AnimeSourcePoll, page AnimeSourceResult, stats storage.AnimeDiscoveryStats) {
	if s.Logs == nil {
		return
	}
	level, action := "DEBUG", "检查完成"
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
	message := fmt.Sprintf("%s，读取 %d 条", action, page.RawCount)
	if stats.Selected > 0 {
		message += fmt.Sprintf("，选中 %d 条待核验", stats.Selected)
	}
	if stats.Review > 0 {
		message += fmt.Sprintf("，%d 条资源待确认", stats.Review)
	}
	if stats.Selected == 0 && stats.Review == 0 {
		message += "，暂无新资源"
	}
	s.logAnimeRSS(poll, level, message)
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
	if reason == "" {
		reason = "RSS 检查未完成"
	}
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

// Use the frozen naming title and publisher numbering, without metadata I/O.
func (s *AnimeAdminService) logAnimeJob(job model.AnimeDownloadJob, level, message string) {
	if s.Logs == nil {
		return
	}
	title := job.Snapshot.Decision.Title
	if job.Snapshot.Naming != nil && job.Snapshot.Naming.Target.Title != "" {
		title = job.Snapshot.Naming.Target.Title
	}
	if title == "" {
		title = job.Snapshot.Release.Title
	}
	identity := animeLogTitle(title)
	parsed := job.Snapshot.Decision.Parse
	if parsed.Kind == "episode" && parsed.Episode != nil {
		identity += fmt.Sprintf(" · 第%02d集", *parsed.Episode)
	} else if parsed.RawEpisode != "" {
		identity += " · 发布编号 " + parsed.RawEpisode
	}
	if parsed.VersionLabel != "" {
		identity += " " + parsed.VersionLabel
	}
	s.logAnime(job.ID, animeLogSource("动漫任务", job.Snapshot.Source.Provider, job.Snapshot.Source.GroupName), level, identity+"："+message)
}

func (s *AnimeAdminService) logAnimeJobIssue(job model.AnimeDownloadJob, code string) {
	if s.Logs == nil || code == "" || code == "TASK_POLL_CANCELLED" || job.BlockCode != nil && *job.BlockCode == code {
		return
	}
	message := map[string]string{
		"SUBMIT_UNCERTAIN":            "提交结果尚未确认，将核对原任务",
		"SUBMIT_REJECTED":             "115 拒绝提交，请检查网盘配额与账号状态",
		"TASK_MISSING":                "未找到原离线任务，请人工核对",
		"TASK_READ_FAILED":            "读取离线任务失败，将自动重试",
		"TASK_STATUS_UNKNOWN":         "115 离线状态暂未明确，等待后续检查",
		"TASK_RESULT_PENDING":         "115 离线完成，等待结果目录",
		"TASK_RATE_LIMITED":           "115 请求受限，等待冷却后重试",
		"TASK_ACCESS_DENIED":          "115 访问被拒绝，请检查主账号凭据",
		"TASK_TARGET_CONFLICT":        "离线任务或目录不匹配，请人工核对",
		"TASK_RESULT_CHANGED":         "离线结果位置发生变化，请人工核对",
		"TASK_STATUS_CONFLICT":        "离线状态与已有完成记录冲突，请人工核对",
		"DOWNLOAD_ACCOUNT_CHANGED":    "任务使用的主账号已变化，请人工处理原账号中的任务",
		"DOWNLOAD_DIRECTORY_REQUIRED": "下载目录不可用，请重新选择目录",
		"DIRECTORY_CREATE_UNCERTAIN":  "下载目录创建结果尚未确认，将核对原目录",
		"DIRECTORY_READ_FAILED":       "读取下载目录失败，将自动重试",
		"FILES_READ_FAILED":           "读取下载文件失败，请重试读取",
		"FILES_INCOMPLETE":            "下载文件为空或清单不完整，请核对后重试读取",
		"FILES_OUTSIDE_TASK":          "下载结果超出任务目录，请人工核对",
		"ORGANIZE_READ_FAILED":        "读取整理目录失败，将自动重试",
		"ORGANIZE_MAPPING_REQUIRED":   "文件尚未匹配到明确季集，请核对命名设置",
		"ORGANIZE_UNCERTAIN":          "整理结果尚未确认，将核对原步骤",
		"ORGANIZE_RESULT_UNRESOLVED":  "整理结果仍未确认，请人工核对",
		"ORGANIZE_TARGET_CONFLICT":    "部分文件存在目录或来源名称冲突，其余已继续整理，请处理后继续",
	}[code]
	if message == "" {
		message = "任务暂时无法继续，请核对下载与整理状态"
	}
	level := "WARNING"
	if code == "TASK_STATUS_UNKNOWN" || code == "TASK_RESULT_PENDING" {
		level = "DEBUG"
	}
	s.logAnimeJob(job, level, message+"（"+code+"）")
}

// Compare committed states; progress-only reads and repeated issues stay quiet.
func (s *AnimeAdminService) logAnimeJobResult(prior, current model.AnimeDownloadJob) {
	if s.Logs == nil || prior.ID != current.ID || prior.Generation != current.Generation {
		return
	}
	resultPending := current.BlockCode != nil && *current.BlockCode == "TASK_RESULT_PENDING" && current.DownloadState == "completed"
	fileReadIssue := current.DownloadState == "completed" && current.Tracking != nil && current.Tracking.FileReadFailures > 0 && (current.FileState == "checking" || current.FileState == "needs_review")
	if fileReadIssue && prior.FileState != current.FileState && current.FileState == "checking" {
		s.logAnimeJob(current, "DEBUG", "文件清单暂未就绪，稍后自动补读")
	} else if fileReadIssue && prior.FileState != current.FileState && current.Tracking.FileReadFailures >= 3 && current.BlockCode != nil && (*current.BlockCode == "FILES_INCOMPLETE" || *current.BlockCode == "FILES_READ_FAILED") {
		s.logAnimeJob(current, "WARNING", "文件清单连续三次读取未完成，已停止自动补读，可手动重试")
	} else if fileReadIssue && prior.FileState != current.FileState && current.Tracking.FileReadFailures >= 3 && current.BlockCode != nil && *current.BlockCode == "TASK_RATE_LIMITED" {
		s.logAnimeJob(current, "WARNING", "文件清单连续读取受限，已停止自动补读，请等待冷却后手动重试")
	} else if current.BlockCode != nil && !resultPending && (!fileReadIssue || current.FileState == "needs_review") {
		s.logAnimeJobIssue(prior, *current.BlockCode)
	}
	if prior.DownloadState == current.DownloadState {
		return
	}
	message := map[string]string{"accepted": "115 离线任务已确认", "downloading": "115 正在下载", "completed": "115 离线完成"}[current.DownloadState]
	if resultPending {
		message += "，等待结果目录"
	}
	if message != "" {
		s.logAnimeJob(current, "INFO", message)
	} else if current.DownloadState == "failed" && current.BlockCode == nil {
		s.logAnimeJob(current, "ERROR", "115 离线失败，请检查任务")
	}
}

func (s *AnimeAdminService) logAnimeOrganizationResult(job model.AnimeDownloadJob, prior, current model.AnimeOrganizeProgress) {
	if s.Logs == nil {
		return
	}
	if current.State == "done" && prior.State != "done" {
		moved, skipped := 0, 0
		for _, file := range current.Files {
			if file.Moved {
				moved++
			}
			if file.ExistingID != "" {
				skipped++
			}
		}
		level, message := "INFO", fmt.Sprintf("整理完成，已移动 %d 个文件", moved)
		if skipped > 0 {
			message += fmt.Sprintf("，已有同名文件跳过 %d 个", skipped)
		}
		if current.Cleanup == "uncertain" {
			level, message = "WARNING", message+"；临时目录清理结果尚未确认，请人工核对"
		}
		s.logAnimeJob(job, level, message)
		return
	}
	job.BlockCode = &prior.Code
	s.logAnimeJobIssue(job, current.Code)
}
