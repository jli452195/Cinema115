package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type proxyLookupDiagnosticKey struct{}
type proxyLookupDiagnostic struct{ headers, description string }

// Only known diagnostic values are exposed. Unknown headers and credentials
// retain their names but not values; URL-bearing headers lose all query values.
func proxyDiagnosticHeaders(headers http.Header, host string) string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys)+1)
	total := 0
	if host != "" {
		lines = append(lines, "Host: "+host)
	}
	for _, key := range keys {
		lower := strings.ToLower(key)
		value := "[已隐藏]"
		switch lower {
		case "user-agent", "range", "accept", "accept-encoding", "accept-language", "connection", "content-type", "content-length", "content-range", "cache-control", "pragma", "if-modified-since", "if-none-match", "if-range", "etag", "last-modified", "date", "server", "accept-ranges", "transfer-encoding", "upgrade", "x-forwarded-for", "x-forwarded-proto", "x-real-ip":
			value = strings.Join(headers.Values(key), ", ")
		case "origin", "referer":
			if u, err := url.Parse(headers.Get(key)); err == nil && u.Host != "" {
				value = u.Scheme + "://" + u.Host + "/[路径和参数已隐藏]"
			}
		}
		if len(value) > 2048 {
			value = value[:2048] + "…[已截断]"
		}
		line := key + ": " + value
		if total+len(line) > 16384 {
			lines = append(lines, "…[请求头过长，其余已省略]")
			break
		}
		total += len(line)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func proxyLookupHeaders(ua string) string {
	if ua == "" {
		ua = p115.PlaybackUserAgent
	}
	return proxyDiagnosticHeaders(http.Header{"User-Agent": {ua}, "Cookie": {"[已隐藏]"}}, "")
}

// Log fixed templates, never arbitrary path segments, query values or raw headers.
func proxyDebugRoute(r *http.Request) (string, string) {
	if r.Header.Get("X-Cinema115-Emby-Proxy") != "" {
		return "循环请求", "拒绝反代循环"
	}
	if blockedEmbyTranscode(r) {
		return "/Videos|Audio/{Id}/转码接口", "拒绝转码"
	}
	if r.URL.Path == "/cinema115/play" || r.URL.Path == "/emby/cinema115/play" {
		return "/cinema115/play", "302 解析入口"
	}
	if staticStreamItem(r) != "" {
		return "/Videos/{Id}/stream.*", "静态流解析入口（不匹配 STRM 时透传）"
	}
	if downloadItem(r.URL.Path) != "" {
		return "/Items/{Id}/Download", "下载权限校验与直链解析（不支持的源交由 Emby）"
	}
	if originalStreamItem(r) != "" {
		return "/Videos/{Id}/original.*", "原文件入口（配置第三方适配时检查接管，否则透传）"
	}
	if playbackItem(r.URL.Path) != "" {
		return "/Items/{Id}/PlaybackInfo", "播放信息透传并检查改写"
	}
	if embyLoginRequest(r) {
		return "/Users/Authenticate", "登录透传并记录身份"
	}
	path := strings.ToLower(r.URL.Path)
	switch {
	case strings.Contains(path, "/images/"):
		return "/…/Images/…", "图片透传"
	case strings.Contains(path, "/subtitles/"):
		return "/…/Subtitles/…", "字幕透传"
	case strings.Contains(path, "/socket"):
		return "/socket", "WebSocket 透传"
	case strings.Contains(path, "/sessions"):
		return "/Sessions/…", "会话/进度透传"
	case strings.Contains(path, "/videos/"), strings.Contains(path, "/audio/"):
		return "/Videos|Audio/…", "其他媒体请求透传"
	default:
		return "其他路径（已隐藏）", "普通透传"
	}
}

func proxyDebugClient(r *http.Request) string {
	ua := strings.ToLower(r.UserAgent())
	if ua == "" {
		return "未提供 UA"
	}
	for _, client := range []string{"yamby", "hills", "infuse", "emby", "vlc", "mpv", "okhttp", "exoplayer", "chrome", "safari"} {
		if strings.Contains(ua, client) {
			return client + "（根据 UA 识别）"
		}
	}
	return "其他客户端（原始 UA 已隐藏）"
}

func proxyDebugCredential(r *http.Request) string {
	for _, key := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if r.Header.Get(key) != "" {
			return key + " 请求头"
		}
	}
	for _, key := range []string{"api_key", "X-Emby-Token"} {
		if proxyQuery(r.URL.Query(), key) != "" {
			return key + " 查询参数"
		}
	}
	if embyToken(r) != "" {
		return "Authorization 类请求头"
	}
	return "未发现播放凭据"
}

func (h *embyProxyHandler) serveDebug(w http.ResponseWriter, r *http.Request) {
	lookup := &proxyLookupDiagnostic{}
	r = r.WithContext(context.WithValue(r.Context(), proxyLookupDiagnosticKey{}, lookup))
	started := time.Now()
	path, route := proxyDebugRoute(r)
	method := r.Method
	switch method {
	case "GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH":
	default:
		method = "其他方法"
	}
	requestHeaders := proxyDiagnosticHeaders(r.Header, r.Host)
	writer := &proxyDebugWriter{ResponseWriter: w}
	completed := false
	defer func() {
		state := "完成"
		if !completed {
			state = "传输中断"
		}
		if r.Context().Err() != nil {
			state = "请求取消或超时"
		}
		status := writer.status
		if status == 0 && completed {
			status = http.StatusOK
		}
		quiet := route == "图片透传" || route == "会话/进度透传" || route == "普通透传"
		if quiet && (status < 400 || r.Context().Err() != nil) {
			h.debugMu.Lock()
			h.debugQuietCount++
			if h.debugQuietSince.IsZero() {
				h.debugQuietSince = time.Now()
			}
			if time.Since(h.debugQuietSince) < 30*time.Second {
				h.debugMu.Unlock()
				return
			}
			count := h.debugQuietCount
			h.debugQuietCount = 0
			h.debugQuietSince = time.Now()
			h.debugMu.Unlock()
			h.logs.Add(r.Context(), "DEBUG", fmt.Sprintf("反代调试：过去 30 秒合并 %d 次图片、会话及普通透传请求；无逐条详情，异常单独记录", count))
			return
		}
		details := service.ProxyLogDetails{Method: method, Path: path, Status: status, DurationMS: time.Since(started).Milliseconds(), RequestHeaders: requestHeaders, ResponseHeaders: proxyDiagnosticHeaders(w.Header(), "")}
		if status == 302 && (path == "/cinema115/play" || staticStreamItem(r) != "" || originalStreamItem(r) != "" || downloadItem(r.URL.Path) != "") && p115.AllowedMediaURL(w.Header().Get("Location")) {
			details.URL = w.Header().Get("Location")
			details.LookupHeaders = proxyLookupHeaders(r.UserAgent())
			details.Lookup = "取链使用的 UA；Cookie 已隐藏。缓存命中时本次未请求 115。播放器访问 CDN 的实际请求头不可由反代观察。"
			if lookup.headers != "" {
				details.LookupHeaders, details.Lookup = lookup.headers, lookup.description
			}
		}
		level := "DEBUG"
		if status == 499 && r.Context().Err() != nil {
			level = "DEBUG"
		} else if status >= 500 {
			level = "ERROR"
		} else if status >= 400 {
			level = "WARNING"
		}
		h.logs.AddDetails(r.Context(), level, fmt.Sprintf("反代调试：%s｜HTTP %d｜状态=%s｜耗时 %d ms｜路径=%s｜路由=%s｜客户端=%s｜凭据来源=%s", method, status, state, details.DurationMS, path, route, proxyDebugClient(r), proxyDebugCredential(r)), details)
	}()
	h.serveRequest(writer, r)
	completed = true
}

// ResponseController preserves streaming and WebSocket upgrades through the wrapper.
type proxyDebugWriter struct {
	http.ResponseWriter
	status int
}

func (w *proxyDebugWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *proxyDebugWriter) WriteHeader(status int) {
	if w.status == 0 && (status >= 200 || status == 101) {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *proxyDebugWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *proxyDebugWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *proxyDebugWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}
