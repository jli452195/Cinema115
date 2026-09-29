package nanshare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInput       = errors.New("NanShare 配置或请求参数无效")
	ErrAuth        = errors.New("NanShare 登录失败或会话已过期，请重新登录")
	ErrUnavailable = errors.New("无法连接 NanShare，请检查地址和网络")
	ErrFormat      = errors.New("NanShare 响应格式不兼容")
	ErrChanged     = errors.New("NanShare 配置已变化，请刷新搜索并重新选择")
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
	renew   func(context.Context, string) (string, error)
	renewed bool
}

// WithRenewal is configured before use. A client belongs to one operation;
// renewal is attempted at most once, only for read-only endpoints.
func (c *Client) WithRenewal(renew func(context.Context, string) (string, error)) *Client {
	c.renew = renew
	return c
}

// LAN addresses are intentional: NanShare is a user-configured NAS service.
func NormalizeBaseURL(value string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("%w：请输入无路径的 HTTP(S) 站点地址", ErrInput)
	}
	u.Path = ""
	return u.String(), nil
}

func New(baseURL, token string) (*Client, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if len(token) > 16384 || strings.ContainsAny(token, " \r\n\t\x00") {
		return nil, fmt.Errorf("%w：会话令牌格式不正确", ErrInput)
	}
	return &Client{baseURL: base, token: token, http: &http.Client{
		Timeout: 30 * time.Second,
		// Never forward login bodies or bearer tokens to a redirect target.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type envelope struct {
	OK      *bool  `json:"ok"`
	Success *bool  `json:"success"`
	Code    string `json:"code"`
}

func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	err := c.requestOnce(ctx, method, path, input, output)
	readOnly := method == http.MethodGet || path == "/discover/resource/hdhive"
	if !errors.Is(err, ErrAuth) || !readOnly || c.renew == nil || c.renewed {
		return err
	}
	c.renewed = true
	token, err := c.renew(ctx, c.token)
	if err != nil {
		return err
	}
	c.token = token
	return c.requestOnce(ctx, method, path, input, output)
}

func (c *Client) requestOnce(ctx context.Context, method, path string, input, output any) error {
	var reader io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("编码 NanShare 请求: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api"+path, reader)
	if err != nil {
		return ErrInput
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return ErrAuth
	}
	transferRejection := path == "/discover/resource/transfer" && (resp.StatusCode == 400 || resp.StatusCode == 409 || resp.StatusCode == 422)
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && !transferRejection {
		return fmt.Errorf("NanShare 接口 %s 返回 HTTP %d", path, resp.StatusCode)
	}
	const limit = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	if len(data) > limit {
		return ErrFormat
	}
	var state envelope
	if json.Unmarshal(data, &state) != nil {
		return ErrFormat
	}
	if state.Code == "auth_required" || state.Code == "NANSHARE_AUTH_REQUIRED" {
		return ErrAuth
	}
	// Transfer business failures are receipts too. Parse only locally mapped
	// diagnostics; never forward upstream URLs, credentials or arbitrary text.
	if path == "/discover/resource/transfer" {
		if transferRejection && (state.Success == nil || *state.Success) {
			return ErrFormat
		}
		result, ok := output.(*TransferResult)
		if !ok {
			return ErrFormat
		}
		return decodeTransferResult(data, result)
	}
	if (state.OK != nil && !*state.OK) || (state.Success != nil && !*state.Success) {
		if path == "/session/login" {
			return ErrAuth
		}
		return fmt.Errorf("NanShare 接口 %s 执行失败，请检查远端配置", path)
	}
	if state.OK == nil && state.Success == nil {
		return ErrFormat
	}
	// Raw upstream bodies/messages may contain credentials; never return them.
	if json.Unmarshal(data, output) != nil {
		return ErrFormat
	}
	return nil
}

func (c *Client) Login(ctx context.Context, username, password string) (string, string, error) {
	var response struct {
		Data struct {
			Token    string `json:"token"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, "/session/login", map[string]string{"username": username, "password": password}, &response); err != nil {
		return "", "", err
	}
	if response.Data.Token == "" || strings.TrimSpace(response.Data.Username) == "" {
		return "", "", ErrFormat
	}
	return response.Data.Token, response.Data.Username, nil
}

func (c *Client) Session(ctx context.Context) (string, error) {
	var response struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/session", nil, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Data.Username) == "" {
		return "", ErrFormat
	}
	return response.Data.Username, nil
}

func (c *Client) RE0Account(ctx context.Context) (string, error) {
	var response struct {
		Account *string `json:"hdhive_account"`
	}
	if err := c.request(ctx, http.MethodGet, "/discover/resource/options", nil, &response); err != nil {
		return "", err
	}
	if response.Account == nil {
		return "", ErrFormat
	}
	return strings.TrimSpace(*response.Account), nil
}

type ShareConfig struct {
	Enabled *bool `json:"enabled"`
	Normal  *bool `json:"enable_normal_share_mode"`
	Reshare *bool `json:"enable_reshare_mode"`
	Folders []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"strm_folders"`
	AccountID     string `json:"use_pan115_config"`
	OpenAccountID string `json:"reshare_pan115_open_config"`
}

func (c *Client) ShareConfig(ctx context.Context) (ShareConfig, error) {
	var response struct {
		Data ShareConfig `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/strm/share/config", nil, &response); err != nil {
		return ShareConfig{}, err
	}
	d := response.Data
	if d.Enabled == nil || d.Normal == nil || d.Reshare == nil || d.Folders == nil {
		return ShareConfig{}, ErrFormat
	}
	return d, nil
}

type SearchResult struct {
	Total int `json:"total"`
	P115  int `json:"p115"`
}

// SearchTest only lists resources; it never unlocks resources or submits jobs.
func (c *Client) SearchTest(ctx context.Context, account, kind string, id int64) (SearchResult, error) {
	if account == "" || (kind != "movie" && kind != "tv") || id < 1 || id > 9007199254740991 {
		return SearchResult{}, ErrInput
	}
	var response struct {
		Resources []struct {
			Source  string `json:"source"`
			Key     string `json:"key"`
			PanType string `json:"pan_type"`
		} `json:"resources"`
	}
	if err := c.request(ctx, http.MethodPost, "/discover/resource/hdhive", map[string]any{"tmdb_id": id, "media_type": kind, "account_name": account}, &response); err != nil {
		return SearchResult{}, err
	}
	if response.Resources == nil {
		return SearchResult{}, ErrFormat
	}
	result := SearchResult{Total: len(response.Resources)}
	for _, item := range response.Resources {
		if item.Source != "hdhive" || item.Key == "" || item.PanType == "" {
			return SearchResult{}, ErrFormat
		}
		if item.PanType == "115" {
			result.P115++
		}
	}
	return result, nil
}
