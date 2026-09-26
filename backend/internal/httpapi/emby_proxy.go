package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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

func NewEmbyProxyHandler(c service.EmbyProxyConfig, accounts *service.P115Accounts, share *ShareAPI) http.Handler {
	h := &embyProxyHandler{config: c, accounts: accounts, share: share, download: share.Client.DownloadURLWithUA}
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
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "无法连接 Emby 或读取播放信息", http.StatusBadGateway)
		},
	}
	return h
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
	if r.Header.Get("X-Cinema115-Emby-Proxy") != "" {
		http.Error(w, "Emby 上游指向了反代自身，请修正服务器地址", http.StatusLoopDetected)
		return
	}
	// Some Emby clients prepend /emby when resolving relative playback URLs.
	if r.URL.Path == "/cinema115/play" || r.URL.Path == "/emby/cinema115/play" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
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
	h.proxy.ServeHTTP(w, r)
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
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	token := embyToken(r)
	if !embyProxyID.MatchString(item) || token == "" || len(token) > 2048 {
		http.Error(w, "缺少有效的 Emby 播放凭据", 401)
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
	status, err := h.embyJSON(ctx, token, "/Users/Me", nil, &user)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if !embyProxyID.MatchString(user.ID) || user.Policy.IsDisabled || user.Policy.EnableMediaPlayback != nil && !*user.Policy.EnableMediaPlayback {
		http.Error(w, "Emby 用户没有播放权限", 403)
		return
	}
	var allowed struct {
		ID         string `json:"Id"`
		PlayAccess string
	}
	status, err = h.embyJSON(ctx, token, "/Users/"+user.ID+"/Items/"+item, nil, &allowed)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if allowed.ID != item || allowed.PlayAccess != "" && !strings.EqualFold(allowed.PlayAccess, "Full") {
		http.Error(w, "Emby 用户无权播放此条目", http.StatusForbidden)
		return
	}
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
		http.Error(w, err.Error(), status)
		return
	}
	if info.ErrorCode != "" {
		http.Error(w, "Emby 拒绝播放此媒体", 403)
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
		http.Error(w, "无法确定播放版本，请重新选择媒体源", 409)
		return
	}
	claim, err := h.sourceClaim(source)
	if errors.Is(err, errNotShareSTRM) && fallback {
		h.proxy.ServeHTTP(w, r)
		return
	}
	if err != nil {
		http.Error(w, "无法解析分享 STRM，请检查来源地址和媒体路径映射", 422)
		return
	}
	if cookie == "" {
		http.Error(w, "302 使用的 115 账号未选择、已删除或未登录，请管理员重新配置", 503)
		return
	}
	link, err := h.download(ctx, &p115.ShareInfo{Code: claim.Code, Password: claim.Password}, claim.FileID, cookie, r.UserAgent())
	if err != nil || !p115.AllowedMediaURL(link) {
		http.Error(w, "115 直链获取失败，请检查所选账号、分享状态及播放客户端", 502)
		return
	}
	// Empty body prevents accidentally embedding signed CDN URLs into rendered HTML or logs.
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
	if item == "" || token == "" || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return fmt.Errorf("Emby 播放信息读取失败")
	}
	var info map[string]json.RawMessage
	var sources []map[string]json.RawMessage
	if json.Unmarshal(data, &info) != nil || json.Unmarshal(info["MediaSources"], &sources) != nil {
		return fmt.Errorf("Emby 播放信息格式无效")
	}
	changed := false
	for _, source := range sources {
		// DirectStream can mean remuxing or audio conversion; only true direct play may bypass Emby.
		if !jsonTrue(source["SupportsDirectPlay"]) {
			continue
		}
		if _, err := h.sourceClaim(source); err != nil {
			continue
		}
		id := jsonString(source["Id"])
		if id == "" {
			continue
		}
		query := url.Values{"ItemId": {item}, "MediaSourceId": {id}, "api_key": {token}}
		source["DirectStreamUrl"], _ = json.Marshal("/cinema115/play?" + query.Encode())
		// Clients must enter our authenticated endpoint instead of opening the original STRM URL.
		source["SupportsDirectPlay"] = json.RawMessage("false")
		source["SupportsDirectStream"] = json.RawMessage("true")
		changed = true
	}
	if changed {
		info["MediaSources"], err = json.Marshal(sources)
		if err != nil {
			return err
		}
		data, err = json.Marshal(info)
		if err != nil {
			return err
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	resp.ContentLength = int64(len(data))
	resp.Header.Set("Content-Length", strconv.Itoa(len(data)))
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Del("ETag")
	return nil
}
