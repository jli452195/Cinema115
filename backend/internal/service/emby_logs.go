package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type embyLogKey struct{}

// Background failures carry their operation metadata to the worker boundary.
// The worker logs them once, without losing the task ID or credential snapshot.
type embyOperationError struct {
	error
	logger embyOperationLog
}

func (e *embyOperationError) Unwrap() error { return e.error }

func embyFailure(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if logger, ok := ctx.Value(embyLogKey{}).(embyOperationLog); ok {
		return &embyOperationError{err, logger}
	}
	return err
}

type embyOperationLog struct {
	store        *GenerationLogs
	task, source string
	secrets      []string
	started      time.Time
}

func (s *EmbyService) newLogTask() string {
	return s.Logs.NewTaskID("EMBY")
}

func (s *EmbyService) logContext(ctx context.Context, c EmbyConfig, task, source string, extra ...string) context.Context {
	secrets := append([]string{c.APIKey, c.WebhookSecret}, extra...)
	return context.WithValue(ctx, embyLogKey{}, embyOperationLog{s.Logs, task, source, secrets, time.Now()})
}

func embyElapsed(ctx context.Context) float64 {
	if logger, ok := ctx.Value(embyLogKey{}).(embyOperationLog); ok {
		return time.Since(logger.started).Seconds()
	}
	return 0
}

func (s *EmbyService) startOperation(ctx context.Context, action string, extra ...string) (context.Context, func(error)) {
	ctx = s.logContext(ctx, s.snapshot(), s.newLogTask(), "网页配置", extra...)
	started := time.Now()
	logEmby(ctx, "INFO", "开始"+action)
	return ctx, func(err error) {
		level, message := "INFO", action+"完成"
		if err != nil {
			level, message = "ERROR", action+"失败："+embyLogError(err)
			if errors.Is(err, context.Canceled) {
				level = "WARNING"
			}
		}
		logEmby(ctx, level, fmt.Sprintf("%s，耗时 %.2f 秒", message, time.Since(started).Seconds()))
	}
}

var embyLogURL = regexp.MustCompile(`(?i)https?://[^\s]+`)
var embyLogCredential = regexp.MustCompile(`(?i)(?:api[_-]?key|x-emby-token|authorization|cookie|token|secret|password)\s*[=:：].*`)

// Never log request bodies or headers. Scrub even names/IDs supplied by Emby,
// including encoded forms of credentials, before adding them to the shared ring.
func logEmby(ctx context.Context, level, message string) {
	logger, ok := ctx.Value(embyLogKey{}).(embyOperationLog)
	if !ok || logger.store == nil {
		return
	}
	for _, secret := range logger.secrets {
		if secret == "" {
			continue
		}
		encoded, _ := json.Marshal(secret)
		for _, value := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), string(encoded[1 : len(encoded)-1])} {
			message = strings.ReplaceAll(message, value, "[密钥已隐藏]")
		}
	}
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	message = embyLogURL.ReplaceAllString(message, "[地址已隐藏]")
	message = embyLogCredential.ReplaceAllString(message, "[凭据已隐藏]")
	logger.store.Append("Emby", logger.task, logger.source, level, message)
}

func embyLogError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "操作已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return "本地配置读写失败"
	}
	// Emby client errors contain fixed diagnostics, never upstream response bodies.
	// Dynamic values are scrubbed by logEmby at the final boundary.
	return err.Error()
}

// Called under s.mu. Only transitions are logged, not every successful probe.
func (s *EmbyService) logConnection(ctx context.Context, c EmbyConfig, err error) {
	ctx = s.logContext(ctx, c, "EMBY-CONNECTION", "连接检查")
	if err != nil && s.connectionError == "" {
		logEmby(ctx, "WARNING", "Emby 连接异常："+embyLogError(err))
	} else if err == nil && s.connectionError != "" {
		logEmby(ctx, "INFO", "Emby 连接已恢复")
	}
}
