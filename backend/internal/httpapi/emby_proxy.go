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
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

var errNotShareSTRM = errors.New("不是配置的分享 STRM")
var embyProxyID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

type embyProxyHandler struct {
	logs     *service.EmbyProxyLogs
	config   service.EmbyProxyConfig
	accounts *service.P115Accounts
	share    *ShareAPI
	http     *http.Client
	proxy    *httputil.ReverseProxy
	// Injection point for protocol tests; production always uses the 115 client.
	download func(context.Context, *p115.ShareInfo, string, string, string) (string, error)
}

func RegisterEmbyProxy(admin *gin.RouterGroup, s *service.EmbyProxy) {
	admin.GET("/emby/proxy", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.JSON(200, s.Status()) })
	admin.POST("/emby/proxy", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var input service.EmbyProxyConfig
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "反代配置格式无效"})
			return
		}
		if err := s.Apply(c.Request.Context(), input); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
	})
}

func NewEmbyProxyHandler(c service.EmbyProxyConfig, accounts *service.P115Accounts, share *ShareAPI, logs ...*service.EmbyProxyLogs) http.Handler {
	h := &embyProxyHandler{config: c, accounts: accounts, share: share, download: share.Client.DownloadURLWithUA}
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
			if playbackItem(p.In.URL.Path) != "" {
				p.Out.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: h.rewritePlayback,
		// net/http's default proxy logger includes upstream URLs containing Emby tokens.
		ErrorLog:     log.New(io.Discard, "", 0),
		ErrorHandler: h.proxyError,
	}
	return h
}

type proxyRequestStartedKey struct{}

// Only fixed endpoint categories are logged; raw paths can contain tokens (e.g. signed STRM URLs).
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
	r = r.WithContext(h.logs.Trace(r.Context()))
	r = r.WithContext(context.WithValue(r.Context(), proxyRequestStartedKey{}, time.Now()))
	if r.Header.Get("X-Cinema115-Emby-Proxy") != "" {
		h.logs.Add(r.Context(), "ERROR", "检测到反代循环，已拒绝请求")
		http.Error(w, "Emby 上游指向了反代自身，请修正服务器地址", http.StatusLoopDetected)
		return
	}
	if blockedEmbyTranscode(r) {
		h.logs.Add(r.Context(), "WARNING", "已禁止转码/封装转换请求，HTTP 403；请使用支持原文件格式的客户端")
		http.Error(w, "反代已关闭转码，请使用支持原文件格式的客户端", http.StatusForbidden)
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
	if item := playbackItem(r.URL.Path); item != "" {
		h.logs.Add(r.Context(), "INFO", "读取 Emby 播放信息，条目="+item)
		if err := disablePlaybackTranscoding(r); err != nil {
			h.logs.Add(r.Context(), "ERROR", "播放协商参数无效或超出大小限制，HTTP 400")
			http.Error(w, "播放协商参数无效或过大", 400)
			return
		}
	} else if strings.Contains(strings.ToLower(r.URL.Path), "/videos/") {
		h.logs.Add(r.Context(), "DEBUG", "普通视频附属请求交由 Emby 处理（例如字幕）")
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

func (h *embyProxyHandler) embyJSON(ctx context.Context, token, path string, query url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.config.URL+"/emby"+path+"?"+query.Encode(), nil)
	if err != nil {
		return 502, fmt.Errorf("创建 Emby 校验请求失败")
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return 502, fmt.Errorf("无法读取 Emby 播放权限")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
			return resp.StatusCode, fmt.Errorf("Emby 未授权播放此媒体")
		}
		return 502, fmt.Errorf("Emby 播放查询失败")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 || json.Unmarshal(data, out) != nil {
		return 502, fmt.Errorf("Emby 播放信息无效")
	}
	return 200, nil
}

// No administrative API key is used: every redirect is authorized as the requesting Emby user.
func (h *embyProxyHandler) redirect(w http.ResponseWriter, r *http.Request, item string, fallback bool) {
	started := time.Now()
	outcome := "未完成"
	defer func() {
		h.logs.Add(r.Context(), "INFO", fmt.Sprintf("播放解析结束：%s，耗时 %d ms", outcome, time.Since(started).Milliseconds()))
	}()
	fail := func(message string, status int) {
		outcome = fmt.Sprintf("HTTP %d", status)
		h.logs.Add(r.Context(), "ERROR", outcome+"："+message)
		http.Error(w, message, status)
	}
	h.logs.Add(r.Context(), "INFO", "开始直链播放解析，方法="+r.Method+"，条目="+proxyLogID(item)+"，媒体源="+proxyLogID(proxyQuery(r.URL.Query(), "MediaSourceId"))+"，账号配置="+proxyLogID(h.config.AccountID))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	token := embyToken(r)
	if !embyProxyID.MatchString(item) || token == "" || len(token) > 2048 {
		fail("缺少有效的 Emby 播放凭据", 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	// Capture credentials once, before any I/O; configuration changes cannot switch this request's account.
	cookie := h.accounts.CookieFor(h.config.AccountID)
	var user struct {
		ID     string `json:"Id"`
		Policy struct {
			IsDisabled          bool
			EnableMediaPlayback *bool
		}
	}
	h.logs.Add(ctx, "DEBUG", "开始校验 Emby 用户身份与播放权限")
	status, err := h.embyJSON(ctx, token, "/Users/Me", nil, &user)
	if err != nil {
		fail(err.Error(), status)
		return
	}
	if !embyProxyID.MatchString(user.ID) || user.Policy.IsDisabled || user.Policy.EnableMediaPlayback != nil && !*user.Policy.EnableMediaPlayback {
		fail("Emby 用户没有播放权限", 403)
		return
	}
	var allowed struct {
		ID         string `json:"Id"`
		PlayAccess string
	}
	status, err = h.embyJSON(ctx, token, "/Users/"+user.ID+"/Items/"+item, nil, &allowed)
	if err != nil {
		fail(err.Error(), status)
		return
	}
	if allowed.ID != item || allowed.PlayAccess != "" && !strings.EqualFold(allowed.PlayAccess, "Full") {
		fail("Emby 用户无权播放此条目", http.StatusForbidden)
		return
	}
	h.logs.Add(ctx, "INFO", "Emby 用户与条目播放权限校验通过，用户="+user.ID)
	sourceID := proxyQuery(r.URL.Query(), "MediaSourceId")
	query := url.Values{"UserId": {user.ID}}
	if sourceID != "" {
		query.Set("MediaSourceId", sourceID)
	}
	var info struct {
		MediaSources []map[string]json.RawMessage
		ErrorCode    string
	}
	status, err = h.embyJSON(ctx, token, "/Items/"+item+"/PlaybackInfo", query, &info)
	if err != nil {
		fail(err.Error(), status)
		return
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
	h.logs.Add(ctx, "DEBUG", "媒体源已确定，开始读取并解析 STRM，媒体源="+proxyLogID(jsonString(source["Id"])))
	claim, err := h.sourceClaim(source)
	if errors.Is(err, errNotShareSTRM) && fallback {
		outcome = "转交 Emby"
		h.logs.Add(ctx, "WARNING", "媒体源不匹配配置的分享来源或路径映射，转交 Emby 原播放链路")
		h.proxy.ServeHTTP(w, r)
		return
	}
	if err != nil {
		h.logs.Add(ctx, "ERROR", "STRM 读取解析失败："+err.Error())
		fail("无法解析分享 STRM，请检查来源地址和媒体路径映射", 422)
		return
	}
	if cookie == "" {
		fail("302 使用的 115 账号未选择、已删除或未登录，请管理员重新配置", 503)
		return
	}
	h.logs.Add(ctx, "INFO", "STRM 解析成功，文件编号="+proxyLogID(claim.FileID))
	h.logs.Add(ctx, "INFO", "开始向 115 获取直链（使用本次固定账号和播放器 UA）")
	downloadStarted := time.Now()
	link, err := h.download(ctx, &p115.ShareInfo{Code: claim.Code, Password: claim.Password}, claim.FileID, cookie, r.UserAgent())
	if err != nil || !p115.AllowedMediaURL(link) {
		h.logs.Add(ctx, "ERROR", fmt.Sprintf("115 直链获取失败，耗时 %d ms：%s", time.Since(downloadStarted).Milliseconds(), proxyFailureReason(err)))
		fail("115 直链获取失败，请检查所选账号、分享状态及播放客户端", 502)
		return
	}
	// Empty body prevents accidentally embedding signed CDN URLs into rendered HTML or logs.
	cdn, _ := url.Parse(link)
	h.logs.Add(ctx, "INFO", fmt.Sprintf("115 直链获取成功，CDN=%s，耗时 %d ms（地址路径和签名已隐藏）", cdn.Hostname(), time.Since(downloadStarted).Milliseconds()))
	outcome = "HTTP 302"
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

func (h *embyProxyHandler) sourceClaim(source map[string]json.RawMessage) (*playClaim, error) {
	raw := jsonString(source["Path"])
	if strings.EqualFold(filepath.Ext(raw), ".strm") {
		prefix := h.config.EmbyPathPrefix
		remote := strings.ReplaceAll(raw, "\\", "/")
		if prefix == "" || !strings.HasPrefix(remote, prefix+"/") {
			return nil, errNotShareSTRM
		}
		relative := strings.TrimPrefix(remote, prefix+"/")
		if !filepath.IsLocal(filepath.FromSlash(relative)) {
			return nil, fmt.Errorf("STRM 路径越界")
		}
		root, err := filepath.EvalSymlinks(h.share.OutputRoot)
		if err != nil {
			return nil, fmt.Errorf("无法读取 STRM 根目录")
		}
		file, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return nil, fmt.Errorf("无法找到 STRM 文件")
		}
		rel, err := filepath.Rel(root, file)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, fmt.Errorf("STRM 路径越界")
		}
		f, err := os.Open(file)
		if err != nil {
			return nil, fmt.Errorf("无法读取 STRM 文件")
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			return nil, fmt.Errorf("STRM 不是普通文件")
		}
		data, err := io.ReadAll(io.LimitReader(f, 16385))
		if err != nil || len(data) > 16384 {
			return nil, fmt.Errorf("STRM 内容无效")
		}
		raw = strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return nil, errNotShareSTRM
	}
	base, _ := url.Parse(h.config.STRMBaseURL)
	if base == nil || !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
		return nil, errNotShareSTRM
	}
	if u.Path == "/api/" {
		return h.share.parseQuery(u.Query())
	}
	if strings.HasPrefix(u.Path, "/api/stream/share/") {
		return h.share.parseToken(strings.TrimPrefix(u.Path, "/api/stream/share/"))
	}
	return nil, errNotShareSTRM
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
		if _, err := h.sourceClaim(source); err != nil {
			h.logs.Add(ctx, "WARNING", sourceLabel+"："+err.Error()+"；请检查来源地址、路径映射及文件权限，保留原直放链路")
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
		h.logs.Add(ctx, "INFO", sourceLabel+"：分享 STRM 识别成功，播放入口已改写为 302 解析入口")
	}
	if rewritten == 0 {
		h.logs.Add(ctx, "INFO", "没有可改写的分享直放源；转码已禁用")
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
	return nil
}
