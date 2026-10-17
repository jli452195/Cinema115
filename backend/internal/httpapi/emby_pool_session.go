package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"strm115/backend/internal/service"
)

type poolSessionEventKey struct{}
type poolSessionEvent struct {
	user, session, item string
	stopped             bool
	captured            service.PlaybackEvent
	missingSession      bool
}

// Capture only bounded session-event metadata. The body is replayed unchanged
// to Emby; only its successful response is allowed to update an existing lease.
func (h *embyProxyHandler) capturePoolSession(r *http.Request) *http.Request {
	if r.Method != http.MethodPost || h.cache == nil || h.cache.Members == nil {
		return r
	}
	path := strings.TrimPrefix(strings.Trim(strings.ToLower(r.URL.Path), "/"), "emby/")
	if path != "sessions/playing" && path != "sessions/playing/progress" && path != "sessions/playing/stopped" && path != "sessions/playing/ping" {
		return r
	}
	token := embyToken(r)
	user := h.identities.UserID(h.config.URL, token)
	if user == "" {
		return r
	}
	if path == "sessions/playing/ping" {
		session := proxyQuery(r.URL.Query(), "PlaySessionId")
		if session == "" || len(session) > 256 {
			return r
		}
		e := poolSessionEvent{captured: h.cache.Members.CapturePlaybackPing(h.config.URL, user, service.DirectLinkKey(token, session))}
		return r.WithContext(context.WithValue(r.Context(), poolSessionEventKey{}, e))
	}
	if r.Body == nil {
		return r
	}
	body := r.Body
	data, err := io.ReadAll(io.LimitReader(body, (16<<10)+1))
	r.Body = &embyReplayBody{Reader: io.MultiReader(bytes.NewReader(data), body), closer: body}
	if err != nil || len(data) > 16<<10 {
		return r
	}
	var v struct {
		ItemID        string          `json:"ItemId"`
		PlaySessionID string          `json:"PlaySessionId"`
		IsPaused      json.RawMessage `json:"IsPaused"`
	}
	if json.Unmarshal(data, &v) != nil || !embyProxyID.MatchString(v.ItemID) || len(v.PlaySessionID) > 256 {
		return r
	}
	e := poolSessionEvent{user: user, session: service.DirectLinkKey(token, v.PlaySessionID), item: v.ItemID, stopped: path == "sessions/playing/stopped"}
	e.missingSession = v.PlaySessionID == ""
	var paused *bool
	if json.Unmarshal(v.IsPaused, &paused) != nil {
		paused = nil // Optional display metadata must not affect event lifecycle.
	}
	e.captured = h.cache.Members.CapturePlaybackEvent(h.config.URL, e.user, e.session, service.DirectLinkKey("playback-client", token), e.item, e.missingSession).WithPlaybackPause(paused)
	return r.WithContext(context.WithValue(r.Context(), poolSessionEventKey{}, e))
}

func (h *embyProxyHandler) observePoolSession(resp *http.Response) {
	e, ok := resp.Request.Context().Value(poolSessionEventKey{}).(poolSessionEvent)
	if !ok || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	if e.stopped && !e.captured.Matched() {
		h.logs.Add(resp.Request.Context(), "WARNING", "Emby 已接受停止，但未匹配本地播放租约；请检查播放会话字段、登录凭据是否一致或租约是否已结束")
	}
	if err := h.cache.Members.ApplyPlaybackEvent(resp.Request.Context(), e.captured, e.stopped); err != nil {
		h.logs.Add(resp.Request.Context(), "ERROR", "播放会话状态保存失败，将按原租约期限回收")
	} else if e.stopped && e.captured.Matched() {
		h.logs.Add(resp.Request.Context(), "INFO", "Emby 已接受停止：已处理对应代次的播放会话和池租约；池已签发记录最多保留两分钟残余，旧代次或重复事件忽略")
	}
}
