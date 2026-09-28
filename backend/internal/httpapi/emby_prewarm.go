package httpapi

import (
	"context"
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
	source   service.P115Account
	bindings service.PlaybackCacheSnapshot
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

func (h *embyProxyHandler) schedulePrewarm(r *http.Request, item string, candidates []string) {
	if !h.config.Prewarm || len(candidates) == 0 {
		return
	}
	sourceID := proxyQuery(r.URL.Query(), "MediaSourceId")
	if sourceID == "" {
		sourceID, _ = r.Context().Value(playbackSelectedSourceKey{}).(string)
	}
	if sourceID == "" && len(candidates) == 1 {
		sourceID = candidates[0]
	}
	found := false
	for _, id := range candidates {
		if id == sourceID {
			found = true
		}
	}
	if !found {
		return
	}
	token := embyToken(r)
	userID := h.identities.UserID(h.config.URL, token)
	if !embyProxyID.MatchString(userID) {
		return
	}
	snapshot := &prewarmSnapshot{}
	snapshot.source, _ = h.accounts.Primary("")
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
	recentKey := service.DirectLinkKey(key, r.UserAgent())
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
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		ctx, cancel := context.WithTimeout(p.ctx, 90*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, prewarmContextKey{}, snapshot)
		ctx = service.PlaybackPrewarmContext(ctx)
		ctx, finish := h.logs.BeginRequest(h.logs.Trace(ctx), true)
		defer finish()
		h.logs.Add(ctx, "INFO", "播放预热开始，条目="+proxyLogID(item)+"，媒体源="+proxyLogID(sourceID))
		writer := &prewarmResponse{header: make(http.Header)}
		h.redirect(writer, request.WithContext(ctx), item, false)
		level, message := "INFO", "播放预热完成；同 UA 可复用有效直链，不同 UA 复用已准备文件后取链"
		if writer.status != http.StatusFound {
			level, message = "WARNING", fmt.Sprintf("播放预热未完成，HTTP %d；正式播放仍按正常流程处理", writer.status)
		}
		h.logs.Add(ctx, level, message)
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
