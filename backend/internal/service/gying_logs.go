package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"strm115/backend/internal/client/gying"
)

type gyingLogKey struct{}
type gyingOperationLog struct {
	store        *GenerationLogs
	task, source string
}

// StartGyingOperation records one completion at the outermost operation boundary.
// Nested service calls inherit the same task ID, including login retries.
func StartGyingOperation(ctx context.Context, logs *GenerationLogs, source, action string) (context.Context, func(error)) {
	if _, ok := ctx.Value(gyingLogKey{}).(gyingOperationLog); ok || logs == nil {
		return ctx, func(error) {}
	}
	logs.mu.Lock()
	logs.taskSequence++
	task := fmt.Sprintf("GYING-%06d", logs.taskSequence)
	logs.mu.Unlock()
	logger := gyingOperationLog{store: logs, task: task, source: source}
	ctx = context.WithValue(ctx, gyingLogKey{}, logger)
	ctx = gying.WithProgress(ctx, func(level, message string) { logGying(ctx, level, message) })
	started := time.Now()
	logGying(ctx, "INFO", "开始"+action)
	return ctx, func(err error) {
		if err != nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		level, message := "INFO", action+"完成"
		if err != nil {
			level, message = "ERROR", action+"失败："+gyingLogError(err)
			if errors.Is(err, context.Canceled) {
				level, message = "WARNING", action+"已取消"
			} else if errors.Is(err, gying.ErrNotConfigured) || errors.Is(err, gying.ErrInput) {
				level = "WARNING"
			}
		}
		logGying(ctx, level, fmt.Sprintf("%s，耗时 %.2f 秒", message, time.Since(started).Seconds()))
	}
}

func logGying(ctx context.Context, level, message string) {
	if logger, ok := ctx.Value(gyingLogKey{}).(gyingOperationLog); ok {
		logger.store.Append("观影", logger.task, logger.source, level, message)
	}
}

var gyingLogURL = regexp.MustCompile(`(?i)(?:https?://|magnet:\?)[^\s]+`)
var gyingLogSecret = regexp.MustCompile(`(?i)(?:cookie|authorization|password|token|secret|密码)\s*[:=：].*`)
var gyingLogHTTPStatus = regexp.MustCompile(`HTTP ([1-5][0-9]{2})\b`)

func gyingLogQuery(value string, doc gyingDocument) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	for _, secret := range append([]string{doc.Password, doc.Username}, gyingCookieValues(doc.Cookies)...) {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[已隐藏]")
		}
	}
	value = gyingLogURL.ReplaceAllString(value, "[链接已隐藏]")
	value = gyingLogSecret.ReplaceAllString(value, "[敏感内容已隐藏]")
	runes := []rune(value)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return value
}

func gyingCookieValues(cookies []gying.SessionCookie) []string {
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		values = append(values, cookie.Value)
	}
	return values
}

// Classify errors instead of logging err.Error(): upstream errors can embed
// login responses, cookie values, URL parameters or local secret-store paths.
func gyingLogError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "操作已取消"
	case errors.Is(err, context.DeadlineExceeded):
		return "请求超时"
	case errors.Is(err, gying.ErrNotConfigured):
		return "尚未配置观影登录信息"
	case errors.Is(err, gying.ErrInput):
		return "参数无效，请检查配置或搜索条件"
	case errors.Is(err, gying.ErrLogin):
		return "观影站拒绝登录，请检查账号凭据"
	case errors.Is(err, gying.ErrForbidden):
		return "登录状态失效或访问受限"
	case errors.Is(err, gying.ErrChallenge):
		return "安全验证未通过或验证重试耗尽"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "网络请求超时"
		}
		return "网络连接失败"
	}
	var syntax *json.SyntaxError
	var field *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &field) {
		return "站点响应格式无法解析"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return "本地配置读写失败"
	}
	if status := gyingLogHTTPStatus.FindStringSubmatch(err.Error()); len(status) == 2 {
		return "上游接口返回 HTTP " + status[1]
	}
	return "操作失败（上游详细内容已隐藏）"
}
