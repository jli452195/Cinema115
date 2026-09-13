package tmdb

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
	"sync"
	"time"
)

var ErrInput = errors.New("看板查询参数无效")
var ErrNotConfigured = errors.New("请先在根目录 .env 中填写 TMDB_API_KEY 并重启后端")
var ErrNotFound = errors.New("未找到对应的影视或季信息")

type cachedResponse struct {
	data    []byte
	expires time.Time
}

// Client 只访问固定的 TMDB API，密钥不会出现在对外响应或错误信息中。
type Client struct {
	key   string
	http  *http.Client
	base  string
	mu    sync.Mutex
	cache map[string]cachedResponse
}

func New(key string) *Client {
	return &Client{key: strings.TrimSpace(key), base: "https://api.themoviedb.org/3", http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cache: make(map[string]cachedResponse)}
}

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type Item struct {
	ID            int     `json:"id"`
	Kind          string  `json:"kind"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"originalTitle"`
	Overview      string  `json:"overview"`
	PosterURL     string  `json:"posterUrl"`
	Date          string  `json:"date"`
	Rating        float64 `json:"rating"`
	Votes         int     `json:"votes"`
}
type Page struct {
	Items      []Item `json:"items"`
	Page       int    `json:"page"`
	TotalPages int    `json:"totalPages"`
	Total      int    `json:"total"`
}
type Options struct {
	Kind, Genre, Region, Year, Sort, Query string
	Page                                   int
}

func validKind(kind string) bool { return kind == "tv" || kind == "movie" }

func (c *Client) get(ctx context.Context, endpoint string, query url.Values, target any) error {
	if c.key == "" {
		return ErrNotConfigured
	}
	cacheKey := endpoint + "?" + query.Encode()
	c.mu.Lock()
	cached, ok := c.cache[cacheKey]
	c.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		if err := json.Unmarshal(cached.data, target); err != nil {
			return fmt.Errorf("解析 TMDB 缓存失败")
		}
		return nil
	}
	query.Set("api_key", c.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("创建 TMDB 请求失败")
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("无法连接 TMDB，请检查后端网络")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case 404:
			return ErrNotFound
		case 401, 403:
			return fmt.Errorf("TMDB 密钥无效或无权访问，请检查 TMDB_API_KEY")
		case 429:
			return fmt.Errorf("TMDB 请求过于频繁，请稍后重试")
		default:
			return fmt.Errorf("TMDB 返回 HTTP %d，请稍后重试", response.StatusCode)
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil {
		return fmt.Errorf("读取 TMDB 响应失败")
	}
	if len(data) > 2<<20 {
		return fmt.Errorf("TMDB 响应超过大小限制")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("TMDB 返回的数据格式不正确")
	}
	c.mu.Lock()
	// 有界缓存限制 NAS 内存占用，只缓存成功请求五分钟。
	if len(c.cache) >= 128 {
		for key, value := range c.cache {
			if time.Now().After(value.expires) {
				delete(c.cache, key)
			}
		}
		if len(c.cache) >= 128 {
			for key := range c.cache {
				delete(c.cache, key)
				break
			}
		}
	}
	c.cache[cacheKey] = cachedResponse{data: data, expires: time.Now().Add(5 * time.Minute)}
	c.mu.Unlock()
	return nil
}

func (c *Client) Genres(ctx context.Context, kind string) ([]Genre, error) {
	if !validKind(kind) {
		return nil, ErrInput
	}
	var result struct {
		Genres []Genre `json:"genres"`
	}
	if err := c.get(ctx, "/genre/"+kind+"/list", url.Values{"language": {"zh-CN"}}, &result); err != nil {
		return nil, err
	}
	if result.Genres == nil {
		return nil, fmt.Errorf("TMDB 未返回类型列表")
	}
	return result.Genres, nil
}

func optionsQuery(options Options) (string, url.Values, error) {
	if !validKind(options.Kind) || options.Page < 1 || options.Page > 500 {
		return "", nil, ErrInput
	}
	query := url.Values{"language": {"zh-CN"}, "page": {strconv.Itoa(options.Page)}, "include_adult": {"false"}}
	if options.Query != "" {
		if len([]rune(options.Query)) > 100 || options.Genre != "" || options.Region != "" || options.Year != "" || options.Sort != "" {
			return "", nil, ErrInput
		}
		query.Set("query", options.Query)
		return "/search/" + options.Kind, query, nil
	}
	if options.Genre != "" {
		id, err := strconv.Atoi(options.Genre)
		if err != nil || id < 1 || id > 99999 {
			return "", nil, ErrInput
		}
		query.Set("with_genres", strconv.Itoa(id))
	}
	regions := map[string]string{"CN": "CN", "HK": "HK", "TW": "TW", "JP": "JP", "KR": "KR", "western": "US|GB|FR|DE|CA|AU"}
	if options.Region != "" {
		region, ok := regions[options.Region]
		if !ok {
			return "", nil, ErrInput
		}
		query.Set("with_origin_country", region)
	}
	dateField, yearField := "first_air_date", "first_air_date_year"
	if options.Kind == "movie" {
		dateField, yearField = "primary_release_date", "primary_release_year"
	}
	if options.Year != "" {
		year, err := strconv.Atoi(options.Year)
		if err != nil || year < 1900 || year > time.Now().Year() {
			return "", nil, ErrInput
		}
		query.Set(yearField, strconv.Itoa(year))
	}
	query.Set(dateField+".lte", time.Now().Format("2006-01-02"))
	switch options.Sort {
	case "", "popular":
		query.Set("sort_by", "popularity.desc")
	case "latest":
		query.Set("sort_by", dateField+".desc")
	case "rating":
		query.Set("sort_by", "vote_average.desc")
		query.Set("vote_count.gte", "100")
	default:
		return "", nil, ErrInput
	}
	return "/discover/" + options.Kind, query, nil
}

func posterURL(path string) string {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?\\#") {
		return ""
	}
	for _, r := range strings.TrimPrefix(path, "/") {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return ""
		}
	}
	return "https://image.tmdb.org/t/p/w342" + path
}

func (c *Client) Browse(ctx context.Context, options Options) (*Page, error) {
	options.Query = strings.TrimSpace(options.Query)
	endpoint, query, err := optionsQuery(options)
	if err != nil {
		return nil, err
	}
	return c.readPage(ctx, options.Kind, endpoint, query)
}

// 看板和相关推荐共用列表归一化、海报地址校验及缓存。
func (c *Client) readPage(ctx context.Context, kind, endpoint string, query url.Values) (*Page, error) {
	var result struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
		Total      int `json:"total_results"`
		Results    []struct {
			ID            int     `json:"id"`
			Name          string  `json:"name"`
			Title         string  `json:"title"`
			OriginalName  string  `json:"original_name"`
			OriginalTitle string  `json:"original_title"`
			Overview      string  `json:"overview"`
			Poster        string  `json:"poster_path"`
			AirDate       string  `json:"first_air_date"`
			ReleaseDate   string  `json:"release_date"`
			Rating        float64 `json:"vote_average"`
			Votes         int     `json:"vote_count"`
			Adult         bool    `json:"adult"`
		} `json:"results"`
	}
	if err := c.get(ctx, endpoint, query, &result); err != nil {
		return nil, err
	}
	if result.Results == nil || result.Page < 1 {
		return nil, fmt.Errorf("TMDB 未返回有效列表")
	}
	page := &Page{Items: make([]Item, 0, len(result.Results)), Page: result.Page, Total: result.Total, TotalPages: min(result.TotalPages, 500)}
	for _, entry := range result.Results {
		if entry.Adult || entry.ID <= 0 {
			continue
		}
		title, original, date := entry.Name, entry.OriginalName, entry.AirDate
		if kind == "movie" {
			title, original, date = entry.Title, entry.OriginalTitle, entry.ReleaseDate
		}
		if title == "" {
			title = original
		}
		if title == "" {
			title = "未命名"
		}
		page.Items = append(page.Items, Item{ID: entry.ID, Kind: kind, Title: title, OriginalTitle: original, Overview: entry.Overview, PosterURL: posterURL(entry.Poster), Date: date, Rating: entry.Rating, Votes: entry.Votes})
	}
	return page, nil
}
