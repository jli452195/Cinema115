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
	generation          uint64
}

// Capture only bounded session-event metadata. The body is replayed unchanged
// to Emby; only its successful response is allowed to update an existing lease.
func (h *embyProxyHandler) capturePoolSession(r *http.Request) *http.Request {
	if r.Method != http.MethodPost || r.Body == nil || h.cache == nil || h.cache.Members == nil {
		return r
	}
	path := strings.TrimPrefix(strings.Trim(strings.ToLower(r.URL.Path), "/"), "emby/")
	if path != "sessions/playing" && path != "sessions/playing/progress" && path != "sessions/playing/stopped" {
		return r
	}
	token := embyToken(r)
	user := h.identities.UserID(h.config.URL, token)
	if user == "" {
		return r
	}
	body := r.Body
	data, err := io.ReadAll(io.LimitReader(body, (16<<10)+1))
	r.Body = &embyReplayBody{Reader: io.MultiReader(bytes.NewReader(data), body), closer: body}
	if err != nil || len(data) > 16<<10 {
		return r
	}
	var v struct {
		ItemID        string `json:"ItemId"`
		PlaySessionID string `json:"PlaySessionId"`
	}
	if json.Unmarshal(data, &v) != nil || !embyProxyID.MatchString(v.ItemID) || v.PlaySessionID == "" || len(v.PlaySessionID) > 256 {
		return r
	}
	e := poolSessionEvent{user: user, session: service.DirectLinkKey(token, v.PlaySessionID), item: v.ItemID, stopped: path == "sessions/playing/stopped"}
	e.generation = h.cache.Members.PlaybackEventGeneration(h.config.URL, e.user, e.session, e.item)
	return r.WithContext(context.WithValue(r.Context(), poolSessionEventKey{}, e))
}

func (h *embyProxyHandler) observePoolSession(resp *http.Response) {
	e, ok := resp.Request.Context().Value(poolSessionEventKey{}).(poolSessionEvent)
	if !ok || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	if err := h.cache.Members.PoolSessionEvent(resp.Request.Context(), h.config.URL, e.user, e.session, e.item, e.stopped, e.generation); err != nil {
		h.logs.Add(resp.Request.Context(), "ERROR", "播放会话状态保存失败，将按原租约期限回收")
	}
}
