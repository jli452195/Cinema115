package httpapi

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

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
	started := time.Now()
	path, route := proxyDebugRoute(r)
	method := r.Method
	switch method {
	case "GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH":
	default:
		method = "其他方法"
	}
	h.logs.Add(r.Context(), "DEBUG", "反代调试：收到请求｜方法="+method+"｜路径="+path+"｜客户端="+proxyDebugClient(r)+"｜凭据来源="+proxyDebugCredential(r)+"｜路由="+route)
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
		h.logs.Add(r.Context(), "DEBUG", fmt.Sprintf("反代调试：请求结束｜HTTP %d｜状态=%s｜耗时 %d ms｜路径=%s｜路由=%s", status, state, time.Since(started).Milliseconds(), path, route))
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
