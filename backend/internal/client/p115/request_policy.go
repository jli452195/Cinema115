package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// HTTPError 只保留固定接口信息，不能包含查询参数、请求体或 Cookie。
type HTTPError struct {
	Status           int
	Method, Endpoint string
	RetryAfter       time.Duration
}

func (e *HTTPError) Error() string {
	hint := ""
	switch e.Status {
	case 405:
		hint = "：上游拒绝此请求，请核对接口方法或访问限制，不能仅凭 405 确定原因"
	case 429:
		hint = "：请求过于频繁，请暂停生成并稍后重试"
	}
	return fmt.Sprintf("115 分享接口返回 HTTP %d（%s %s）%s", e.Status, e.Method, e.Endpoint, hint)
}

type requestProgressKey struct{}

func WithRequestProgress(ctx context.Context, report func(string)) context.Context {
	return context.WithValue(ctx, requestProgressKey{}, report)
}

func waitRequest(ctx context.Context, until time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := time.Until(until)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfter(value string) time.Duration {
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		if delay := time.Until(date); delay > 0 {
			return delay
		}
	}
	return 0
}

// metadataRequest 在客户端内串行限速；仅重试只读 GET，云端写操作不会自动重放。
func (c *ShareClient) metadataRequest(ctx context.Context, method string, request func() ([]byte, error)) ([]byte, error) {
	select {
	case c.requestGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.requestGate }()
	for attempt := 0; ; attempt++ {
		if err := waitRequest(ctx, c.nextRequest); err != nil {
			return nil, err
		}
		data, err := request()
		c.nextRequest = time.Now().Add(c.RequestInterval)
		var upstream *HTTPError
		if !errors.As(err, &upstream) || (upstream.Status != 405 && upstream.Status != 429) {
			return data, err
		}
		// 即使本次不再重试，也为后续任务保留冷却时间。
		delay := 10 * time.Second
		if attempt < len(c.ReadRetryDelays) {
			delay = c.ReadRetryDelays[attempt]
		}
		if upstream.RetryAfter > delay {
			delay = upstream.RetryAfter
		}
		if delay < c.RequestInterval {
			delay = c.RequestInterval
		}
		c.nextRequest = time.Now().Add(delay)
		if method != http.MethodGet || attempt >= len(c.ReadRetryDelays) {
			return nil, err
		}
		if report, ok := ctx.Value(requestProgressKey{}).(func(string)); ok {
			report(fmt.Sprintf("115 目录接口返回 HTTP %d，等待 %.0f 秒后重试只读请求（%d/%d）", upstream.Status, delay.Seconds(), attempt+1, len(c.ReadRetryDelays)))
		}
	}
}
