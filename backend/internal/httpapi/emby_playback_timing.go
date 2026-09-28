package httpapi

import (
	"context"
	"fmt"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/service"
)

// All labels are fixed locally; no URL, token, headers or raw errors are logged.
type playbackTimeline struct {
	h                     *embyProxyHandler
	ctx                   context.Context
	started, stageStarted time.Time
	stage                 string
	stages                []string
}

func (h *embyProxyHandler) newPlaybackTimeline(ctx context.Context) *playbackTimeline {
	if !service.ProxyDebugEnabled(ctx) {
		return nil
	}
	now := time.Now()
	return &playbackTimeline{h: h, ctx: ctx, started: now, stageStarted: now, stage: "入口及配置快照", stages: make([]string, 0, 10)}
}

func (t *playbackTimeline) next(stage string) {
	if t == nil {
		return
	}
	t.stages = append(t.stages, fmt.Sprintf("%s=%d ms", t.stage, time.Since(t.stageStarted).Milliseconds()))
	t.stage, t.stageStarted = stage, time.Now()
}

func (t *playbackTimeline) finish() {
	if t == nil {
		return
	}
	t.next("结束")
	t.h.logs.Add(t.ctx, "INFO", fmt.Sprintf("播放入口分段耗时：%s｜合计=%d ms", strings.Join(t.stages, "｜"), time.Since(t.started).Milliseconds()))
}

func (h *embyProxyHandler) traceEmbyRequest(ctx context.Context, stage string) (context.Context, func()) {
	if !service.ProxyDebugEnabled(ctx) {
		return ctx, func() {}
	}
	started := time.Now()
	var mu sync.Mutex
	var connection, firstByte time.Duration
	var connected, reused, received bool
	h.logs.Add(ctx, "INFO", "Emby 请求开始："+stage)
	trace := &httptrace.ClientTrace{
		GotConn: func(v httptrace.GotConnInfo) {
			mu.Lock()
			connection, connected, reused = time.Since(started), true, v.Reused
			mu.Unlock()
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			firstByte, received = time.Since(started), true
			mu.Unlock()
		},
	}
	return httptrace.WithClientTrace(ctx, trace), func() {
		mu.Lock()
		conn, first, got, reuse, response := connection, firstByte, connected, reused, received
		mu.Unlock()
		state := "未取得连接"
		if got {
			state = "新建"
			if reuse {
				state = "复用"
			}
		}
		firstText := "未收到"
		if response {
			firstText = fmt.Sprintf("%d ms", first.Milliseconds())
		}
		h.logs.Add(ctx, "INFO", fmt.Sprintf("Emby 请求计时：%s｜合计=%d ms｜取得连接=%d ms｜首字节=%s｜连接=%s", stage, time.Since(started).Milliseconds(), conn.Milliseconds(), firstText, state))
	}
}
