package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

var errNotShareSTRM = errors.New("不是支持的分享 STRM")
var embyProxyID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

type embyProxyHandler struct {
	prewarm         proxyPrewarmer
	debugMu         sync.Mutex
	debugQuietSince time.Time
	debugQuietCount uint64
	directLinks     service.DirectLinkCache
	identities      *service.EmbyPlaybackIdentities
	cache           *service.PlaybackCache
	logs            *service.EmbyProxyLogs
	config          service.EmbyProxyConfig
	accounts        *service.P115Accounts
	share           *ShareAPI
	http            *http.Client
	proxy           *httputil.ReverseProxy
	// Injection point for protocol tests; production always uses the 115 client.
	download     func(context.Context, *p115.ShareInfo, string, string, string) (string, error)
	pickDownload func(context.Context, string, string, string) (string, error)
}

func NewEmbyCachedProxyHandler(c service.EmbyProxyConfig, accounts *service.P115Accounts, share *ShareAPI, cache *service.PlaybackCache, logs *service.EmbyProxyLogs, identities ...*service.EmbyPlaybackIdentities) http.Handler {
	h := NewEmbyProxyHandler(c, accounts, share, logs).(*embyProxyHandler)
	h.cache = cache
	if len(identities) > 0 && identities[0] != nil {
		h.identities = identities[0]
	}
	return h
}

func RegisterEmbyProxy(admin *gin.RouterGroup, s *service.EmbyProxy, members *service.MemberPlaybackService) {
	admin.GET("/emby/proxy/direct-links", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.JSON(200, s.DirectLinkStatus()) })
	admin.DELETE("/emby/proxy/direct-links", func(c *gin.Context) {
		s.ClearDirectLinks("")
		c.Header("Cache-Control", "no-store")
		c.JSON(200, s.DirectLinkStatus())
	})
	admin.DELETE("/emby/proxy/direct-links/:id", func(c *gin.Context) {
		id := c.Param("id")
		if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
			c.JSON(400, gin.H{"error": "缓存编号无效"})
			return
		}
		s.ClearDirectLinks(id)
		c.Header("Cache-Control", "no-store")
		c.JSON(200, s.DirectLinkStatus())
	})
	admin.GET("/emby/proxy", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.JSON(200, s.Status()) })
	admin.POST("/emby/proxy", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		input := service.EmbyProxyConfig{DirectLinkCacheMinutes: 9}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "反代配置格式无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		if err := members.ApplyProxy(ctx, s, input); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
	})
}

func (h *embyProxyHandler) DirectLinkStatus() service.DirectLinkCacheStatus {
	return h.directLinks.Status()
}
func (h *embyProxyHandler) ClearDirectLinks(id string) {
	if id == "" {
		h.directLinks.Clear()
	} else {
		h.directLinks.Remove(id)
	}
}

func NewEmbyProxyHandler(c service.EmbyProxyConfig, accounts *service.P115Accounts, share *ShareAPI, logs ...*service.EmbyProxyLogs) http.Handler {
	h := &embyProxyHandler{config: c, accounts: accounts, share: share, download: share.Client.DownloadURLWithUA, pickDownload: share.Client.PickCodeDownloadURLWithUA}
	h.prewarm.ctx, h.prewarm.cancel = context.WithCancel(context.Background())
	h.identities, _ = service.NewEmbyPlaybackIdentities(nil)
	if len(logs) > 0 {
		h.logs = logs[0]
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	h.http = &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	upstream, _ := url.Parse(c.URL)
	h.proxy = &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(upstream)
			p.SetXForwarded()
			p.Out.Header.Set("X-Cinema115-Emby-Proxy", "1")
			if playbackItem(p.In.URL.Path) != "" || embyLoginRequest(p.In) {
				p.Out.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: h.modifyResponse,
		// net/http's default proxy logger includes upstream URLs containing Emby tokens.
		ErrorLog:     log.New(io.Discard, "", 0),
		ErrorHandler: h.proxyError,
	}
	return h
}

type proxyRequestStartedKey struct{}

// Only fixed endpoint categories are logged; raw paths can contain credentials.
func proxyRequestDescription(r *http.Request) string {
	kind := "其他 Emby 接口"
	path := strings.ToLower(r.URL.Path)
	switch {
	case playbackItem(r.URL.Path) != "":
		kind = "播放信息 PlaybackInfo"
	case strings.Contains(path, "/images/"):
		kind = "图片"
	case strings.Contains(path, "/subtitles/"):
		kind = "字幕"
	case strings.Contains(path, "/videos/") || strings.Contains(path, "/audio/"):
		kind = "媒体流"
	case strings.Contains(path, "/sessions/"):
		kind = "会话/播放进度"
	case strings.Contains(path, "/items"):
		kind = "媒体条目"
	case strings.Contains(path, "/users/"):
		kind = "用户"
	case strings.Contains(path, "/socket"):
		kind = "WebSocket"
	}
	method := "其他方法"
	switch r.Method {
	case "GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH":
		method = r.Method
	}
	detail := method + "，" + kind
	if started, ok := r.Context().Value(proxyRequestStartedKey{}).(time.Time); ok {
		detail += fmt.Sprintf("，耗时 %d ms", time.Since(started).Milliseconds())
	}
	return detail
}

func (h *embyProxyHandler) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	detail := proxyRequestDescription(r)
	if errors.Is(r.Context().Err(), context.Canceled) {
		// Browsing routinely cancels off-screen artwork; keep the log buffer for playback diagnostics.
		if strings.Contains(strings.ToLower(r.URL.Path), "/images/") {
			w.WriteHeader(499)
			return
		}
		reason := "请求上下文已取消（客户端断开、取消加载或中间连接关闭）"
		if service.EmbyProxyStopping(r.Context()) {
			reason = "反代服务正在停止或切换端口，已取消进行中的请求"
		}
		h.logs.Add(r.Context(), "DEBUG", reason+"；"+detail)
		// The requester is no longer waiting. Do not present a false upstream 502 failure.
		w.WriteHeader(499)
		return
	}
	if errors.Is(err, context.Canceled) {
		h.logs.Add(r.Context(), "WARNING", "Emby 上游操作已取消，但客户端请求仍有效，取消来源未确认；"+detail)
		http.Error(w, "Emby 请求中断，请重试", http.StatusBadGateway)
		return
	}
	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded) || errors.As(err, &networkErr) && networkErr.Timeout() {
		h.logs.Add(r.Context(), "ERROR", "Emby 转发超时；"+detail)
		http.Error(w, "Emby 响应超时", http.StatusGatewayTimeout)
		return
	}
	h.logs.Add(r.Context(), "ERROR", "Emby 转发失败："+proxyFailureReason(err)+"；"+detail)
	http.Error(w, "无法连接 Emby 或读取播放信息", http.StatusBadGateway)
}

func proxyQuery(q url.Values, name string) string {
	for key, values := range q {
		if strings.EqualFold(key, name) && len(values) == 1 {
			return values[0]
		}
	}
	return ""
}

func embyToken(r *http.Request) string {
	for _, key := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if token := r.Header.Get(key); token != "" {
			return token
		}
	}
	for _, key := range []string{"api_key", "X-Emby-Token"} {
		if token := proxyQuery(r.URL.Query(), key); token != "" {
			return token
		}
	}
	for _, header := range []string{"Authorization", "X-Emby-Authorization"} {
		value := r.Header.Get(header)
		if strings.HasPrefix(strings.ToLower(value), "bearer ") {
			return strings.TrimSpace(value[7:])
		}
		for _, part := range strings.Split(value, ",") {
			key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			key = strings.TrimSpace(strings.TrimPrefix(key, "MediaBrowser "))
			if ok && strings.EqualFold(key, "Token") {
				return strings.Trim(value, "\" ")
			}
		}
	}
	return ""
}

func playbackItem(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && strings.EqualFold(parts[len(parts)-3], "Items") && strings.EqualFold(parts[len(parts)-1], "PlaybackInfo") && embyProxyID.MatchString(parts[len(parts)-2]) {
		return parts[len(parts)-2]
	}
	return ""
}

// Downloads use a separate Emby endpoint; never infer download permission from playback alone.
func downloadItem(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 4 && strings.EqualFold(parts[0], "emby") {
		parts = parts[1:]
	}
	if len(parts) == 3 && strings.EqualFold(parts[0], "Items") && strings.EqualFold(parts[2], "Download") && embyProxyID.MatchString(parts[1]) {
		return parts[1]
	}
	return ""
}

func proxyTransferKind(r *http.Request) string {
	if downloadItem(r.URL.Path) != "" {
		return "下载"
	}
	return "播放"
}

func staticStreamItem(r *http.Request) string {
	if !strings.EqualFold(proxyQuery(r.URL.Query(), "Static"), "true") {
		return ""
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || !strings.EqualFold(parts[len(parts)-3], "Videos") {
		return ""
	}
	stream := strings.ToLower(parts[len(parts)-1])
	if stream != "stream" && (!strings.HasPrefix(stream, "stream.") || strings.HasSuffix(stream, ".m3u8")) {
		return ""
	}
	if embyProxyID.MatchString(parts[len(parts)-2]) {
		return parts[len(parts)-2]
	}
	return ""
}

func (h *embyProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	r = r.WithContext(h.logs.Trace(r.Context()))
	ctx, finish := h.logs.BeginRequest(r.Context(), h.config.Debug)
	defer finish()
	r = r.WithContext(ctx)
	r = r.WithContext(context.WithValue(r.Context(), proxyRequestStartedKey{}, started))
	if h.config.Debug && (playbackItem(r.URL.Path) != "" || proxyTransferKind(r) == "下载" || staticStreamItem(r) != "" || originalStreamItem(r) != "" || r.URL.Path == "/cinema115/play" || r.URL.Path == "/emby/cinema115/play") {
		h.logs.Add(r.Context(), "INFO", "播放请求进入反代，关联编号="+service.ProxyTraceID(r.Context())+"，"+proxyRequestDescription(r))
		defer func() {
			h.logs.Add(r.Context(), "INFO", fmt.Sprintf("播放请求处理结束，从进入反代起耗时 %d ms", time.Since(started).Milliseconds()))
		}()
	}
	if h.config.Debug {
		h.serveDebug(w, r)
		return
	}
	h.serveRequest(w, r)
}

func (h *embyProxyHandler) serveRequest(w http.ResponseWriter, r *http.Request) {
	if blockedEmbyTranscode(r) {
		h.logs.Add(r.Context(), "WARNING", "已禁止转码/封装转换请求，HTTP 403；请使用支持原文件格式的客户端")
		http.Error(w, "反代已关闭转码，请使用支持原文件格式的客户端", http.StatusForbidden)
		return
	}
	if h.cache != nil && h.cache.Members != nil {
		userID := h.identities.UserID(h.config.URL, embyToken(r))
		if h.cache.Members.ChildSessionDenied(h.config.URL, userID) {
			http.Error(w, "播放子账号已停用，请联系账号所有者", http.StatusForbidden)
			return
		}
		path := strings.ToLower(r.URL.Path)
		media := playbackItem(r.URL.Path) != "" || strings.Contains(path, "/videos/") || strings.Contains(path, "/audio/") || downloadItem(r.URL.Path) != "" || strings.Contains(path, "/cinema115/play") || strings.Contains(path, "/livetv/")
		direct := downloadItem(r.URL.Path) != "" || staticStreamItem(r) != "" || originalStreamItem(r) != "" || r.URL.Path == "/cinema115/play" || r.URL.Path == "/emby/cinema115/play"
		if media && userID == "" {
			http.Error(w, "请通过反代退出并重新登录，以确认播放身份", http.StatusUnauthorized)
			return
		}
		if media && !direct && playbackItem(r.URL.Path) == "" && !strings.Contains(path, "/subtitles/") {
			http.Error(w, "个人播放仅支持原文件直链，不支持转码或直播", http.StatusForbidden)
			return
		}
		// Direct-link handlers authenticate once below. Browsing requests never probe 115.
		if media && !direct {
			if _, err := h.cache.Members.AuthorizeMedia(r.Context(), h.config.URL, userID, false); err != nil {
				message, status := playbackAuthorizationFailure(r.Context(), err)
				level := "WARNING"
				if status == 499 {
					level = "DEBUG"
				}
				h.logs.Add(r.Context(), level, message)
				http.Error(w, message, status)
				return
			}
		}
	}
	if r.Header.Get("X-Cinema115-Emby-Proxy") != "" {
		h.logs.Add(r.Context(), "ERROR", "检测到反代循环，已拒绝请求")
		http.Error(w, "Emby 上游指向了反代自身，请修正服务器地址", http.StatusLoopDetected)
		return
	}
	if item := downloadItem(r.URL.Path); item != "" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "下载入口仅支持 GET 或 HEAD", http.StatusMethodNotAllowed)
			return
		}
		h.redirect(w, r, item, true)
		return
	}
	// Some Emby clients prepend /emby when resolving relative playback URLs.
	if r.URL.Path == "/cinema115/play" || r.URL.Path == "/emby/cinema115/play" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.logs.Add(r.Context(), "WARNING", "播放入口拒绝不支持的请求方法，HTTP 405")
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "请求方法不支持", 405)
			return
		}
		h.redirect(w, r, proxyQuery(r.URL.Query(), "ItemId"), false)
		return
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && staticStreamItem(r) != "" {
		h.redirect(w, r, staticStreamItem(r), true)
		return
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && originalStreamItem(r) != "" {
		h.redirect(w, r, originalStreamItem(r), true)
		return
	}
	if item := playbackItem(r.URL.Path); item != "" {
		ctx, finish := h.traceEmbyRequest(r.Context(), "客户端 PlaybackInfo 转发（含响应改写与返回）")
		r = r.WithContext(ctx)
		defer finish()
		h.logs.Add(r.Context(), "INFO", "读取 Emby 播放信息，条目="+item)
		if err := disablePlaybackTranscoding(r); err != nil {
			h.logs.Add(r.Context(), "ERROR", "播放协商参数无效或超出大小限制，HTTP 400")
			http.Error(w, "播放协商参数无效或过大", 400)
			return
		}
	}
	h.proxy.ServeHTTP(w, r)
}

func blockedEmbyTranscode(r *http.Request) bool {
	parts := strings.Split(strings.Trim(strings.ToLower(r.URL.Path), "/"), "/")
	if len(parts) > 0 && parts[0] == "emby" {
		parts = parts[1:]
	}
	if len(parts) < 3 || (parts[0] != "videos" && parts[0] != "audio") {
		return false
	}
	endpoint := parts[2]
	if strings.Contains(endpoint, "m3u8") || strings.HasPrefix(endpoint, "hls") || strings.HasPrefix(endpoint, "dash") || endpoint == "universal" {
		return true
	}
	return (endpoint == "stream" || strings.HasPrefix(endpoint, "stream.")) && !strings.EqualFold(proxyQuery(r.URL.Query(), "Static"), "true")
}

func disablePlaybackTranscoding(r *http.Request) error {
	query := r.URL.Query()
	for key := range query {
		if strings.EqualFold(key, "EnableTranscoding") {
			delete(query, key)
		}
	}
	query.Set("EnableTranscoding", "false")
	r.URL.RawQuery = query.Encode()
	if r.Method != http.MethodPost || r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return fmt.Errorf("播放参数读取失败")
	}
	var body map[string]json.RawMessage
	if len(bytes.TrimSpace(data)) > 0 && json.Unmarshal(data, &body) != nil {
		return fmt.Errorf("播放参数格式无效")
	}
	if body == nil {
		body = make(map[string]json.RawMessage)
	}
	for key := range body {
		if strings.EqualFold(key, "MediaSourceId") {
			*r = *r.WithContext(context.WithValue(r.Context(), playbackSelectedSourceKey{}, jsonString(body[key])))
		}
		if strings.EqualFold(key, "EnableTranscoding") {
			delete(body, key)
		}
	}
	body["EnableTranscoding"] = json.RawMessage("false")
	data, err = json.Marshal(body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.Header.Set("Content-Type", "application/json")
	return nil
}

// Never include upstream error text: it may contain signed URLs, tokens or API response bodies.
func proxyFailureReason(err error) string {
	if errors.Is(err, context.Canceled) {
		return "请求已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时"
	}
	if errors.Is(err, p115.ErrShareUnderReview) {
		return "分享审核中"
	}
	var upstream *p115.HTTPError
	if errors.As(err, &upstream) {
		return fmt.Sprintf("115 返回 HTTP %d", upstream.Status)
	}
	return "上游请求或响应无效，请检查连接、账号及分享状态"
}

func proxyLogID(id string) string {
	if embyProxyID.MatchString(id) {
		return id
	}
	return "未提供或无效"
}

func (h *embyProxyHandler) embyJSON(ctx context.Context, token, stage, path string, query url.Values, out any) (int, error) {
	ctx, finish := h.traceEmbyRequest(ctx, stage)
	defer finish()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.config.URL+"/emby"+path+"?"+query.Encode(), nil)
	if err != nil {
		return 502, fmt.Errorf("Emby %s：创建请求失败", stage)
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			message, status := playbackAuthorizationFailure(ctx, ctx.Err())
			return status, errors.New(message)
		}
		return 502, fmt.Errorf("Emby %s：%s", stage, proxyFailureReason(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
			return resp.StatusCode, fmt.Errorf("Emby %s失败：上游 HTTP %d", stage, resp.StatusCode)
		}
		return 502, fmt.Errorf("Emby %s失败：上游 HTTP %d", stage, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 || json.Unmarshal(data, out) != nil {
		return 502, fmt.Errorf("Emby %s：上游 HTTP 200，响应格式无效或超过 8 MB", stage)
	}
	return 200, nil
}

// No administrative API key is used: every redirect is authorized as the requesting Emby user.
func (h *embyProxyHandler) redirect(w http.ResponseWriter, r *http.Request, item string, fallback bool) {
	timing := h.newPlaybackTimeline(r.Context())
	defer timing.finish()
	warm, warming := r.Context().Value(prewarmContextKey{}).(*prewarmSnapshot)
	if !warming {
		finish := h.foregroundPlayback()
		defer finish()
	}
	started := time.Now()
	kind := proxyTransferKind(r)
	if warming {
		kind = "预热"
	}
	isDownload := kind == "下载"
	outcome := "未完成"
	defer func() {
		h.logs.Add(r.Context(), "INFO", fmt.Sprintf("%s解析结束：%s，耗时 %d ms", kind, outcome, time.Since(started).Milliseconds()))
	}()
	fail := func(message string, status int) {
		outcome = fmt.Sprintf("HTTP %d", status)
		level := "ERROR"
		if status == 499 {
			level = "DEBUG"
		}
		h.logs.Add(r.Context(), level, outcome+"："+message)
		http.Error(w, message, status)
	}
	h.logs.Add(r.Context(), "INFO", "开始直链"+kind+"解析，方法="+r.Method+"，条目="+proxyLogID(item)+"，媒体源="+proxyLogID(proxyQuery(r.URL.Query(), "MediaSourceId"))+"，账号配置="+proxyLogID(h.config.AccountID))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	token := embyToken(r)
	if !embyProxyID.MatchString(item) || token == "" || len(token) > 2048 {
		fail("缺少有效的 Emby 播放凭据", 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	// Capture credentials once, before any I/O; configuration changes cannot switch this request's account.
	var sourceAccount service.P115Account
	var cacheSnapshot service.PlaybackCacheSnapshot
	if warming {
		sourceAccount, cacheSnapshot = warm.source, warm.bindings
	} else {
		sourceAccount, _ = h.accounts.Get(h.accounts.PrimaryID())
		if h.cache != nil {
			cacheSnapshot = h.cache.Snapshot()
		}
	}
	cookie := sourceAccount.Cookie
	sourceAccountLabel := service.PlaybackAccountLabel(sourceAccount.Name, p115.CookieUserID(cookie), sourceAccount.ID)
	var user emby.ManagedUser
	var authorization *service.MediaAuthorization
	timing.next("Emby 用户权限查询")
	h.logs.Add(ctx, "DEBUG", "开始校验 Emby 用户身份与"+kind+"权限")
	userID := h.identities.UserID(h.config.URL, token)
	if !embyProxyID.MatchString(userID) {
		fail("尚未记录此 Emby 登录身份，请在播放器中通过 302 反代地址退出并重新登录一次", 401)
		return
	}
	status, err := h.embyJSON(ctx, token, "用户权限查询 GET /Users/{Id}", "/Users/"+userID, nil, &user)
	if err != nil {
		fail(err.Error(), status)
		return
	}
	if user.ID != userID || user.Policy["IsDisabled"] == true {
		fail("Emby 用户不可用", 403)
		return
	}
	if !isDownload && user.Policy["EnableMediaPlayback"] != true {
		fail("Emby 用户没有播放权限", 403)
		return
	}
	if h.cache != nil && h.cache.Members != nil {
		timing.next("业务身份与网盘实时核验")
		authorized, err := h.cache.Members.BeginMediaAuthorization(ctx, h.config.URL, userID, isDownload, &user)
		if err != nil {
			fail(playbackAuthorizationFailure(ctx, err))
			return
		}
		authorization = authorized
		if authorized != nil {
			fallback = false
			if isDownload && !h.cache.Members.Settings().AllowDownload {
				fail("管理员尚未允许个人用户下载", 403)
				return
			}
		}
	}
	if isDownload && user.Policy["EnableContentDownloading"] != true {
		fail("Emby 用户未获准下载，请在 Emby 用户权限中开启内容下载", http.StatusForbidden)
		return
	}
	var allowed struct {
		ID           string `json:"Id"`
		PlayAccess   string
		CanDownload  *bool
		IsFolder     bool
		MediaSources []map[string]json.RawMessage
	}
	timing.next("Emby 条目权限查询")
	// Fetch source identity with the per-request item permission check. This
	// does not replace the complete PlaybackInfo sent to the player earlier.
	itemQuery := url.Values{"Fields": {"MediaSources"}}
	status, err = h.embyJSON(ctx, token, "条目权限查询 GET /Users/{Id}/Items/{Id}", "/Users/"+user.ID+"/Items/"+item, itemQuery, &allowed)
	if err != nil {
		fail(err.Error(), status)
		return
	}
	if allowed.ID != item || !isDownload && allowed.PlayAccess != "" && !strings.EqualFold(allowed.PlayAccess, "Full") {
		fail("Emby 用户无权播放此条目", http.StatusForbidden)
		return
	}
	if isDownload && (allowed.IsFolder || allowed.CanDownload == nil || !*allowed.CanDownload) {
		fail("此条目不允许下载", http.StatusForbidden)
		return
	}
	h.logs.Add(ctx, "INFO", "Emby 用户与条目"+kind+"权限校验通过，用户="+service.PlaybackLogName(user.Name)+"（ID="+user.ID+"）")
	sourceID := proxyQuery(r.URL.Query(), "MediaSourceId")
	query := url.Values{"UserId": {user.ID}}
	// Resolving a 115 source never requires Emby to open the old STRM host.
	metadataOnlyQuery(query)
	if sourceID != "" {
		query.Set("MediaSourceId", sourceID)
	}
	var info struct {
		MediaSources []map[string]json.RawMessage
		ErrorCode    string
	}
	var claim *sharePlaybackParams
	timing.next("Emby 媒体源确定")
	if isDownload {
		// Download-only users cannot negotiate PlaybackInfo. Resolve the original
		// source from the item returned under their own token and download permission.
		info.MediaSources = allowed.MediaSources
	} else if warming && warm.media != nil && jsonString(warm.media["Id"]) == sourceID {
		info.MediaSources = []map[string]json.RawMessage{warm.media}
		h.logs.Add(ctx, "INFO", "预热复用本次 PlaybackInfo 已确认的媒体源，跳过重复播放信息查询")
	} else if source, parsed := h.itemPlaybackSource(allowed.MediaSources, sourceID); parsed != nil {
		info.MediaSources, claim = []map[string]json.RawMessage{source}, parsed
		h.logs.Add(ctx, "INFO", "复用本次条目权限查询返回的 STRM 媒体源，跳过重复 PlaybackInfo 查询；首次完整播放协商保持不变")
	} else {
		status, err = h.embyJSON(ctx, token, "播放信息查询 GET /Items/{Id}/PlaybackInfo", "/Items/"+item+"/PlaybackInfo", query, &info)
		if err != nil {
			fail(err.Error(), status)
			return
		}
	}
	if info.ErrorCode != "" {
		fail("Emby 拒绝播放此媒体", 403)
		return
	}
	var source map[string]json.RawMessage
	for _, candidate := range info.MediaSources {
		if sourceID != "" && jsonString(candidate["Id"]) == sourceID {
			source = candidate
			break
		}
	}
	if sourceID == "" && len(info.MediaSources) == 1 {
		source = info.MediaSources[0]
	}
	if source == nil {
		fail("无法确定播放版本，请重新选择媒体源", 409)
		return
	}
	timing.next("STRM 读取解析")
	h.logs.Add(ctx, "DEBUG", "媒体源已确定，开始读取并解析 STRM，媒体源="+proxyLogID(jsonString(source["Id"])))
	if claim == nil {
		claim, err = h.playbackClaim(source)
	}
	if errors.Is(err, errNotShareSTRM) && fallback {
		outcome = "转交 Emby"
		h.logs.Add(ctx, "WARNING", "媒体源不符合支持的分享链接格式或本地路径映射，转交 Emby 原"+kind+"链路")
		h.proxy.ServeHTTP(w, r)
		return
	}
	if err != nil {
		h.logs.Add(ctx, "ERROR", "STRM 读取解析失败："+err.Error())
		fail("无法解析分享 STRM，请检查链接格式、必要分享参数及本地媒体路径映射", 422)
		return
	}
	timing.next("播放账号绑定检查")
	var cacheSelection *service.PlaybackCacheSelection
	if h.cache != nil {
		cacheSelection, err = cacheSnapshot.Select(h.config.URL, user.ID)
		if err != nil {
			fail(err.Error(), 503)
			return
		}
		if authorization != nil {
			if err := authorization.CheckCurrent(ctx); err != nil {
				fail(playbackAuthorizationFailure(ctx, err))
				return
			}
			if !authorization.MatchesSelection(sourceAccount, cacheSelection) {
				fail("播放来源绑定已变化，请重新发起播放", http.StatusForbidden)
				return
			}
		}
	}
	if cookie == "" && cacheSelection == nil {
		fail("302 使用的 115 账号未选择、已删除或未登录，请管理员重新配置", 503)
		return
	}
	if claim.PickCode != "" {
		h.logs.Add(ctx, "INFO", "网盘 PickCode STRM 解析成功，优先匹配已有文件资料；pickcode="+proxyLogID(claim.PickCode))
	} else {
		h.logs.Add(ctx, "INFO", "STRM 解析成功，文件编号="+proxyLogID(claim.FileID))
	}
	downloadStarted := time.Now()
	if warming {
		timing.next("预热文件准备")
	} else {
		timing.next("等待同源预热")
	}
	if !warming {
		if err := h.waitPrewarm(ctx, h.prewarmKey(token, item, jsonString(source["Id"]), sourceAccount, cacheSelection)); err != nil {
			fail("等待播放预热已取消或超时", 504)
			return
		}
	}
	// Include effective credentials only in a digest; neither keys nor views expose secrets.
	key := service.DirectLinkKey(h.config.URL, user.ID, item, jsonString(source["Id"]), claim.Code, claim.Password, claim.FileID, claim.PickCode, claim.SHA1, sourceAccount.ID, strconv.FormatUint(sourceAccount.Revision, 10), cookie, cacheSelection.DirectLinkScope(), r.UserAgent())
	fetch := func(fetchCtx context.Context) (service.DirectLinkValue, error) {
		h.logs.Add(fetchCtx, "INFO", "直链缓存未命中或已关闭，开始向 115 获取直链")
		value := service.DirectLinkValue{Report: service.PlaybackResolution{Mode: "源账号分享直取", AccountLabel: sourceAccountLabel}}
		var fetchErr error
		if claim.PickCode != "" && cacheSelection != nil && !cacheSelection.UsesSourceAccount(sourceAccount.ID) {
			value.URL, value.Report, fetchErr = h.cache.ResolvePickCodeWithReport(service.WithPlaybackSHA1Hint(fetchCtx, claim.SHA1), cacheSelection, claim.PickCode, cookie, r.UserAgent(), sourceAccount.ID)
		} else if claim.PickCode != "" {
			value.URL, fetchErr = h.pickDownload(fetchCtx, claim.PickCode, cookie, r.UserAgent())
			value.Report.Mode = "源账号网盘直取"
		} else if cacheSelection != nil {
			value.URL, value.Report, fetchErr = h.cache.ResolveWithReport(fetchCtx, cacheSelection, &p115.ShareInfo{Code: claim.Code, Password: claim.Password}, claim.FileID, cookie, r.UserAgent())
		} else {
			value.URL, fetchErr = h.download(fetchCtx, &p115.ShareInfo{Code: claim.Code, Password: claim.Password}, claim.FileID, cookie, r.UserAgent())
		}
		if fetchErr != nil {
			return service.DirectLinkValue{}, fetchErr
		}
		if !p115.AllowedMediaURL(value.URL) {
			return service.DirectLinkValue{}, fmt.Errorf("115 返回的直链无效")
		}
		return value, nil
	}
	view := service.DirectLinkCacheEntry{User: service.PlaybackLogName(user.Name) + "（" + user.ID + "）", MediaSource: proxyLogID(jsonString(source["Id"])), TraceID: service.ProxyTraceID(ctx), RequestHeaders: proxyDiagnosticHeaders(r.Header, r.Host), LookupHeaders: proxyLookupHeaders(r.UserAgent())}
	if warming && warm.prepareOnly {
		// Share URLs may be directly readable by the target. Do not turn a
		// read-only share playback into an unsolicited upload or extra URL lookup.
		if claim.PickCode == "" || cacheSelection == nil || cacheSelection.UsesSourceAccount(sourceAccount.ID) {
			outcome = "无需提前准备"
			h.logs.Add(ctx, "INFO", "此来源无需提前秒传，正式播放时按原流程解析")
			w.WriteHeader(http.StatusOK)
			return
		}
		_, report, err := h.cache.ResolvePickCodeWithReport(service.WithPlaybackSHA1Hint(ctx, claim.SHA1), cacheSelection, claim.PickCode, cookie, r.UserAgent(), sourceAccount.ID)
		if err != nil {
			fail(err.Error(), 502)
			return
		}
		outcome = "文件已准备"
		h.logs.Add(ctx, "INFO", "文件准备成功｜取链账号="+report.AccountLabel+"｜方式="+report.Mode+"；未预取播放直链")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	timing.next("直链缓存等待及 115 取链")
	value, hit, expires, err := h.directLinks.Get(ctx, key, time.Duration(h.config.DirectLinkCacheMinutes)*time.Minute, view, fetch)
	timing.next("缓存记录核验及响应")
	if err == nil && hit && value.Report.FileCacheKey != "" {
		if touchErr := h.cache.TouchDirectLink(ctx, value.Report.FileCacheKey, value.Report.FileCacheID); touchErr != nil {
			if !errors.Is(touchErr, service.ErrPlaybackCopyChanged) {
				fail(touchErr.Error(), 502)
				return
			}
			h.directLinks.Remove(key)
			h.logs.Add(ctx, "INFO", "直链缓存对应副本已失效，清除旧链接并在本次请求重新解析")
			value, hit, expires, err = h.directLinks.Get(ctx, key, time.Duration(h.config.DirectLinkCacheMinutes)*time.Minute, view, fetch)
		}
	}
	link, report := value.URL, value.Report
	if hit && (report.Mode == "新建秒传缓存" || report.Mode == "新建秒传文件") {
		report.Mode = "复用同 SHA1 文件"
	}
	if err != nil && cacheSelection != nil {
		var pending *service.PlaybackPendingError
		if errors.As(err, &pending) {
			outcome = "HTTP 503（等待秒传结果确认）"
			w.Header().Set("Retry-After", strconv.Itoa(pending.RetrySeconds()))
			h.logs.Add(ctx, "WARNING", outcome+"："+pending.Error())
			http.Error(w, pending.Error(), http.StatusServiceUnavailable)
			return
		}
		fail(err.Error(), 502)
		return
	}
	if err != nil || !p115.AllowedMediaURL(link) {
		h.logs.Add(ctx, "ERROR", fmt.Sprintf("115 直链获取失败，耗时 %d ms：%s", time.Since(downloadStarted).Milliseconds(), proxyFailureReason(err)))
		fail("115 直链获取失败，请检查所选账号、分享状态及播放客户端", 502)
		return
	}
	if authorization != nil {
		if err := authorization.Revalidate(ctx); err != nil {
			fail(playbackAuthorizationFailure(ctx, err))
			return
		}
	}
	// Empty body prevents accidentally embedding signed CDN URLs into rendered HTML or logs.
	cdn, _ := url.Parse(link)
	if warming {
		outcome = "已准备"
		h.logs.AddDetails(ctx, "INFO", "预热取链成功｜取链账号="+report.AccountLabel+"｜方式="+report.Mode, service.ProxyLogDetails{URL: link, RequestHeaders: view.RequestHeaders, LookupHeaders: view.LookupHeaders})
		w.WriteHeader(http.StatusFound)
		return
	}
	if hit && expires.IsZero() {
		h.logs.Add(ctx, "INFO", "复用同一时刻的取链请求；直链缓存关闭，未保存直链")
	} else if hit {
		h.logs.Add(ctx, "INFO", fmt.Sprintf("直链缓存命中｜取链账号=%s｜剩余 %d 秒（有效期不顺延）", report.AccountLabel, max(0, int(time.Until(expires).Seconds()))))
	}
	h.logs.Add(ctx, "INFO", fmt.Sprintf("115 直链获取成功，CDN=%s，耗时 %d ms", cdn.Hostname(), time.Since(downloadStarted).Milliseconds()))
	outcome = "HTTP 302"
	summary := kind + "取链结果：用户=" + service.PlaybackLogName(user.Name) + "（ID=" + user.ID + "）｜条目=" + proxyLogID(item) + "｜媒体源=" + proxyLogID(jsonString(source["Id"])) + "｜" + kind + "方式=" + report.Mode + "｜取链账号=" + report.AccountLabel
	if hit {
		summary += "｜直链缓存=命中"
	} else {
		summary += "｜直链缓存=本次取链"
	}
	if report.UsedSource {
		summary += "｜源读取账号=" + sourceAccountLabel
		if hit {
			summary += "（缓存建立时使用，本次未重新取链）"
		}
	}
	h.logs.Add(ctx, "INFO", summary+"｜返回 HTTP 302（不代表客户端已完成"+kind+"）")
	h.logs.Add(ctx, "INFO", "返回 HTTP 302，客户端接下来直接访问 115；此处无法确认 CDN 下载或实际播放结果")
	w.Header().Set("Location", link)
	w.WriteHeader(http.StatusFound)
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func jsonTrue(raw json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

func (h *embyProxyHandler) playbackClaim(source map[string]json.RawMessage) (*sharePlaybackParams, error) {
	raw, err := h.sourceURL(source)
	if err != nil {
		return nil, err
	}
	if claim, ok := parsePickCodeSTRMClaim(raw); ok {
		return claim, nil
	}
	return h.share.parseSTRMURL(raw)
}

func (h *embyProxyHandler) sourceClaim(source map[string]json.RawMessage) (*sharePlaybackParams, error) {
	raw, err := h.sourceURL(source)
	if err != nil {
		return nil, err
	}
	return h.share.parseSTRMURL(raw)
}

func (h *embyProxyHandler) sourceURL(source map[string]json.RawMessage) (string, error) {
	raw := jsonString(source["Path"])
	if strings.EqualFold(filepath.Ext(raw), ".strm") {
		prefix := h.config.EmbyPathPrefix
		remote := strings.ReplaceAll(raw, "\\", "/")
		if prefix == "" || !strings.HasPrefix(remote, prefix+"/") {
			return "", errNotShareSTRM
		}
		relative := strings.TrimPrefix(remote, prefix+"/")
		if !filepath.IsLocal(filepath.FromSlash(relative)) {
			return "", fmt.Errorf("STRM 路径越界")
		}
		root, err := filepath.EvalSymlinks(h.share.OutputRoot)
		if err != nil {
			return "", fmt.Errorf("无法读取 STRM 根目录")
		}
		file, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", fmt.Errorf("无法找到 STRM 文件")
		}
		rel, err := filepath.Rel(root, file)
		if err != nil || !filepath.IsLocal(rel) {
			return "", fmt.Errorf("STRM 路径越界")
		}
		f, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("无法读取 STRM 文件")
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			return "", fmt.Errorf("STRM 不是普通文件")
		}
		data, err := io.ReadAll(io.LimitReader(f, 16385))
		if err != nil || len(data) > 16384 {
			return "", fmt.Errorf("STRM 内容无效")
		}
		raw = strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	}
	return raw, nil
}

func (h *embyProxyHandler) rewritePlayback(resp *http.Response) error {
	item := playbackItem(resp.Request.URL.Path)
	token := embyToken(resp.Request)
	ctx := resp.Request.Context()
	if item != "" {
		h.logs.Add(ctx, "DEBUG", fmt.Sprintf("Emby 播放信息响应 HTTP %d", resp.StatusCode))
		if token == "" {
			h.logs.Add(ctx, "WARNING", "缺少客户端 Emby 凭据，不改写直链入口；仍禁用转码")
		}
		if resp.StatusCode != 200 {
			h.logs.Add(ctx, "WARNING", fmt.Sprintf("Emby 播放信息查询未成功，HTTP %d，保留原响应", resp.StatusCode))
		}
	}
	if item == "" || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		h.logs.Add(ctx, "ERROR", "Emby 播放信息读取失败或响应超出 8 MB")
		return fmt.Errorf("Emby 播放信息读取失败")
	}
	var info map[string]json.RawMessage
	var sources []map[string]json.RawMessage
	if json.Unmarshal(data, &info) != nil || json.Unmarshal(info["MediaSources"], &sources) != nil {
		h.logs.Add(ctx, "ERROR", "Emby 播放信息 JSON 或媒体源格式无效")
		return fmt.Errorf("Emby 播放信息格式无效")
	}
	// Transcoding is disabled for every media source, including non-share files.
	rewritten, playable := 0, false
	prewarmSources := make([]map[string]json.RawMessage, 0, len(sources))
	h.logs.Add(ctx, "DEBUG", fmt.Sprintf("开始检查 %d 个媒体源", len(sources)))
	for _, source := range sources {
		sourceLabel := "媒体源=" + proxyLogID(jsonString(source["Id"]))
		source["SupportsTranscoding"] = json.RawMessage("false")
		delete(source, "TranscodingUrl")
		delete(source, "TranscodingSubProtocol")
		delete(source, "TranscodingContainer")
		// DirectStream can mean remuxing or audio conversion; only true direct play may bypass Emby.
		if !jsonTrue(source["SupportsDirectPlay"]) {
			source["SupportsDirectStream"] = json.RawMessage("false")
			delete(source, "DirectStreamUrl")
			h.logs.Add(ctx, "WARNING", sourceLabel+"：客户端不支持原文件直接播放，已禁用转码与封装转换")
			continue
		}
		playable = true
		if token == "" {
			continue
		}
		claim, err := h.playbackClaim(source)
		if err != nil {
			h.logs.Add(ctx, "WARNING", sourceLabel+"："+err.Error()+"；未接管取链，交由 Emby 原链路处理（不保证上游可播放）")
			continue
		}
		id := jsonString(source["Id"])
		if id == "" {
			h.logs.Add(ctx, "WARNING", "媒体源编号为空，未改写播放入口")
			continue
		}
		query := url.Values{"ItemId": {item}, "MediaSourceId": {id}, "api_key": {token}}
		source["DirectStreamUrl"], _ = json.Marshal("/cinema115/play?" + query.Encode())
		// Clients must enter our authenticated endpoint instead of opening the original STRM URL.
		source["SupportsDirectPlay"] = json.RawMessage("false")
		source["SupportsDirectStream"] = json.RawMessage("true")
		rewritten++
		prewarmSources = append(prewarmSources, source)
		if claim.PickCode != "" {
			h.logs.Add(ctx, "INFO", sourceLabel+"：网盘 pickcode STRM 识别成功，播放入口已改写为 302 解析入口；来源为配置的源账号")
		} else {
			h.logs.Add(ctx, "INFO", sourceLabel+"：分享 STRM 识别成功，播放入口已改写为 302 解析入口")
		}
	}
	if rewritten == 0 {
		h.logs.Add(ctx, "INFO", "没有可接管的分享或网盘 pickcode 源；转码已禁用")
	}
	if !playable {
		info["ErrorCode"] = json.RawMessage(`"NoCompatibleStream"`)
		h.logs.Add(ctx, "WARNING", "没有兼容的原文件直放源，请更换播放器；不会回退转码")
	}
	info["MediaSources"], err = json.Marshal(sources)
	if err != nil {
		return err
	}
	data, err = json.Marshal(info)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	resp.ContentLength = int64(len(data))
	resp.Header.Set("Content-Length", strconv.Itoa(len(data)))
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Del("ETag")
	if jsonString(info["ErrorCode"]) == "" && (len(sources) == 1 || proxyQuery(resp.Request.URL.Query(), "MediaSourceId") != "" || resp.Request.Context().Value(playbackSelectedSourceKey{}) != nil) {
		h.schedulePrewarm(resp.Request, item, prewarmSources)
	}
	return nil
}
