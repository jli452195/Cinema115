package btresource

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"strm115/backend/internal/client/animefeed"
)

var mikanTorrentPath = regexp.MustCompile(`^/Download/[0-9]{8}/([a-fA-F0-9]{40})\.torrent$`)
var dmhyTorrentPath = regexp.MustCompile(`^/[0-9]{4}/[0-9]{2}/[0-9]{2}/([a-fA-F0-9]{40})\.torrent$`)
var anibtPostKey = regexp.MustCompile(`^rel_[A-Za-z0-9_-]{1,60}$`)
var ErrRead = errors.New("anime torrent metadata unavailable")

// TorrentURL rejects arbitrary hosts, query strings, credentials, encoded paths
// and redirects. Download filenames bind a post, never prove the info-hash.
func TorrentURL(provider, postKey, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.EscapedPath() != u.Path {
		return "", ErrInvalid
	}
	if provider == "mikan" && u.Host == "mikanani.me" {
		parts := mikanTorrentPath.FindStringSubmatch(u.Path)
		if len(parts) == 2 && strings.EqualFold(parts[1], postKey) {
			return u.String(), nil
		}
	}
	if provider == "anibt" && u.Host == "anibt.net" && anibtPostKey.MatchString(postKey) && u.Path == "/api/torrent/"+postKey+".torrent" {
		return u.String(), nil
	}
	if provider == "dmhy" && u.Host == "dl.dmhy.org" {
		parts := dmhyTorrentPath.FindStringSubmatch(u.Path)
		if len(parts) == 2 && strings.EqualFold(parts[1], postKey) {
			return u.String(), nil
		}
	}
	return "", ErrInvalid
}

type HTTPReader struct{ HTTP *http.Client }

func (r *HTTPReader) ReadTorrent(ctx context.Context, provider, postKey, raw string) ([]byte, error) {
	endpoint, err := TorrentURL(provider, postKey, raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := http.Client{Timeout: 20 * time.Second}
	if r != nil && r.HTTP != nil {
		client = *r.HTTP
	}
	client.Jar = nil
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("User-Agent", "Cinema115-Anime/1.0 (https://github.com/jli452195/Cinema115)")
	req.Header.Set("Accept", "application/x-bittorrent, application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrRead
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &animefeed.HTTPError{Site: provider, Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxTorrentBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrRead
	}
	if len(body) > MaxTorrentBytes {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}
