package service

import (
	"context"
	"encoding/json"
	"strings"
	"strm115/backend/internal/model"
	"time"
)

type ShareRetryResult struct {
	ID       string `json:"id"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// RetryReason 旧版等待任务沿用已经通过安全检查的调度约定，不解析错误文案。
func (s *ShareTasks) RetryReason(task model.ShareTask) string {
	switch task.Status {
	case "failed", "review", "waiting_review", "cancelled", "interrupted":
	default:
		return "任务正在执行、已经成功或执行进度不确定，不能重试"
	}
	var req shareTaskRequest
	if json.Unmarshal(task.Request, &req) != nil || req.Input.URL == "" {
		return "缺少可恢复的请求信息"
	}
	if task.Status == "interrupted" && (req.DirectoryLogID == "" || req.Input.Mode != "direct") {
		return "任务中断，请核对原任务记录"
	}
	if !req.RetrySafe && req.Input.Mode != "direct" && task.Status != "waiting_review" {
		return "无法确认转存进度，请先核对转存记录"
	}
	if req.Settings.AccountID == "" || s.AccountCookie == nil {
		return "原任务未绑定明确的 115 账号"
	}
	cookie := s.AccountCookie(req.Settings.AccountID)
	if strings.TrimSpace(cookie) == "" || cookieHash(cookie) != req.CookieHash {
		return "原任务账号不存在或登录凭据已变化，请检查账号配置"
	}
	return ""
}

func (s *ShareTasks) RetryTasks(ctx context.Context, ids []string) ([]ShareRetryResult, error) {
	results := make([]ShareRetryResult, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		result := ShareRetryResult{ID: id}
		task, err := s.Store.GetShareTask(ctx, id)
		if err != nil {
			result.Reason = "任务不存在或读取失败"
			results = append(results, result)
			continue
		}
		if reason := s.RetryReason(task); reason != "" {
			result.Reason = reason
			results = append(results, result)
			continue
		}
		before := task
		task.Status, task.NextRetryAt, task.UpdatedAt = "retry_queued", nil, time.Now().UTC()
		accepted, err := s.Store.CompareShareTask(ctx, before, task)
		if err != nil {
			return results, err
		}
		result.Accepted = accepted
		if !accepted {
			result.Reason = "任务状态已变化，请刷新后查看"
		}
		results = append(results, result)
	}
	select {
	case s.RetryWake <- struct{}{}:
	default:
	}
	return results, nil
}
