package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AccountClient 查询 Cookie 对应的真实登录状态。
type AccountClient struct {
	HTTP       *http.Client
	StatusURL  string
	DevicesURL string
	InfoURL    string
	SpaceURL   string
	summaryMu  sync.Mutex
	summaries  map[[32]byte]*summaryEntry
}

func NewAccountClient() *AccountClient {
	return &AccountClient{
		HTTP:       &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		StatusURL:  "https://my.115.com/?ct=guide&ac=status",
		DevicesURL: "https://qrcodeapi.115.com/app/1.0/web/1.0/login_log/login_devices",
		InfoURL:    "https://my.115.com/?ct=ajax&ac=nav",
		SpaceURL:   "https://webapi.115.com/files/index_info?count_space_nums=1",
	}
}

func (a *AccountClient) CheckCookie(ctx context.Context, cookie string) (bool, error) {
	ctx = context.WithValue(ctx, budgetStageKey{}, "credential_check")
	if err := WaitRequestBudget(ctx, cookie); err != nil {
		return false, err
	}
	// 避免空值和粘贴的多行请求头被当成有效凭证提交。
	if strings.TrimSpace(cookie) == "" || strings.ContainsAny(cookie, "\r\n") {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.StatusURL, nil)
	if err != nil {
		return false, fmt.Errorf("创建登录状态请求: %w", err)
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", PlaybackUserAgent)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return false, fmt.Errorf("查询登录状态: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		ObserveRequestBudget(ctx, resp.StatusCode, retryAfter(resp.Header.Get("Retry-After")))
		return false, &HTTPError{Status: resp.StatusCode, Method: req.Method, Endpoint: req.URL.Host + req.URL.Path, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	var result struct {
		State json.RawMessage `json:"state"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return false, fmt.Errorf("解析登录状态: %w", err)
	}
	switch string(result.State) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("登录状态响应缺少有效的 state")
	}
}
