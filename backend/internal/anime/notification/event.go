package notification

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
)

var beijing = time.FixedZone("北京时间", 8*60*60)

func ValidEvent(kind string) bool {
	return kind == "downloaded" || kind == "organized" || kind == "failed"
}

func episodeLabel(parsed releaseparse.Result) string {
	if parsed.Kind == "episode" && parsed.Episode != nil {
		return fmt.Sprintf("第 %02d 集", *parsed.Episode)
	}
	if parsed.RawEpisode != "" {
		return parsed.RawEpisode
	}
	if parsed.Kind == "batch" {
		return "合集"
	}
	if parsed.Kind == "special" {
		return "特别篇"
	}
	return "待确认"
}

func Values(event model.AnimeNotificationEvent) map[string]string {
	snapshot := event.Snapshot
	title := event.Title
	if title == "" && snapshot.Naming != nil {
		title = snapshot.Naming.Target.Title
	}
	if title == "" {
		title = snapshot.Decision.Title
	}
	if title == "" {
		title = "动漫订阅"
	}
	group := snapshot.Source.GroupName
	if group == "" {
		group = "未标注"
	}
	provider := map[string]string{"mikan": "蜜柑计划", "anibt": "AniBT", "garden": "Anime Garden"}[snapshot.Source.Provider]
	if provider == "" {
		provider = "其他来源"
	}
	icon, label, reason := "⚠️", "任务异常", issueReason(event.BlockCode)
	if event.Kind == "failed" {
		switch event.BlockCode {
		case "TASK_MISSING", "TASK_READ_FAILED", "TASK_RATE_LIMITED", "TASK_ACCESS_DENIED", "DIRECTORY_READ_FAILED", "ORGANIZE_READ_FAILED":
			label = "持续异常"
		case "DIRECTORY_CREATE_UNCERTAIN", "SUBMIT_UNCERTAIN", "ORGANIZE_UNCERTAIN", "ORGANIZE_RESULT_UNRESOLVED":
			label = "需要复核"
		}
	}
	path := snapshot.Target.ParentPath
	switch event.Kind {
	case "downloaded":
		icon, label, reason = "📥", "下载完成", "115 离线下载已完成，文件校验与整理进度请查看订阅任务。"
	case "organized":
		icon, label, reason = "✅", "整理完成", "文件已完成整理，请以 Emby 实际扫描结果为准。"
	}
	if event.Kind == "organized" || strings.HasPrefix(event.BlockCode, "ORGANIZE_") {
		if snapshot.Organization != nil {
			path = snapshot.Organization.Target.ParentPath
		}
	}
	if path == "" {
		path = "请查看任务详情"
	}
	resource := snapshot.Release.Title
	if resource == "" {
		resource = "资源名称未记录"
	}
	parsed := snapshot.Decision.Parse
	if parsed.Kind == "" {
		parsed = snapshot.Release.Parse
	}
	when := "时间未记录"
	if event.CreatedAt > 0 {
		when = time.UnixMilli(event.CreatedAt).In(beijing).Format("2006-01-02 15:04:05 -07:00")
	}
	return map[string]string{
		"icon": icon, "event": label, "title": title, "episode": episodeLabel(parsed),
		"group": group, "provider": provider, "resource": resource, "path": path,
		"reason": reason, "time": when, "job_id": event.JobID,
	}
}

func issueReason(code string) string {
	message := map[string]string{
		"SUBMIT_UNCERTAIN":            "提交结果尚未确认，请人工核对原任务后继续。",
		"SUBMIT_REJECTED":             "115 拒绝提交，请检查网盘配额与账号状态。",
		"TASK_MISSING":                "连续检查仍未找到原离线任务，请人工核对。",
		"TASK_READ_FAILED":            "连续多次读取离线任务失败，系统将继续重试。",
		"TASK_STATUS_UNKNOWN":         "115 离线状态暂未明确，等待后续检查。",
		"TASK_RESULT_PENDING":         "115 离线已完成，正在等待结果目录。",
		"TASK_RATE_LIMITED":           "115 请求持续受限，系统将在冷却后继续重试。",
		"TASK_ACCESS_DENIED":          "连续检查均被 115 拒绝访问，请检查主账号凭据。",
		"TASK_TARGET_CONFLICT":        "离线任务或目录不匹配，请人工核对。",
		"TASK_RESULT_CHANGED":         "离线结果位置发生变化，请人工核对。",
		"TASK_STATUS_CONFLICT":        "离线状态与已有完成记录冲突，请人工核对。",
		"DOWNLOAD_ACCOUNT_CHANGED":    "主账号已变化，请人工处理原账号中的任务。",
		"DOWNLOAD_DIRECTORY_REQUIRED": "下载目录不可用，请重新选择目录。",
		"DIRECTORY_CREATE_UNCERTAIN":  "下载目录创建结果尚未确认，请人工核对原目录后继续。",
		"DIRECTORY_READ_FAILED":       "连续多次读取下载目录失败，系统将继续重试。",
		"FILES_READ_FAILED":           "自动重试后仍未能读取下载文件，请人工核对并重试读取。",
		"FILES_INCOMPLETE":            "下载文件为空或清单不完整，请核对后重试读取。",
		"FILES_OUTSIDE_TASK":          "下载结果超出任务目录，请人工核对。",
		"ORGANIZE_READ_FAILED":        "连续多次读取整理目录失败，系统将继续重试。",
		"ORGANIZE_MAPPING_REQUIRED":   "文件尚未匹配到明确季集，请核对命名设置。",
		"ORGANIZE_UNCERTAIN":          "自动核对后整理结果仍未确认，请人工核对原步骤。",
		"ORGANIZE_RESULT_UNRESOLVED":  "整理结果仍未确认，请人工核对。",
		"ORGANIZE_TARGET_CONFLICT":    "部分文件存在目录或名称冲突，请处理后继续整理。",
	}[code]
	if message == "" {
		message = "任务未能完成，请查看下载与整理任务详情。"
	}
	if code != "" {
		message += "（" + code + "）"
	}
	return message
}

func Sample(kind string) model.AnimeNotificationEvent {
	episode := 7
	return model.AnimeNotificationEvent{
		ID: 1, JobID: "anime-demo-" + strconv.Itoa(episode), Generation: 1, Kind: kind,
		Title: "葬送的芙莉莲", BlockCode: sampleCode(kind),
		CoverURL:  "/api/bangumi/image/l-13-c5/400602_ZI8Y9.jpg",
		CreatedAt: time.Date(2026, 11, 5, 20, 30, 0, 0, beijing).UnixMilli(),
		Snapshot: model.AnimeDownloadSnapshot{
			Source:       model.AnimeDownloadSource{Provider: "mikan", GroupName: "桜都字幕组"},
			Target:       model.AnimeDownloadTarget{ParentPath: "/动漫下载"},
			Release:      model.AnimeRelease{Title: "[桜都字幕组] 葬送的芙莉莲 - 07 [1080p][简繁内封]"},
			Decision:     model.AnimeDecisionSnapshot{Parse: releaseparse.Result{Kind: "episode", Episode: &episode}},
			Organization: &model.AnimeOrganizationSnapshot{Target: model.AnimeDownloadTarget{ParentPath: "/媒体库/动漫"}},
		},
	}
}

func sampleCode(kind string) string {
	if kind == "failed" {
		return "ORGANIZE_TARGET_CONFLICT"
	}
	return ""
}
