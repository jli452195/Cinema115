package gying

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var torrentPathPattern = regexp.MustCompile(`^/dbt/[A-Za-z0-9]{1,64}/[a-fA-F0-9]{32,128}$`)
var torrentIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

var ErrTorrentExpired = errors.New("观影种子链接已过期")

// TorrentKey identifies a resource without accepting external download URLs.
func TorrentKey(kind, id, hash string) (string, error) {
	if (kind != "mv" && kind != "tv" && kind != "ac" && kind != "zy") || !torrentIDPattern.MatchString(id) || !magnetHashPattern.MatchString(hash) {
		return "", ErrInput
	}
	return kind + "/" + id + "/" + strings.ToLower(hash), nil
}

// ReadTorrent uses the search ticket first, refreshing detail once when expired.
// Signed links stay in memory; redirects cannot leave the configured site.
func (c *Client) ReadTorrent(ctx context.Context, kind, id, hash, cachedPath string) ([]byte, error) {
	if _, err := TorrentKey(kind, id, hash); err != nil {
		return nil, err
	}
	if torrentPathPattern.MatchString(cachedPath) {
		body, err := c.readTorrentPath(ctx, cachedPath)
		if !errors.Is(err, ErrTorrentExpired) {
			return body, err
		}
	}
	detail, err := c.fetchDetail(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	for _, link := range detailLinks(detail) {
		if link.HasTorrent && strings.HasPrefix(link.URL, "magnet:?xt=urn:btih:"+strings.ToLower(hash)) {
			return c.readTorrentPath(ctx, link.TorrentPath)
		}
	}
	return nil, fmt.Errorf("%w：该资源暂无种子清单，可使用普通离线", ErrResourceUnavailable)
}

func (c *Client) readTorrentPath(ctx context.Context, path string) ([]byte, error) {
	client := *c.http
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != c.baseURL.Scheme || req.URL.Host != c.baseURL.Host || !torrentPathPattern.MatchString(req.URL.EscapedPath()) || req.URL.RawQuery != "" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	reader := &Client{baseURL: c.baseURL, http: &client}
	body, status, _, err := reader.requestWithChallengeRetry(ctx, http.MethodGet, path, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// net/url errors include signed URLs. Never return them to logs or APIs.
		return nil, ErrAccessDenied
	}
	if status == http.StatusForbidden && strings.Contains(string(body), "链接过期") {
		return nil, ErrTorrentExpired
	}
	if err := responseError(body, status, true); err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("读取观影种子失败: HTTP %d", status)
	}
	return body, nil
}
