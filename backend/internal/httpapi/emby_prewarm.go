package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"strm115/backend/internal/service"
)

// No speculative queue: one owned task, bounded duplicate suppression, and
// foreground requests prevent new admission. An admitted write is not preempted.
type proxyPrewarmer struct {
	mu         sync.Mutex
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	closed     bool
	foreground int
	key        string
	done       chan struct{}
	recent     map[string]time.Time
}

type prewarmSnapshot struct {
	prepareOnly bool
	source      service.P115Account
	bindings    service.PlaybackCacheSnapshot
	media       map[string]json.RawMessage
}
type prewarmContextKey struct{}
type playbackSelectedSourceKey struct{}

func (h *embyProxyHandler) Close() {
	p := &h.prewarm
	p.mu.Lock()
	p.closed = true
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Unlock()
	p.wg.Wait()
	h.http.CloseIdleConnections()
}

func (h *embyProxyHandler) foregroundPlayback() func() {
	p := &h.prewarm
	p.mu.Lock()
	p.foreground++
	p.mu.Unlock()
	return func() { p.mu.Lock(); p.foreground--; p.mu.Unlock() }
}

func (h *embyProxyHandler) prewarmKey(token, item, sourceID string, source service.P115Account, selected *service.PlaybackCacheSelection) string {
	return service.DirectLinkKey(h.config.URL, token, item, sourceID, source.ID, source.Cookie, strconv.FormatUint(source.Revision, 10), selected.DirectLinkScope())
}

func (h *embyProxyHandler) waitPrewarm(ctx context.Context, key string) error {
	p := &h.prewarm
	p.mu.Lock()
	var done chan struct{}
	if p.key == key {
		done = p.done
	}
	p.mu.Unlock()
	if done == nil {
		return nil
	}
	h.logs.Add(ctx, "INFO", "同一媒体源正在预热，等待现有任务；播放器 UA 不同时将重新取链")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (h *embyProxyHandler) schedulePrewarm(r *http.Request, item string, candidates []map[string]json.RawMessage) {
	if (!h.config.Prewarm && !h.config.PrepareFiles) || len(candidates) == 0 {
		return
	}
	sourceID := proxyQuery(r.URL.Query(), "MediaSourceId")
	if sourceID == "" {
		sourceID, _ = r.Context().Value(playbackSelectedSourceKey{}).(string)
	}
	if sourceID == "" && len(candidates) == 1 {
		sourceID = jsonString(candidates[0]["Id"])
	}
	var media map[string]json.RawMessage
	for _, candidate := range candidates {
		if jsonString(candidate["Id"]) == sourceID {
			// Copy only the validated source fields needed by the STRM resolver.
			media = make(map[string]json.RawMessage, 2)
			for _, field := range []string{"Id", "Path"} {
				media[field] = append(json.RawMessage(nil), candidate[field]...)
			}
			break
		}
	}
	if media == nil {
		return
	}
	token := embyToken(r)
	userID := h.identities.UserID(h.config.URL, token)
	if !embyProxyID.MatchString(userID) {
		return
	}
	snapshot := &prewarmSnapshot{media: media, prepareOnly: !h.config.Prewarm}
	snapshot.source, _ = h.accounts.Get(h.accounts.PrimaryID())
	if h.cache != nil {
		snapshot.bindings = h.cache.Snapshot()
	}
	var selected *service.PlaybackCacheSelection
	if h.cache != nil {
		var err error
		selected, err = snapshot.bindings.Select(h.config.URL, userID)
		if err != nil {
			return
		}
	}
	key := h.prewarmKey(token, item, sourceID, snapshot.source, selected)
	if snapshot.prepareOnly && (selected == nil || selected.UsesSourceAccount(snapshot.source.ID)) {
		return
	}
	recentKey := service.DirectLinkKey(key, r.UserAgent())
	if snapshot.prepareOnly {
		recentKey = key
	}
	p := &h.prewarm
	p.mu.Lock()
	if p.closed || p.foreground > 0 || p.done != nil || time.Now().Before(p.recent[recentKey]) {
		p.mu.Unlock()
		return
	}
	if p.recent == nil {
		p.recent = make(map[string]time.Time)
	}
	for k, until := range p.recent {
		if time.Now().After(until) {
			delete(p.recent, k)
		}
	}
	if len(p.recent) >= 512 {
		p.mu.Unlock()
		return
	}
	p.key, p.done = key, make(chan struct{})
	done := p.done
	p.wg.Add(1)
	// Copy only the fields used by our authenticated resolver, never the body or
	// the original response/request log group. Credentials stay in task memory.
	q := url.Values{"ItemId": {item}, "MediaSourceId": {sourceID}, "api_key": {token}}
	request, _ := http.NewRequest(http.MethodGet, "/cinema115/play?"+q.Encode(), nil)
	request.Header.Set("User-Agent", r.UserAgent())
	request.Host = r.Host
	parentTrace := service.ProxyTraceID(r.Context())
	parentStarted, _ := r.Context().Value(proxyRequestStartedKey{}).(time.Time)
	scheduled := time.Now()
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		ctx, cancel := context.WithTimeout(p.ctx, 90*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, prewarmContextKey{}, snapshot)
		ctx = service.PlaybackPrewarmContext(ctx)
		if snapshot.prepareOnly {
			ctx = service.PreparePlaybackFileContext(ctx)
		}
		ctx, finish := h.logs.BeginRequest(h.logs.Trace(ctx), h.config.Debug)
		defer finish()
		label := "播放预热"
		if snapshot.prepareOnly {
			label = "提前准备秒传文件"
		}
		h.logs.Add(ctx, "INFO", label+"开始，条目="+proxyLogID(item)+"，媒体源="+proxyLogID(sourceID))
		if h.config.Debug {
			h.logs.Add(ctx, "INFO", fmt.Sprintf("预热关联请求=%s｜调度等待=%d ms", parentTrace, time.Since(scheduled).Milliseconds()))
			if !parentStarted.IsZero() {
				h.logs.Add(ctx, "INFO", fmt.Sprintf("距触发 PlaybackInfo 请求进入反代=%d ms", time.Since(parentStarted).Milliseconds()))
			}
		}
		writer := &prewarmResponse{header: make(http.Header)}
		h.redirect(writer, request.WithContext(ctx), item, false)
		level, message := "INFO", "播放预热完成；同 UA 可复用有效直链，不同 UA 复用已准备文件后取链"
		if snapshot.prepareOnly {
			message = "秒传文件准备完成；正式播放时按播放器 UA 获取直链"
		}
		if snapshot.prepareOnly && writer.status == http.StatusOK {
			message = "当前来源跳过文件准备；正式播放时按原流程处理"
		}
		if writer.status != http.StatusFound && writer.status != http.StatusNoContent && !(snapshot.prepareOnly && writer.status == http.StatusOK) {
			level, message = "WARNING", fmt.Sprintf("播放预热未完成，HTTP %d；正式播放仍按正常流程处理", writer.status)
		}
		h.logs.Add(ctx, level, message)
		if h.config.Debug && !parentStarted.IsZero() {
			h.logs.Add(ctx, "INFO", fmt.Sprintf("从触发 PlaybackInfo 请求进入反代至预热结束=%d ms", time.Since(parentStarted).Milliseconds()))
		}
		p.mu.Lock()
		p.recent[recentKey] = time.Now().Add(30 * time.Second)
		p.key, p.done = "", nil
		close(done)
		p.mu.Unlock()
	}()
}

// Discard all response data; this task never sends a redirect to a player.
type prewarmResponse struct {
	header http.Header
	status int
}

func (w *prewarmResponse) Header() http.Header { return w.header }
func (w *prewarmResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *prewarmResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return len(b), nil
}
