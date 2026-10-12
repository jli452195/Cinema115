// Package woniu implements an independent client for the wn4k MacCMS site.
// Protocol reference: https://github.com/fish2018/pansou/tree/main/plugin/woniu
package woniu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const BaseURL = "https://www.wn4k.com"
const MaxPages = 4
const MaxItemsPerPage = 24
const maxResponseBytes = 8 << 20

var (
	ErrInput         = errors.New("蜗牛搜索参数无效")
	ErrNotConfigured = errors.New("请先配置蜗牛账号")
	ErrSession       = errors.New("蜗牛登录状态已失效，请重新登录")
	ErrLogin         = errors.New("蜗牛账号登录失败，请检查用户名和密码")
	ErrChallenge     = errors.New("蜗牛站要求安全验证或验证码，请在站点完成验证后重试")
	ErrRateLimited   = errors.New("蜗牛站搜索过于频繁，请稍后重试")
	ErrFormat        = errors.New("蜗牛页面格式异常，暂时无法读取")
	ErrUpstream      = errors.New("蜗牛站连接失败或访问受限，请稍后重试")
)

// PublicError never exposes upstream bodies, credentials, request URLs or paths.
func PublicError(err error) (string, string) {
	for _, entry := range []struct {
		err  error
		code string
	}{
		{ErrInput, "INVALID_INPUT"}, {ErrNotConfigured, "NOT_CONFIGURED"},
		{ErrSession, "SESSION_EXPIRED"}, {ErrLogin, "LOGIN_FAILED"}, {ErrChallenge, "CHALLENGE_REQUIRED"},
		{ErrRateLimited, "RATE_LIMITED"}, {ErrFormat, "PARSER_CHANGED"},
	} {
		if errors.Is(err, entry.err) {
			return entry.code, entry.err.Error()
		}
	}
	if errors.Is(err, context.Canceled) {
		return "CANCELLED", "蜗牛搜索已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT", "蜗牛请求超时，请稍后重试"
	}
	return "UPSTREAM_ERROR", ErrUpstream.Error()
}

type SessionCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ResourceLink struct {
	Category  string `json:"category"`
	Type      string `json:"type"`
	TypeLabel string `json:"typeLabel"`
	URL       string `json:"url"`
	Password  string `json:"password,omitempty"`
	WorkTitle string `json:"workTitle,omitempty"`
	Size      string `json:"size,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type SearchItem struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	CoverURL     string         `json:"coverUrl,omitempty"`
	Year         int            `json:"year,omitempty"`
	MediaType    string         `json:"mediaType"`
	Description  string         `json:"description,omitempty"`
	DetailStatus string         `json:"detailStatus"` // ready, empty, failed
	Links        []ResourceLink `json:"links"`
}

type SearchAttempt struct {
	Method    string `json:"method"`
	Query     string `json:"query"`
	Total     int    `json:"total"`
	Usable    int    `json:"usable"`
	Failed    int    `json:"failed,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type SearchResponse struct {
	Query         string          `json:"query"`
	MatchedBy     string          `json:"matchedBy"`
	Items         []SearchItem    `json:"items"`
	Total         int             `json:"total"` // candidates on this page, not the site's full total
	Page          int             `json:"page"`
	NextPage      int             `json:"nextPage,omitempty"`
	HasMore       bool            `json:"hasMore"`
	Truncated     bool            `json:"truncated,omitempty"`
	Partial       bool            `json:"partial,omitempty"`
	FailedDetails int             `json:"failedDetails,omitempty"`
	Attempts      []SearchAttempt `json:"attempts"`
}

type Client struct {
	base           *url.URL
	http           *http.Client
	searchGate     chan struct{}
	lastSearch     time.Time // only accessed while holding searchGate
	searchInterval time.Duration
}

// New uses a fixed HTTPS origin in production. An injected client and origin
// allow local httptest fixtures without enabling configurable upstream URLs.
func New(base string, injected *http.Client) (*Client, error) {
	if base == "" {
		base = BaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, ErrInput
	}
	if injected == nil && base != BaseURL {
		return nil, ErrInput
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("创建蜗牛会话: %w", err)
	}
	var client http.Client
	if injected != nil {
		client = *injected
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxConnsPerHost = 4
		client.Transport = transport
	}
	client.Timeout = 20 * time.Second
	client.Jar = jar
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != u.Scheme || req.URL.Host != u.Host || req.URL.User != nil {
			return ErrUpstream
		}
		return nil
	}
	return &Client{base: u, http: &client, searchGate: make(chan struct{}, 1), searchInterval: 3200 * time.Millisecond}, nil
}

func (c *Client) SnapshotSession() []SessionCookie {
	cookies := c.http.Jar.Cookies(c.base)
	result := make([]SessionCookie, 0, len(cookies))
	for _, cookie := range cookies {
		result = append(result, SessionCookie{Name: cookie.Name, Value: cookie.Value})
	}
	return result
}

func (c *Client) RestoreSession(input []SessionCookie) error {
	if len(input) > 64 {
		return ErrInput
	}
	cookies := make([]*http.Cookie, 0, len(input))
	for _, value := range input {
		cookie := &http.Cookie{Name: value.Name, Value: value.Value, Path: "/", Secure: c.base.Scheme == "https"}
		if cookie.Valid() != nil || len(value.Value) > 8192 {
			return ErrInput
		}
		cookies = append(cookies, cookie)
	}
	c.http.Jar.SetCookies(c.base, cookies)
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, form url.Values) ([]byte, *url.URL, error) {
	u := c.base.ResolveReference(&url.URL{Path: path})
	if parts, err := url.Parse(path); err == nil {
		u = c.base.ResolveReference(parts)
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host || u.User != nil {
		return nil, nil, ErrInput
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, nil, fmt.Errorf("创建蜗牛请求: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", c.base.String()+"/")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	if len(data) > maxResponseBytes {
		return nil, nil, ErrFormat
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, ErrRateLimited
	}
	if strings.Contains(string(data), "cf-chl-") || strings.Contains(string(data), "Just a moment...") {
		return nil, nil, ErrChallenge
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, ErrSession
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, ErrUpstream
	}
	return data, resp.Request.URL, nil
}

func (c *Client) Login(ctx context.Context, username, password string) error {
	if username == "" || password == "" {
		return ErrInput
	}
	data, _, err := c.request(ctx, http.MethodGet, "/user/login/", nil)
	if err != nil {
		return err
	}
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return ErrFormat
	}
	if find(doc, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "verify" }) != nil {
		return ErrChallenge
	}
	data, _, err = c.request(ctx, http.MethodPost, "/user/login.html", url.Values{"user_name": {username}, "user_pwd": {password}})
	if err != nil {
		return err
	}
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(data, &response) != nil {
		return ErrFormat
	}
	if response.Code != 1 {
		if strings.Contains(response.Msg, "验证码") {
			return ErrChallenge
		}
		return ErrLogin
	}
	if len(c.SnapshotSession()) == 0 {
		return ErrSession
	}
	return c.Check(ctx)
}

func (c *Client) Check(ctx context.Context) error {
	data, finalURL, err := c.request(ctx, http.MethodGet, "/user/index.html", nil)
	if err != nil {
		return err
	}
	if strings.HasPrefix(finalURL.Path, "/user/login") {
		return ErrSession
	}
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return ErrFormat
	}
	if find(doc, func(n *html.Node) bool { return n.Data == "a" && strings.HasPrefix(attr(n, "href"), "/user/logout") }) == nil {
		return ErrFormat
	}
	return nil
}

func (c *Client) waitSearch(ctx context.Context) (func(), error) {
	select {
	case c.searchGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-c.searchGate }
	wait := time.Until(c.lastSearch.Add(c.searchInterval))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	c.lastSearch = time.Now()
	return release, nil
}

func (c *Client) Search(ctx context.Context, query, method string, page int) (SearchResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 200 || strings.ContainsAny(query, "\r\n\x00") || page < 1 || page > MaxPages {
		return SearchResponse{}, ErrInput
	}
	release, err := c.waitSearch(ctx)
	if err != nil {
		return SearchResponse{}, err
	}
	defer release()
	path := "/vodsearch/-------------/?wd=" + url.QueryEscape(query)
	if page > 1 {
		path = "/vodsearch/" + url.PathEscape(query) + "----------" + fmt.Sprint(page) + "---/"
	}
	data, _, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return SearchResponse{}, err
	}
	result, err := parseSearch(data, c.base, query, method, page)
	if err != nil {
		return SearchResponse{}, err
	}
	if len(result.Items) == 0 {
		return result, nil
	}
	// Fixed workers and a cancellable dispatch loop: no detached goroutines,
	// no unbounded semaphore queue, and stable source order in the response.
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var failureMu sync.Mutex
	var fatal error
	var firstDetailError error
	failures := 0
	for worker := 0; worker < min(4, len(result.Items)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if workCtx.Err() != nil {
					continue
				}
				item, detailErr := c.detail(workCtx, result.Items[index])
				if detailErr == nil {
					result.Items[index] = item
					continue
				}
				result.Items[index].DetailStatus = "failed"
				failureMu.Lock()
				failures++
				if firstDetailError == nil {
					firstDetailError = detailErr
				}
				if errors.Is(detailErr, ErrSession) || errors.Is(detailErr, ErrChallenge) || errors.Is(detailErr, ErrRateLimited) {
					if fatal == nil {
						fatal = detailErr
						cancel()
					}
				}
				failureMu.Unlock()
			}
		}()
	}
dispatch:
	for i := range result.Items {
		select {
		case jobs <- i:
		case <-workCtx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return SearchResponse{}, ctx.Err()
	}
	if fatal != nil {
		return SearchResponse{}, fatal
	}
	if failures == len(result.Items) {
		if errors.Is(firstDetailError, ErrFormat) {
			return SearchResponse{}, ErrFormat
		}
		return SearchResponse{}, ErrUpstream
	}
	result.Partial, result.FailedDetails = failures > 0, failures
	usable := 0
	for _, item := range result.Items {
		if len(item.Links) > 0 {
			usable++
		}
	}
	result.Attempts[0].Usable, result.Attempts[0].Failed = usable, failures
	return result, nil
}

func (c *Client) detail(ctx context.Context, item SearchItem) (SearchItem, error) {
	data, _, err := c.request(ctx, http.MethodGet, "/voddetail/"+item.ID+"/", nil)
	if err != nil {
		return item, err
	}
	return parseDetail(data, item)
}
