// Package dmhy 读取动漫花园原站的关键词搜索与种子详情。
package dmhy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/netproxy"
)

const BaseURL = "https://share.dmhy.org"
const MaxPage = 1000

var (
	ErrInput   = errors.New("动漫花园搜索词、页码或发布方参数无效")
	ErrParse   = errors.New("动漫花园页面结构异常，请访问原站核对")
	ErrBlocked = errors.New("动漫花园访问受限或需要安全验证，请稍后重试或访问原站")
	ErrNetwork = errors.New("无法连接动漫花园，请检查网络后重试")
	numericID  = regexp.MustCompile(`^[1-9][0-9]{0,11}$`)
	listPath   = regexp.MustCompile(`^/topics/list(?:/page/([1-9][0-9]{0,3}))?$`)
	detailPath = regexp.MustCompile(`^/topics/view/([1-9][0-9]{0,11})_\.html$`)
)

type Client struct{ HTTP *http.Client }

func New() *Client {
	return &Client{HTTP: &http.Client{Transport: netproxy.Transport("dmhy"), Timeout: 20 * time.Second}}
}

func NormalizeQuery(raw string) (string, error) {
	query := strings.TrimSpace(raw)
	if query == "" || len([]rune(query)) > 200 || strings.ContainsFunc(query, unicode.IsControl) {
		return "", ErrInput
	}
	return query, nil
}

func ValidGroup(group string) bool { return group == "" || numericID.MatchString(group) }

func SearchPath(query, group string, page int) (string, error) {
	query, err := NormalizeQuery(query)
	if err != nil || !ValidGroup(group) || page < 1 || page > MaxPage {
		return "", ErrInput
	}
	values := url.Values{"keyword": {query}, "order": {"date-desc"}}
	if group != "" {
		values.Set("team_id", group)
	}
	path := "/topics/list"
	if page > 1 {
		path += "/page/" + strconv.Itoa(page)
	}
	return path + "?" + values.Encode(), nil
}

func DetailPath(id string) (string, error) {
	if !numericID.MatchString(id) {
		return "", ErrInput
	}
	// 原站详情路由要求下划线及 .html；标题段可以为空。
	return "/topics/view/" + id + "_.html", nil
}

func validFetchPath(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.EscapedPath() != u.Path {
		return false
	}
	if detailPath.MatchString(u.Path) {
		return u.RawQuery == "" && !u.ForceQuery
	}
	parts := listPath.FindStringSubmatch(u.Path)
	if len(parts) == 0 {
		return false
	}
	if parts[1] != "" {
		page, _ := strconv.Atoi(parts[1])
		if page < 2 || page > MaxPage {
			return false
		}
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(values["keyword"]) != 1 || len(values["order"]) != 1 || values.Get("order") != "date-desc" || len(values["team_id"]) > 1 {
		return false
	}
	for key := range values {
		if key != "keyword" && key != "order" && key != "team_id" {
			return false
		}
	}
	_, err = NormalizeQuery(values.Get("keyword"))
	return err == nil && ValidGroup(values.Get("team_id"))
}

func (c *Client) Fetch(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if !validFetchPath(path) || maxBytes < 1 || maxBytes > 16<<20 || c == nil || c.HTTP == nil {
		return nil, ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := *c.HTTP
	client.Jar = nil
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+path, nil)
	if err != nil {
		return nil, ErrInput
	}
	req.Header.Set("User-Agent", "Cinema115-DmhySearch/1.0 (https://github.com/jli452195/Cinema115)")
	req.Header.Set("Accept", "text/html")
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrNetwork
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := &animefeed.HTTPError{Site: "dmhy", Status: response.StatusCode}
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			// 上游异常数值不能溢出为负数；较长冷却统一封顶一天。
			if seconds > 24*60*60 {
				seconds = 24 * 60 * 60
			}
			failure.RetryAt = time.Now().Add(time.Duration(seconds) * time.Second)
		} else if until, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			failure.RetryAt = until
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusServiceUnavailable {
			return nil, errors.Join(ErrBlocked, failure)
		}
		return nil, failure
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrNetwork
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrParse
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}
