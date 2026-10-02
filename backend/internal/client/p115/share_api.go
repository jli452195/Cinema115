package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const PlaybackUserAgent = "Mozilla/5.0"

// The legacy /app/share/downurl endpoint can receive shared files into the
// caller's drive. Use the desktop share endpoint without falling back to it.
const sharePlaybackPath = "/os_windows/2.0/share/downurl"

const defaultShareRequestInterval = 250 * time.Millisecond

// ErrShareSnapshotPending 仅表示分享快照正在生成，不代表登录失效或分享审核中。
var ErrShareSnapshotPending = errors.New("115 正在生成文件快照")

// ShareClient 只请求固定的 115 API 地址，分享链接不作为网络请求目标。
type ShareClient struct {
	HTTP   *http.Client
	WebURL string
	AppURL string
	// 请求策略在启动时设置，开始使用后不可并发修改。
	RequestInterval     time.Duration
	ReadRetryDelays     []time.Duration
	requestGate         chan struct{}
	nextRequest         time.Time
	playbackPreparation playbackPreparationCache
	playbackReaders     playbackReadPool
	playbackSources     playbackSourceCache
}

func NewShareClient() *ShareClient {
	return &ShareClient{HTTP: &http.Client{Transport: sharedPlaybackTransport, Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, WebURL: "https://webapi.115.com", AppURL: "https://proapi.115.com", RequestInterval: defaultShareRequestInterval, ReadRetryDelays: []time.Duration{5 * time.Second, 15 * time.Second}, requestGate: make(chan struct{}, 1)}
}

type ShareFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Directory bool   `json:"directory"`
}

type SharePage struct {
	OwnerID     string      `json:"ownerId,omitempty"`
	UnderReview bool        `json:"underReview,omitempty"`
	Files       []ShareFile `json:"files"`
	Count       int         `json:"count"`
	Offset      int         `json:"offset"`
}

// 115 同时使用整数和布尔值表示请求是否成功。
func upstreamOK(value json.RawMessage) bool { return string(value) == "true" || string(value) == "1" }

func (c *ShareClient) request(ctx context.Context, method, endpoint, cookie string, form url.Values) (json.RawMessage, error) {
	raw, err := c.requestEnvelope(ctx, method, endpoint, cookie, form)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("解析 115 响应: %w", err)
	}
	return envelope.Data, nil
}

// requestEnvelope 保留目录等接口位于顶层的返回字段，避免丢失大整数 ID。
func (c *ShareClient) requestEnvelope(ctx context.Context, method, endpoint, cookie string, form url.Values) (json.RawMessage, error) {
	// 播放直链查询不与目录扫描排队，避免影响已生成 STRM 的播放启动。
	if strings.HasPrefix(endpoint, c.AppURL+sharePlaybackPath+"?") {
		return c.requestEnvelopeOnce(ctx, method, endpoint, cookie, form)
	}
	return c.metadataRequest(ctx, method, func() ([]byte, error) { return c.requestEnvelopeOnce(ctx, method, endpoint, cookie, form) })
}

func (c *ShareClient) requestEnvelopeOnce(ctx context.Context, method, endpoint, cookie string, form url.Values) (json.RawMessage, error) {
	return c.requestEnvelopeWithUA(ctx, method, endpoint, cookie, form, PlaybackUserAgent)
}

func (c *ShareClient) requestEnvelopeWithUA(ctx context.Context, method, endpoint, cookie string, form url.Values, userAgent string) (json.RawMessage, error) {
	if err := WaitRequestBudget(ctx, cookie); err != nil {
		return nil, err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("创建分享请求: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &playbackTransportError{cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		ObserveRequestBudget(ctx, resp.StatusCode, retryAfter(resp.Header.Get("Retry-After")))
		return nil, &HTTPError{Status: resp.StatusCode, Method: req.Method, Endpoint: req.URL.Host + req.URL.Path, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	var envelope shareResponse
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("读取 115 响应: %w", err)
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("115 响应过大")
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("115 返回了无法解析的分享数据")
	}
	if !upstreamOK(envelope.State) {
		if req.URL.Path == "/files/shasearch" && playbackSHAMiss(raw) {
			return raw, nil
		}
		switch req.URL.Path {
		case "/app/chrome/downurl", "/files/get_info", "/files/supervision", "/files/shasearch", "/files/search", "/files":
			return nil, driveAPIError(req.URL.Path, raw, envelope)
		}
		if req.URL.Path == sharePlaybackPath {
			return nil, shareDownloadError(envelope)
		}
		if envelope.underReview() {
			return nil, ErrShareUnderReview
		}
		if req.URL.Path == "/share/snap" && strings.TrimSpace(envelope.message()) == "正在生成文件快照" {
			return nil, ErrShareSnapshotPending
		}
		return nil, fmt.Errorf("115 拒绝请求：%s", envelope.message())
	}
	return raw, nil
}

func (c *ShareClient) List(ctx context.Context, share *ShareInfo, cid string, offset int, cookie string) (*SharePage, error) {
	return c.list(ctx, share, cid, offset, cookie, false)
}

// ListForTransfer 在同一次目录响应中检查审核与归属，不能将可读列表当作可转存许可。
func (c *ShareClient) ListForTransfer(ctx context.Context, share *ShareInfo, cid string, offset int, cookie string) (*SharePage, error) {
	return c.list(ctx, share, cid, offset, cookie, true)
}

func (c *ShareClient) list(ctx context.Context, share *ShareInfo, cid string, offset int, cookie string, forTransfer bool) (*SharePage, error) {
	if cid == "" {
		cid = "0"
	}
	if offset < 0 {
		return nil, fmt.Errorf("分页偏移不能为负数")
	}
	query := url.Values{"share_code": {share.Code}, "receive_code": {share.Password}, "cid": {cid}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}}
	response, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/share/snap?"+query.Encode(), cookie, nil)
	if err != nil {
		return nil, err
	}
	var envelope shareResponse
	if err := json.Unmarshal(response, &envelope); err != nil {
		return nil, fmt.Errorf("解析分享响应: %w", err)
	}
	page, err := parseSharePage(envelope.Data, offset)
	if page != nil {
		page.OwnerID = envelope.ownerID()
		page.UnderReview = envelope.underReview()
	}
	// 普通生成仍以列表可读为准；转存他人审核中的分享则必须在云端写入前停止。
	// 无法确认归属时不放行待审核转存，缺失审核字段本身不作为审核证据。
	if envelope.underReview() && (err != nil || forTransfer && !envelope.ownedBy(cookie)) {
		return nil, ErrShareUnderReview
	}
	return page, err
}

func parseSharePage(data json.RawMessage, offset int) (*SharePage, error) {
	var raw struct {
		Count *int `json:"count"`
		List  []struct {
			FID  json.Number `json:"fid"`
			CID  json.Number `json:"cid"`
			Name string      `json:"n"`
			Size json.Number `json:"s"`
		} `json:"list"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析分享文件列表: %w", err)
	}
	if raw.Count == nil || *raw.Count < 0 || raw.List == nil || len(raw.List) == 0 && offset < *raw.Count || len(raw.List) > 0 && int64(offset)+int64(len(raw.List)) > int64(*raw.Count) {
		return nil, fmt.Errorf("分享文件列表缺失或分页不完整")
	}
	page := &SharePage{Files: make([]ShareFile, 0, len(raw.List)), Count: *raw.Count, Offset: offset}
	seen := make(map[string]bool, len(raw.List))
	for _, f := range raw.List {
		directory := f.FID.String() == "" || f.FID.String() == "0"
		id := f.FID.String()
		if directory {
			id = f.CID.String()
		}
		if _, err := strconv.ParseUint(id, 10, 64); err != nil || id == "0" || seen[id] {
			return nil, fmt.Errorf("分享条目 ID 无效或重复")
		}
		seen[id] = true
		size, err := strconv.ParseInt(f.Size.String(), 10, 64)
		if err != nil && f.Size.String() != "" {
			return nil, fmt.Errorf("文件大小无效: %w", err)
		}
		page.Files = append(page.Files, ShareFile{ID: id, Name: f.Name, Size: size, Directory: directory})
	}
	return page, nil
}

// DownloadURL 获取有权访问的分享文件直链，不执行转存操作。
func (c *ShareClient) DownloadURL(ctx context.Context, share *ShareInfo, fileID, cookie string) (string, error) {
	return c.DownloadURLWithUA(ctx, share, fileID, cookie, PlaybackUserAgent)
}

// DownloadURLWithUA binds link acquisition to the actual player, without changing shared client state.
// DownloadInfo preserves source metadata for instant upload. Signed URLs never serialize.
type DownloadInfo struct {
	FileID          string `json:"-"` // Verified source identity; never a destination ID.
	Name            string `json:"name"`
	Size            int64  `json:"size"`
	SHA1            string `json:"sha1"`
	URL             string `json:"-"`
	loadPlaybackURL func(context.Context) (string, error)
	// Request-local resolver. Credentials, URLs and callbacks never serialize.
	ResolveRangeSHA1 func(context.Context, int64, int64) (string, error) `json:"-"`
	// Started before the target search for fresh playback material, or before
	// the first upload POST by standalone callers. Never reuse across requests.
	PrefetchRangeSource func(context.Context) func() `json:"-"`
}

func (c *ShareClient) DownloadURLWithUA(ctx context.Context, share *ShareInfo, fileID, cookie, userAgent string) (string, error) {
	info, err := c.DownloadInfoWithUA(ctx, share, fileID, cookie, userAgent)
	if err != nil {
		return "", err
	}
	return info.URL, nil
}
func (c *ShareClient) DownloadInfoWithUA(ctx context.Context, share *ShareInfo, fileID, cookie, userAgent string) (*DownloadInfo, error) {
	if share == nil || cookie == "" {
		return nil, fmt.Errorf("请先登录 115，再获取分享播放地址")
	}
	query := url.Values{"share_code": {share.Code}, "receive_code": {share.Password}, "file_id": {fileID}, "dl": {"0"}}
	if userAgent == "" {
		userAgent = PlaybackUserAgent
	}
	if len(userAgent) > 2048 || strings.ContainsAny(userAgent, "\r\n") {
		return nil, fmt.Errorf("播放器 User-Agent 无效")
	}
	raw, err := c.requestEnvelopeWithUA(ctx, http.MethodGet, c.AppURL+sharePlaybackPath+"?"+query.Encode(), cookie, nil, userAgent)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("解析直链响应: %w", err)
	}
	var result struct {
		URL  json.RawMessage `json:"url"`
		Name string          `json:"fn"`
		Size json.Number     `json:"fs"`
		SHA1 string          `json:"sha1"`
	}
	if err = json.Unmarshal(envelope.Data, &result); err != nil {
		return nil, fmt.Errorf("解析分享直链: %w", err)
	}
	var link string
	if json.Unmarshal(result.URL, &link) != nil {
		var nested struct {
			URL string `json:"url"`
		}
		if err = json.Unmarshal(result.URL, &nested); err != nil {
			return nil, fmt.Errorf("分享直链格式无效")
		}
		link = nested.URL
	}
	if !AllowedMediaURL(link) {
		return nil, fmt.Errorf("115 返回的下载域名不在允许范围内")
	}
	size, _ := result.Size.Int64()
	return &DownloadInfo{Name: result.Name, Size: size, SHA1: strings.ToUpper(result.SHA1), URL: link}, nil
}

// 只代理 115 返回的公开媒体域名，禁止将播放器接口变为任意地址代理。
func AllowedMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || (u.Port() != "" && u.Port() != "443" && u.Port() != "80") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"115.com", "115cdn.com", "115cdn.net"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
