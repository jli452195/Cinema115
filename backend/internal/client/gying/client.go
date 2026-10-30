package gying

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/netproxy"
)

var (
	ErrInput         = errors.New("观影搜索参数无效")
	ErrLogin         = errors.New("观影账号登录失败")
	ErrForbidden     = errors.New("观影站登录状态已失效")
	ErrChallenge     = errors.New("观影站安全验证失败")
	ErrNotConfigured = errors.New("观影站尚未配置")
	ErrDetailFormat  = errors.New("观影资源详情格式异常，暂时无法读取，请查看观影日志或稍后重试")
)

const (
	maxResponseBytes = 8 << 20
	maxSearchItems   = 100
	maxPoWIterations = 5_000_000
	maxNonce         = 5_000_000
	maxChallengeBody = 1 << 20
)

// Client is an independent client for the Gying-compatible site. It does not
// depend on PanSou or expose the site's cookies to callers.
type Client struct {
	baseURL     *url.URL
	http        *http.Client
	mu          sync.Mutex
	challengeMu sync.Mutex
}

type SessionCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func New(baseURL string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, ErrNotConfigured
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: 站点地址无效", ErrInput)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("创建观影会话失败: %w", err)
	}
	if httpClient == nil {
		httpClient = &http.Client{Transport: netproxy.Transport("gying"), Timeout: 45 * time.Second}
	} else {
		copy := *httpClient
		httpClient = &copy
		if httpClient.Timeout == 0 {
			httpClient.Timeout = 45 * time.Second
		}
	}
	httpClient.Jar = jar
	return &Client{baseURL: u, http: httpClient}, nil
}

type SearchItem struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	CoverURL    string         `json:"coverUrl,omitempty"`
	Year        int            `json:"year,omitempty"`
	MediaType   string         `json:"mediaType"`
	Description string         `json:"description,omitempty"`
	Links       []ResourceLink `json:"links"`
}

type ResourceLink struct {
	Category     string `json:"category"` // pan or torrent
	Type         string `json:"type"`
	TypeLabel    string `json:"typeLabel"`
	SubType      string `json:"subType,omitempty"`
	SubTypeLabel string `json:"subTypeLabel,omitempty"`
	URL          string `json:"url"`
	Password     string `json:"password,omitempty"`
	WorkTitle    string `json:"workTitle,omitempty"`
	Size         string `json:"size,omitempty"`
	Seeds        *int   `json:"seeds,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	HasTorrent   bool   `json:"hasTorrent,omitempty"`
	TorrentPath  string `json:"-"` // Short-lived, session-bound URL; never expose or persist.
}

type SearchResponse struct {
	Query              string          `json:"query"`
	MatchedBy          string          `json:"matchedBy,omitempty"`
	Items              []SearchItem    `json:"items"`
	Total              int             `json:"total"`
	Truncated          bool            `json:"truncated,omitempty"`
	Attempts           []SearchAttempt `json:"attempts"`
	Partial            bool            `json:"partial,omitempty"`
	FailedDetails      int             `json:"failedDetails,omitempty"`
	UnavailableDetails int             `json:"unavailableDetails,omitempty"`
}

type SearchAttempt struct {
	Method      string `json:"method"`
	Query       string `json:"query"`
	Total       int    `json:"total"`
	Usable      int    `json:"usable"`
	Failed      int    `json:"failed,omitempty"`
	Unavailable int    `json:"unavailable,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type searchData struct {
	Q string `json:"q"`
	N string `json:"n"`
	L struct {
		Title  []string `json:"title"`
		Year   []int    `json:"year"`
		Kind   []string `json:"d"`
		ID     []string `json:"i"`
		Info   []string `json:"info"`
		Daoyan []string `json:"daoyan"`
		Zhuyan []string `json:"zhuyan"`
	} `json:"l"`
}

type detailData struct {
	Code     int          `json:"code"`
	Downlist downlistData `json:"downlist"`
	Panlist  panlistData  `json:"panlist"`
}

type downlistData struct {
	Hex  string `json:"hex"`
	Type struct {
		Labels []string `json:"a"`
		Codes  []string `json:"b"`
	} `json:"type"`
	List struct {
		Hash          []string   `json:"m"`
		ID            []string   `json:"u"`
		Kind          []int      `json:"k"`
		Title         []string   `json:"t"`
		Size          []string   `json:"s"`
		Seeds         seedCounts `json:"e"`
		CategoryCodes []string   `json:"p"`
		Updated       []string   `json:"n"`
	} `json:"list"`
}

type panlistData struct {
	ID    []string `json:"id"`
	Name  []string `json:"name"`
	Pass  []string `json:"p"`
	URL   []string `json:"url"`
	Type  []int    `json:"type"`
	Time  []string `json:"time"`
	TName []string `json:"tname"`
}

func (value *downlistData) UnmarshalJSON(data []byte) error {
	type plainDownlistData downlistData
	return decodeDetailObject(data, (*plainDownlistData)(value), "downlist")
}

func (value *panlistData) UnmarshalJSON(data []byte) error {
	type plainPanlistData panlistData
	return decodeDetailObject(data, (*plainPanlistData)(value), "panlist")
}

// Some detail responses use [] instead of an object when a resource group is
// empty. Accept only an empty array so a future non-empty array shape cannot be
// silently discarded.
func decodeDetailObject[T any](data []byte, target *T, field string) error {
	trimmed := bytes.TrimSpace(data)
	var zero T
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*target = zero
		return nil
	case len(trimmed) > 0 && trimmed[0] == '{':
		var decoded T
		if err := json.Unmarshal(trimmed, &decoded); err != nil {
			return fmt.Errorf("detail field %s: %w", field, err)
		}
		*target = decoded
		return nil
	case len(trimmed) > 0 && trimmed[0] == '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return fmt.Errorf("detail field %s: %w", field, err)
		}
		if len(items) != 0 {
			return fmt.Errorf("detail field %s: non-empty array is unsupported", field)
		}
		*target = zero
		return nil
	default:
		return fmt.Errorf("detail field %s: expected object, null, or empty array", field)
	}
}

var panTypes = map[int]string{
	0: "xunlei", 1: "baidu", 2: "quark", 3: "tianyi", 4: "mobile",
	5: "115", 6: "123", 7: "uc", 8: "aliyun",
}

var panTypeLabels = map[string]string{
	"xunlei": "迅雷", "baidu": "百度", "quark": "夸克", "tianyi": "天翼",
	"mobile": "移动云盘", "115": "115", "123": "123", "guangya": "光鸭网盘", "uc": "UC", "aliyun": "阿里云盘",
}

var (
	webLinkPattern    = regexp.MustCompile(`https?://[^\s<>"'（）()]+`)
	passwordPattern   = regexp.MustCompile(`(?i)(?:[?&](?:pwd|password)=|访问码\s*[:：]?\s*|提取码\s*[:：]?\s*|密码\s*[:：]?\s*)([a-zA-Z0-9]{4,8})`)
	magnetHashPattern = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)
)

// SearchByTitle tries the original title first and only falls back to an IMDb
// identifier when the title query produced no usable resource item.
func (c *Client) SearchByTitle(ctx context.Context, originalTitle, imdbID string) (SearchResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	originalTitle = strings.TrimSpace(originalTitle)
	imdbID = strings.TrimSpace(imdbID)
	if originalTitle == "" && imdbID == "" {
		return SearchResponse{}, ErrInput
	}
	if originalTitle != "" {
		reportProgress(ctx, "DEBUG", "开始按影视原名查询")
		result, err := c.search(ctx, originalTitle)
		if err != nil {
			return SearchResponse{}, err
		}
		result.Attempts[0].Method = "title"
		result.MatchedBy = "title"
		if len(result.Items) > 0 || imdbID == "" {
			return result, nil
		}
		titleAttempt := result.Attempts[0]
		if !validIMDbID(imdbID) {
			return result, nil
		}
		reportProgress(ctx, "DEBUG", "原名搜索未发现可用资源，开始 IMDb 兜底查询")
		result, err = c.search(ctx, imdbID)
		if err != nil {
			return SearchResponse{}, err
		}
		result.Attempts[0].Method = "imdb"
		result.Attempts = append([]SearchAttempt{titleAttempt}, result.Attempts...)
		result.Partial = titleAttempt.Failed > 0 || result.Partial
		result.FailedDetails += titleAttempt.Failed
		result.UnavailableDetails += titleAttempt.Unavailable
		result.MatchedBy = "imdb"
		return result, nil
	}
	if !validIMDbID(imdbID) {
		return SearchResponse{Query: originalTitle, MatchedBy: "title", Items: []SearchItem{}, Attempts: []SearchAttempt{}}, nil
	}
	reportProgress(ctx, "DEBUG", "缺少原名，使用 IMDb 编号查询")
	result, err := c.search(ctx, imdbID)
	if err != nil {
		return SearchResponse{}, err
	}
	result.Attempts[0].Method = "imdb"
	result.MatchedBy = "imdb"
	return result, nil
}

func validIMDbID(value string) bool {
	if len(value) < 3 || len(value) > 20 || !strings.HasPrefix(strings.ToLower(value), "tt") {
		return false
	}
	for _, char := range value[2:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (c *Client) RestoreSession(cookies []SessionCookie) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	values := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" || strings.ContainsAny(cookie.Name, "\r\n;=") || strings.ContainsAny(cookie.Value, "\r\n;") {
			return fmt.Errorf("%w: 会话 Cookie 无效", ErrInput)
		}
		values = append(values, &http.Cookie{Name: cookie.Name, Value: cookie.Value, Path: "/", Secure: c.baseURL.Scheme == "https", HttpOnly: true})
	}
	c.http.Jar.SetCookies(c.baseURL, values)
	return nil
}

func (c *Client) SnapshotSession() []SessionCookie {
	c.mu.Lock()
	defer c.mu.Unlock()
	cookies := c.http.Jar.Cookies(c.baseURL)
	result := make([]SessionCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name != "" {
			result = append(result, SessionCookie{Name: cookie.Name, Value: cookie.Value})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (c *Client) Check(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, status, _, err := c.requestWithChallengeRetry(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	if err := responseError(body, status, false); err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("检查观影会话失败: HTTP %d", status)
	}
	return nil
}

// Login performs the three requests used by the site: establish a session,
// submit the form, and warm up a detail page so anti-bot cookies are issued.
func (c *Client) Login(ctx context.Context, username, password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.login(ctx, username, password)
}

func (c *Client) login(ctx context.Context, username, password string) error {
	username, password = strings.TrimSpace(username), strings.TrimSpace(password)
	if username == "" || password == "" {
		return ErrInput
	}
	if _, status, _, err := c.requestWithChallengeRetry(ctx, http.MethodGet, "/", nil); err != nil {
		return fmt.Errorf("访问登录页: %w", err)
	} else if status != http.StatusOK {
		return fmt.Errorf("访问登录页: HTTP %d", status)
	}
	form := url.Values{
		"code": {""}, "siteid": {"1"}, "dosubmit": {"1"},
		"cookietime": {"10506240"}, "username": {username}, "password": {password},
	}
	body, status, _, err := c.requestWithChallengeRetry(ctx, http.MethodPost, "/user/login", form)
	if err != nil {
		return fmt.Errorf("提交登录: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("登录失败: HTTP %d", status)
	}
	var result struct {
		Code json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("%w: %w", ErrLoginFormat, err)
	}
	code, err := strconv.Atoi(strings.Trim(string(result.Code), `"`))
	if err != nil || code != http.StatusOK {
		return ErrLogin
	}
	if _, _, _, err := c.requestWithChallengeRetry(ctx, http.MethodGet, "/mv/wkMn", nil); err != nil {
		return fmt.Errorf("登录会话预热: %w", err)
	}
	return nil
}

// Search fetches every valid item returned by one site query. It deliberately
// does not filter item titles by the query, because IMDb searches return normal
// titles rather than strings containing the tt identifier.
func (c *Client) Search(ctx context.Context, query string) (SearchResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.search(ctx, query)
}

func (c *Client) search(ctx context.Context, query string) (SearchResponse, error) {
	started := time.Now()
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 120 {
		return SearchResponse{}, ErrInput
	}
	values := url.Values{"q": {query}, "type": {"0"}, "mode": {"2"}}
	body, status, _, err := c.requestWithChallengeRetry(ctx, http.MethodGet, "/search?"+values.Encode(), nil)
	if err != nil {
		return SearchResponse{}, err
	}
	if err := responseError(body, status, false); err != nil {
		return SearchResponse{}, err
	}
	if status != http.StatusOK {
		return SearchResponse{}, fmt.Errorf("搜索失败: HTTP %d", status)
	}
	data, err := parseSearchData(body)
	if err != nil {
		return SearchResponse{}, err
	}
	count := len(data.L.ID)
	reportProgress(ctx, "DEBUG", fmt.Sprintf("查询返回 %d 个影视条目，开始读取资源详情", count))
	total := count
	truncated := false
	if count > maxSearchItems {
		count = maxSearchItems
		truncated = true
	}
	results := make([]*SearchItem, count)
	jobs := make(chan int)
	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eligible := 0
	for i := 0; i < count; i++ {
		if searchItemEligible(data, i) {
			eligible++
		}
	}
	var fatalError error
	var firstDetailError error
	detailErrors := 0
	unavailableDetails := 0
	var resultMu sync.Mutex
	workers := 6
	if count < workers {
		workers = count
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				item, err := c.fetchSearchItem(searchCtx, data, i)
				if errors.Is(err, ErrResourceUnavailable) {
					resultMu.Lock()
					unavailableDetails++
					resultMu.Unlock()
					// 只记录本轮条目序号，不透传上游说明或链接。
					reportProgress(ctx, "WARNING", fmt.Sprintf("跳过本轮第 %d 个条目：资源已下架或不可用，不重新登录", i+1))
					continue
				}
				if errors.Is(err, ErrForbidden) {
					resultMu.Lock()
					if fatalError == nil {
						fatalError = err
						cancel()
					}
					resultMu.Unlock()
					continue
				}
				if err != nil {
					resultMu.Lock()
					detailErrors++
					if firstDetailError == nil {
						firstDetailError = err
					}
					resultMu.Unlock()
					continue
				}
				if err == nil && item != nil {
					results[i] = item
				}
			}
		}()
	}
sendJobs:
	for i := 0; i < count; i++ {
		select {
		case jobs <- i:
		case <-searchCtx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()
	if fatalError != nil {
		return SearchResponse{}, fatalError
	}
	if err := searchCtx.Err(); err != nil {
		return SearchResponse{}, err
	}
	if eligible > 0 && detailErrors == eligible {
		return SearchResponse{}, fmt.Errorf("读取观影资源详情失败: %w", firstDetailError)
	}
	items := make([]SearchItem, 0, count)
	for _, item := range results {
		if item != nil {
			items = append(items, *item)
		}
	}
	attempt := SearchAttempt{Query: query, Total: total, Usable: len(items), Failed: detailErrors, Unavailable: unavailableDetails, Truncated: truncated}
	reportProgress(ctx, "DEBUG", fmt.Sprintf("本轮查询与详情读取完成：%d 个可用条目、%d 个详情失败，耗时 %.2f 秒", len(items), detailErrors, time.Since(started).Seconds()))
	return SearchResponse{
		Query: query, Items: items, Total: total, Truncated: truncated,
		Attempts: []SearchAttempt{attempt}, Partial: detailErrors > 0, FailedDetails: detailErrors,
		UnavailableDetails: unavailableDetails,
	}, nil
}

func searchItemEligible(data searchData, i int) bool {
	return i < len(data.L.Title) && i < len(data.L.Kind) && strings.TrimSpace(data.L.ID[i]) != ""
}

func (c *Client) fetchSearchItem(ctx context.Context, data searchData, i int) (*SearchItem, error) {
	if !searchItemEligible(data, i) {
		return nil, nil
	}
	detail, err := c.fetchDetail(ctx, data.L.Kind[i], data.L.ID[i])
	if err != nil {
		return nil, err
	}
	links := detailLinks(detail)
	if len(links) == 0 {
		return nil, nil
	}
	content := make([]string, 0, 3)
	if i < len(data.L.Info) && strings.TrimSpace(data.L.Info[i]) != "" {
		content = append(content, strings.TrimSpace(data.L.Info[i]))
	}
	if i < len(data.L.Daoyan) && strings.TrimSpace(data.L.Daoyan[i]) != "" {
		content = append(content, "导演: "+strings.TrimSpace(data.L.Daoyan[i]))
	}
	if i < len(data.L.Zhuyan) && strings.TrimSpace(data.L.Zhuyan[i]) != "" {
		content = append(content, "主演: "+strings.TrimSpace(data.L.Zhuyan[i]))
	}
	year := 0
	if i < len(data.L.Year) {
		year = data.L.Year[i]
	}
	return &SearchItem{ID: data.L.Kind[i] + ":" + data.L.ID[i], Title: strings.TrimSpace(data.L.Title[i]), CoverURL: imagecache.GyingCoverURL(data.L.Kind[i], data.L.ID[i]), Year: year, MediaType: mediaType(data.L.Kind[i]), Description: strings.Join(content, " | "), Links: links}, nil
}

func mediaType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mv", "movie":
		return "movie"
	case "ac", "anime":
		return "anime"
	case "tv", "series":
		return "tv"
	default:
		return "other"
	}
}

func (c *Client) fetchDetail(ctx context.Context, kind, id string) (detailData, error) {
	path := "/res/downurl/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
	body, status, _, err := c.requestWithChallengeRetry(ctx, http.MethodGet, path, nil)
	if err != nil {
		return detailData{}, err
	}
	if err := responseError(body, status, true); err != nil {
		return detailData{}, err
	}
	if status != http.StatusOK {
		return detailData{}, fmt.Errorf("详情请求失败: HTTP %d", status)
	}
	var detail detailData
	if err := json.Unmarshal(body, &detail); err != nil {
		return detailData{}, fmt.Errorf("%w: %w", ErrDetailFormat, err)
	}
	unknownSeeds := 0
	for i, count := range detail.Downlist.List.Seeds {
		if i < len(detail.Downlist.List.Hash) && count < 0 {
			unknownSeeds++
		}
	}
	if unknownSeeds > 0 {
		// 只记录字段名和数量，不记录响应原文或字段内容；每个详情最多一条汇总。
		reportProgress(ctx, "DEBUG", fmt.Sprintf("资源详情 downlist.list.e 有 %d 项做种数未知，已保留对应资源并省略做种数显示", unknownSeeds))
	}
	return detail, nil
}

func detailLinks(detail detailData) []ResourceLink {
	links := make([]ResourceLink, 0, len(detail.Panlist.URL)+len(detail.Downlist.List.Hash))
	seen := make(map[string]struct{})
	for i, raw := range detail.Panlist.URL {
		linkURL := extractPanURL(raw)
		if linkURL == "" {
			continue
		}
		kind := classifyPanURL(linkURL)
		if kind == "other" && i < len(detail.Panlist.Type) {
			if mapped, ok := panTypes[detail.Panlist.Type[i]]; ok {
				kind = mapped
			}
		}
		if kind == "other" && i < len(detail.Panlist.Type) {
			code := detail.Panlist.Type[i]
			if code >= 0 && code < len(detail.Panlist.TName) {
				kind = classifyPanName(detail.Panlist.TName[code])
			}
		}
		password := extractPassword(raw, "")
		if i < len(detail.Panlist.Pass) {
			password = extractPassword(raw, detail.Panlist.Pass[i])
		}
		key := strings.ToLower(linkURL) + "|" + strings.ToLower(password)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		label := "其他网盘"
		if value, ok := panTypeLabels[kind]; ok {
			label = value
		}
		link := ResourceLink{Category: "pan", Type: kind, TypeLabel: label, URL: linkURL, Password: password}
		if i < len(detail.Panlist.Name) {
			link.WorkTitle = strings.TrimSpace(detail.Panlist.Name[i])
		}
		if i < len(detail.Panlist.Time) {
			link.UpdatedAt = strings.TrimSpace(detail.Panlist.Time[i])
		}
		links = append(links, link)
	}
	for i, hash := range detail.Downlist.List.Hash {
		hash = strings.ToLower(strings.TrimSpace(hash))
		if !magnetHashPattern.MatchString(hash) {
			continue
		}
		key := "magnet:" + strings.ToLower(hash)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		link := ResourceLink{Category: "torrent", Type: "magnet", TypeLabel: "磁力", SubTypeLabel: "未知", URL: "magnet:?xt=urn:btih:" + hash}
		if i < len(detail.Downlist.List.ID) && i < len(detail.Downlist.List.Kind) && detail.Downlist.List.Kind[i] == 0 {
			candidate := "/dbt/" + detail.Downlist.List.ID[i] + "/" + detail.Downlist.Hex
			if torrentPathPattern.MatchString(candidate) {
				link.HasTorrent, link.TorrentPath = true, candidate
			}
		}
		// list.p contains category codes matching type.b; list.k is not a
		// category index. The parallel type.a array provides display labels.
		if i < len(detail.Downlist.List.CategoryCodes) {
			code := strings.TrimSpace(detail.Downlist.List.CategoryCodes[i])
			link.SubType = code
			for typeIndex, knownCode := range detail.Downlist.Type.Codes {
				if code != "" && code == strings.TrimSpace(knownCode) && typeIndex < len(detail.Downlist.Type.Labels) {
					link.SubTypeLabel = strings.TrimSpace(detail.Downlist.Type.Labels[typeIndex])
					break
				}
			}
			if link.SubTypeLabel == "" {
				link.SubTypeLabel = "未知"
			}
		}
		if i < len(detail.Downlist.List.Title) {
			link.WorkTitle = strings.TrimSpace(detail.Downlist.List.Title[i])
			if link.WorkTitle != "" {
				link.URL += "&dn=" + url.QueryEscape(link.WorkTitle)
			}
		}
		if i < len(detail.Downlist.List.Size) {
			link.Size = strings.TrimSpace(detail.Downlist.List.Size[i])
		}
		if i < len(detail.Downlist.List.Seeds) && detail.Downlist.List.Seeds[i] >= 0 {
			seeds := detail.Downlist.List.Seeds[i]
			link.Seeds = &seeds
		}
		if i < len(detail.Downlist.List.Updated) {
			link.UpdatedAt = strings.TrimSpace(detail.Downlist.List.Updated[i])
		}
		links = append(links, link)
	}
	return links
}

func extractPanURL(raw string) string {
	match := webLinkPattern.FindString(strings.TrimSpace(raw))
	match = strings.TrimRight(match, ".。!！,，;；]】")
	u, err := url.Parse(match)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

func classifyPanURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "other"
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "pan.quark.cn":
		return "quark"
	case host == "drive.uc.cn":
		return "uc"
	case host == "pan.baidu.com":
		return "baidu"
	case hostMatches(host, "alipan.com", "aliyundrive.com"):
		return "aliyun"
	case host == "pan.xunlei.com":
		return "xunlei"
	case host == "cloud.189.cn" || hostMatches(host, "tianyi.cloud"):
		return "tianyi"
	case host == "yun.139.com" || host == "caiyun.139.com" || host == "caiyun.feixin.10086.cn":
		return "mobile"
	case hostMatches(host, "115.com", "115cdn.com", "anxia.com"):
		return "115"
	case hostMatches(host, "123684.com", "123865.com", "123pan.com", "123pan.cn"):
		return "123"
	case hostMatches(host, "guangyapan.com"):
		return "guangya"
	default:
		return "other"
	}
}

func hostMatches(host string, roots ...string) bool {
	for _, root := range roots {
		if host == root || strings.HasSuffix(host, "."+root) {
			return true
		}
	}
	return false
}

func classifyPanName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	for needle, kind := range map[string]string{"天翼": "tianyi", "移动": "mobile", "彩云": "mobile", "百度": "baidu", "夸克": "quark", "迅雷": "xunlei", "阿里": "aliyun", "115": "115", "123": "123", "uc": "uc"} {
		if strings.Contains(value, needle) {
			return kind
		}
	}
	return "other"
}

func extractPassword(raw, fallback string) string {
	for _, value := range []string{raw, fallback} {
		if match := passwordPattern.FindStringSubmatch(value); len(match) > 1 {
			return match[1]
		}
		value = strings.Trim(strings.TrimSpace(value), ".。!！,，;；:：#* ")
		if len(value) >= 4 && len(value) <= 8 {
			valid := true
			for _, char := range value {
				if char < '0' || char > '9' && char < 'A' || char > 'Z' && char < 'a' || char > 'z' {
					valid = false
					break
				}
			}
			if valid {
				return value
			}
		}
	}
	return ""
}

func (c *Client) requestWithChallengeRetry(ctx context.Context, method, path string, form url.Values) ([]byte, int, http.Header, error) {
	for attempt := 0; attempt < 2; attempt++ {
		body, status, headers, err := c.do(ctx, method, path, form)
		if err != nil {
			return nil, status, headers, err
		}
		if isBotChallengePage(body) {
			if attempt > 0 {
				reportProgress(ctx, "DEBUG", "安全验证重试后仍处于验证页，本请求已停止")
				return nil, status, headers, fmt.Errorf("%w: 重试后仍处于验证页", ErrChallenge)
			}
			// One session has one anti-bot cookie jar. Serialize challenge work and
			// probe again after acquiring the lock: another detail worker may have
			// completed the same session challenge while this request was waiting.
			c.challengeMu.Lock()
			probeBody, probeStatus, probeHeaders, probeErr := c.do(ctx, method, path, form)
			if probeErr != nil {
				c.challengeMu.Unlock()
				return nil, probeStatus, probeHeaders, probeErr
			}
			if !isBotChallengePage(probeBody) {
				c.challengeMu.Unlock()
				return probeBody, probeStatus, probeHeaders, nil
			}
			started := time.Now()
			reportProgress(ctx, "DEBUG", "遇到站点安全验证，开始处理（本请求第 1 次，最多 1 次）")
			if err := c.solveChallenge(ctx, path, probeBody); err != nil {
				reportProgress(ctx, "DEBUG", fmt.Sprintf("安全验证未完成，耗时 %.2f 秒，本请求已停止", time.Since(started).Seconds()))
				c.challengeMu.Unlock()
				return nil, status, headers, err
			}
			reportProgress(ctx, "DEBUG", fmt.Sprintf("安全验证通过，耗时 %.2f 秒，重试原请求", time.Since(started).Seconds()))
			c.challengeMu.Unlock()
			continue
		}
		return body, status, headers, nil
	}
	return nil, 0, nil, ErrChallenge
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values) ([]byte, int, http.Header, error) {
	u := *c.baseURL
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		parsed, err := url.Parse(path)
		if err != nil {
			return nil, 0, nil, err
		}
		u = *parsed
	} else {
		parsed, err := url.Parse(path)
		if err != nil {
			return nil, 0, nil, err
		}
		u.Path = parsed.Path
		u.RawQuery = parsed.RawQuery
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131 Safari/537.36")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, nil, ctx.Err()
		}
		return nil, 0, nil, fmt.Errorf("观影站请求失败: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, response.StatusCode, response.Header.Clone(), err
	}
	if len(data) > maxResponseBytes {
		return nil, response.StatusCode, response.Header.Clone(), fmt.Errorf("观影站响应过大")
	}
	return data, response.StatusCode, response.Header.Clone(), nil
}

func isLoginShell(body []byte) bool {
	text := string(body)
	return strings.Contains(text, "_BT.PC.HTML('login')") || strings.Contains(text, `_BT.PC.HTML("login")`) || strings.Contains(text, "未登录，访问受限")
}

func isBotChallengePage(body []byte) bool {
	if len(body) == 0 || len(body) > maxChallengeBody {
		return false
	}
	text := string(body)
	if !strings.Contains(text, "安全验证") && !strings.Contains(text, "机器人") && !strings.Contains(text, "浏览器计算验证") {
		return false
	}
	return strings.Contains(text, "const json") || strings.Contains(text, "powSolve-") || strings.Contains(text, "pow.worker-") || strings.Contains(text, "/res/pow")
}

type challengeData struct {
	ID        string   `json:"id"`
	Challenge []string `json:"challenge"`
	Diff      int      `json:"diff"`
	Salt      string   `json:"salt"`
	N         string   `json:"N"`
	X         string   `json:"x"`
	T         int      `json:"t"`
}

func (c *Client) solveChallenge(ctx context.Context, requestPath string, body []byte) error {
	raw, ok := extractObjectAfter(string(body), "const json")
	verifyPath := requestPath
	remotePoW := false
	if !ok {
		powBody, status, _, err := c.do(ctx, http.MethodGet, "/res/pow", nil)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("%w: 获取 PoW 数据失败", ErrChallenge)
		}
		raw, ok = extractObjectAfter(string(powBody), "{")
		if !ok {
			return fmt.Errorf("%w: 无法解析 PoW 数据", ErrChallenge)
		}
		verifyPath = "/res/pow"
		remotePoW = true
	}
	var challenge challengeData
	if err := json.Unmarshal([]byte(raw), &challenge); err != nil {
		return fmt.Errorf("%w: 解析验证数据失败: %v", ErrChallenge, err)
	}
	form := url.Values{}
	if challenge.N != "" && challenge.X != "" && challenge.T > 0 {
		value, err := computePoW(ctx, challenge)
		if err != nil {
			return err
		}
		if !remotePoW {
			form.Set("action", "verify")
			form.Set("id", challenge.ID)
		}
		form.Set("y", value)
	} else {
		nonces, err := computeNonces(ctx, challenge)
		if err != nil {
			return err
		}
		form.Set("action", "verify")
		form.Set("id", challenge.ID)
		for _, nonce := range nonces {
			form.Add("nonce[]", strconv.Itoa(nonce))
		}
	}
	verifyBody, status, _, err := c.do(ctx, http.MethodPost, verifyPath, form)
	if err != nil {
		return fmt.Errorf("%w: 提交验证失败: %v", ErrChallenge, err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%w: 验证接口 HTTP %d", ErrChallenge, status)
	}
	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(verifyBody, &result); err != nil || !result.Success {
		if result.Msg != "" {
			return fmt.Errorf("%w: %s", ErrChallenge, result.Msg)
		}
		return fmt.Errorf("%w", ErrChallenge)
	}
	return nil
}

func computePoW(ctx context.Context, challenge challengeData) (string, error) {
	if challenge.T <= 0 || challenge.T > maxPoWIterations {
		return "", fmt.Errorf("%w: PoW 计算参数超限", ErrChallenge)
	}
	modulus, ok := new(big.Int).SetString(challenge.N, 16)
	if !ok || modulus.Sign() <= 0 {
		return "", fmt.Errorf("%w: PoW 模数无效", ErrChallenge)
	}
	value, ok := new(big.Int).SetString(challenge.X, 16)
	if !ok || value.Sign() < 0 {
		return "", fmt.Errorf("%w: PoW 初始值无效", ErrChallenge)
	}
	start := time.Now()
	for i := 0; i < challenge.T; i++ {
		if i%1024 == 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}
		}
		value.Mul(value, value).Mod(value, modulus)
	}
	if elapsed := time.Since(start); elapsed < 3*time.Second {
		timer := time.NewTimer(3*time.Second - elapsed)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return value.Text(16), nil
}

func computeNonces(ctx context.Context, challenge challengeData) ([]int, error) {
	if challenge.ID == "" || challenge.Salt == "" || challenge.Diff < 0 || challenge.Diff > maxNonce || len(challenge.Challenge) == 0 {
		return nil, fmt.Errorf("%w: nonce 参数无效", ErrChallenge)
	}
	targets := make(map[string][]int, len(challenge.Challenge))
	for index, value := range challenge.Challenge {
		targets[strings.ToLower(strings.TrimSpace(value))] = append(targets[strings.ToLower(strings.TrimSpace(value))], index)
	}
	nonces := make([]int, len(challenge.Challenge))
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	var mu sync.Mutex
	found := 0
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for nonce := start; nonce <= challenge.Diff; nonce += workers {
				select {
				case <-ctx.Done():
					return
				default:
				}
				input := strconv.Itoa(nonce) + challenge.Salt
				sum := sha256.Sum256([]byte(input))
				hash := hex.EncodeToString(sum[:])
				mu.Lock()
				indexes := targets[hash]
				if len(indexes) > 0 {
					nonces[indexes[0]] = nonce
					if len(indexes) == 1 {
						delete(targets, hash)
					} else {
						targets[hash] = indexes[1:]
					}
					found++
					if found == len(nonces) {
						cancel()
					}
				}
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	if len(targets) != 0 {
		return nil, fmt.Errorf("%w: 无法完成 nonce 验证", ErrChallenge)
	}
	return nonces, nil
}

func parseSearchData(body []byte) (searchData, error) {
	raw, ok := extractObjectAfter(string(body), "_obj.search")
	if !ok {
		if isLoginShell(body) {
			return searchData{}, ErrForbidden
		}
		return searchData{}, fmt.Errorf("搜索页未找到结果数据")
	}
	var data searchData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return searchData{}, fmt.Errorf("解析搜索数据失败: %w", err)
	}
	return data, nil
}

// extractObjectAfter extracts one balanced JSON object after a marker. It is
// less fragile than a regexp when a title or description contains braces.
func extractObjectAfter(text, marker string) (string, bool) {
	markerStart := strings.Index(text, marker)
	if markerStart < 0 {
		return "", false
	}
	start := markerStart
	if marker != "{" {
		offset := strings.Index(text[markerStart+len(marker):], "{")
		if offset < 0 {
			return "", false
		}
		start += len(marker) + offset
	}
	depth := 0
	inString, escaped := false, false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}
	return "", false
}

// SortLinks makes the stable ordering used by the UI explicit while keeping
// the source order for links of the same type.
func SortLinks(links []ResourceLink) {
	sort.SliceStable(links, func(i, j int) bool {
		if links[i].Category != links[j].Category {
			return links[i].Category < links[j].Category
		}
		return links[i].TypeLabel < links[j].TypeLabel
	})
}
