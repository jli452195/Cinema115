package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

var (
	errED2KNoMaterial         = errors.New("没有可核验的首次文件资料")
	errED2KCloudPending       = errors.New("未找到可确认的原云任务，不会自动重复提交；请检查 115 云下载")
	errED2KUploadPending      = errors.New("秒传结果尚未确认，继续按 SHA1 核对；不会自动重复写入")
	errED2KCloudFailed        = errors.New("115 云下载报告任务失败或停止")
	errED2KResultMissing      = errors.New("离线完成，但缓存文件资料尚未就绪")
	errED2KSubmitAfterRemoval = errors.New("原云任务记录已移除，新离线请求尚未发送，请检查后手动恢复")
)

type ed2kStepError struct {
	phase string
	cause error
}

func (e *ed2kStepError) Error() string { return e.cause.Error() }
func (e *ed2kStepError) Unwrap() error { return e.cause }

func ed2kStepFailure(phase string, err error) error {
	return &ed2kStepError{phase: phase, cause: err}
}

func (s *ED2KSTRMService) scheduleCacheError(v *model.ED2KSTRMResource, err error) {
	v.Message = ed2kPublicError(err)
	if v.Status == "blocked" {
		v.NextRunAt = time.Time{}
		return
	}
	if v.Status != "uncertain" && v.Status != "downloading" {
		v.Status = "recovering"
	}
	now := time.Now().UTC()
	retryAt := p115.CloudRequestRetryAt(err)
	var deferred *p115.CloudRequestDeferredError
	if errors.As(err, &deferred) {
		v.NextRunAt = now.Add(max(3*time.Second, retryAt.Sub(now)))
		return
	}
	v.Attempts++
	if v.Attempts >= 8 {
		v.Status, v.NextRunAt = "failed", time.Time{}
		return
	}
	delay := time.Duration(5*(1<<min(v.Attempts, 5))) * time.Second
	if v.Status == "uncertain" {
		delay = max(delay, time.Minute)
	}
	v.NextRunAt = now.Add(max(delay, retryAt.Sub(now)))
}

func (s *ED2KSTRMService) saveCacheProblem(ctx context.Context, v model.ED2KSTRMResource, err error) error {
	if saveErr := s.save(ctx, v); saveErr != nil {
		return fmt.Errorf("保存 ED2K 缓存失败状态: %w", saveErr)
	}
	s.logCacheProblem(v, err)
	return nil
}

func (s *ED2KSTRMService) logCacheProblem(v model.ED2KSTRMResource, err error) {
	if s.Logs == nil {
		return
	}
	level, phase := "WARNING", "缓存处理"
	if v.Status == "failed" {
		level = "ERROR"
	}
	var step *ed2kStepError
	if errors.As(err, &step) {
		phase = step.phase
	}
	var deferred *p115.CloudRequestDeferredError
	if errors.As(err, &deferred) {
		// 同账号排队期间可能影响多个资源，持久记录实际触发冷却的请求。
		level = "DEBUG"
	}
	detail := ed2kPublicError(err)
	var upstream *p115.HTTPError
	var rejected *p115.ED2KTaskRejection
	if errors.As(err, &upstream) {
		detail = fmt.Sprintf("HTTP %d", upstream.Status)
		if endpoint := ed2kLogEndpoint(upstream); endpoint != "" {
			detail = endpoint + " " + detail
		}
		if upstream.RetryAfter > 0 {
			detail += "，上游 Retry-After " + upstream.RetryAfter.Round(time.Second).String()
		}
	} else if errors.As(err, &rejected) {
		detail = rejected.Error()
	} else if errors.Is(err, p115.ErrAnimeOfflineResponse) {
		detail = "115 云任务响应缺少可信的状态或文件资料"
	} else if errors.Is(err, context.DeadlineExceeded) && deferred == nil {
		detail = "请求或处理超时"
	}
	status := "已停止自动重试"
	if v.Status == "blocked" {
		status = "任务已暂停"
	} else if v.Status != "failed" && !v.NextRunAt.IsZero() {
		status = "下次核对 " + v.NextRunAt.UTC().Format(time.RFC3339)
	}
	s.Logs.Append("STRM", "ED2K "+v.Hash[:min(8, len(v.Hash))], "ED2K STRM", level,
		fmt.Sprintf("%s：%s；累计失败 %d 次；%s", phase, detail, v.Attempts, status))
}

func ed2kLogEndpoint(upstream *p115.HTTPError) string {
	if upstream.Method != http.MethodGet && upstream.Method != http.MethodPost {
		return ""
	}
	// 底层返回固定接口。日志只接受 115 主机、固定路由字符，丢弃查询和片段。
	endpoint := upstream.Endpoint
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "115.com" && !strings.HasSuffix(host, ".115.com") {
		return ""
	}
	if len(u.Path) > 96 || strings.IndexFunc(u.Path, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/-_.", r))
	}) >= 0 {
		return ""
	}
	return upstream.Method + " " + host + u.Path
}
