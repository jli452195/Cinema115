package mikan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	AnimeMaxRows      = 2000
	AnimeMaxBodyBytes = 4 << 20
)

var (
	ErrLimit         = errors.New("蜜柑读取达到本次预算，历史未读完整")
	ErrSourceBinding = errors.New("所选字幕组不属于当前蜜柑条目")
	episodePath      = regexp.MustCompile(`^/Home/Episode/([a-fA-F0-9]{40})$`)
	torrentPath      = regexp.MustCompile(`^/Download/[0-9]{8}/[a-fA-F0-9]{40}\.torrent$`)
)

// Kind distinguishes rows from body/request budgets so later API/UI layers
// cannot mislabel a failed read as the 2000-row preview limit.
type LimitError struct{ Kind string }

func (e *LimitError) Error() string { return ErrLimit.Error() }
func (e *LimitError) Unwrap() error { return ErrLimit }

// Source identifies a publisher feed, not a Bangumi subject or a BT resource.
type Source struct {
	ItemID  string `json:"itemId"`
	GroupID string `json:"groupId"`
}

func (s Source) Valid() bool { return ValidID(s.ItemID) && ValidID(s.GroupID) }

func (s Source) RSSURL() (string, error) {
	if !s.Valid() {
		return "", ErrInput
	}
	return BaseURL + "/RSS/Bangumi?" + url.Values{"bangumiId": {s.ItemID}, "subgroupid": {s.GroupID}}.Encode(), nil
}

// ParseRSSSource accepts only the verified single-group HTTPS endpoint. URLs
// are converted to IDs; callers never send arbitrary input URLs upstream.
func ParseRSSSource(raw string) (Source, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "mikanani.me" || u.User != nil || u.Fragment != "" || u.Path != "/RSS/Bangumi" || u.EscapedPath() != u.Path {
		return Source{}, ErrInput
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["bangumiId"]) != 1 || len(q["subgroupid"]) != 1 {
		return Source{}, ErrInput
	}
	s := Source{ItemID: q.Get("bangumiId"), GroupID: q.Get("subgroupid")}
	if !s.Valid() {
		return Source{}, ErrInput
	}
	return s, nil
}

// A PostKey is a controlled Episode identifier. Neither it nor TorrentURL is
// proof of a verified BT info-hash. P1 only retains these metadata hints.
type SourceRelease struct {
	PostKey               string `json:"postKey"`
	Title                 string `json:"title"`
	OriginalGUID          string `json:"originalGuid"`
	URL                   string `json:"url"`
	TorrentURL            string `json:"torrentUrl"`
	MagnetHint            string `json:"magnetHint"`
	SizeRaw               string `json:"sizeRaw"`
	SizeBytes             int64  `json:"sizeBytes"`
	PublishedRaw          string `json:"publishedRaw"`
	AlternatePublishedRaw string `json:"alternatePublishedRaw"`
}

type SourceEntry struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Groups     []Group  `json:"groups"`
	BangumiIDs []string `json:"bangumiIds"` // Optional hints, never a confirmed chapter binding.
}

type SourcePage struct {
	Kind          string          `json:"kind"`
	Source        Source          `json:"source"`
	Entry         *SourceEntry    `json:"entry,omitempty"`
	Releases      []SourceRelease `json:"releases"`
	RawCount      int             `json:"rawCount"`
	RequestedTake int             `json:"requestedTake"`
	NextTake      int             `json:"nextTake"`
	Completeness  string          `json:"completeness"`
	StopReason    string          `json:"stopReason"`
	ObservedAt    time.Time       `json:"observedAt"`
	ReadBytes     int64           `json:"-"`
}

type SourceRequest struct {
	Kind      string // entry, rss, history
	Source    Source
	Take      int
	MaxRows   int
	BodyLimit int64
}

func ValidSourceRequest(in SourceRequest) bool {
	if !ValidID(in.Source.ItemID) || in.MaxRows < 1 || in.MaxRows > AnimeMaxRows || in.BodyLimit < 1 || in.BodyLimit > AnimeMaxBodyBytes {
		return false
	}
	if in.Kind == "entry" {
		return in.Source.GroupID == "" && in.Take == 0
	}
	return in.Source.Valid() && ((in.Kind == "rss" && in.Take == 0) ||
		(in.Kind == "history" && in.Take >= 1 && in.Take <= in.MaxRows))
}

// ReadSource is the bounded transport for anime metadata. It deliberately
// does not change the existing browser search's 16 MiB/take<=10000 limits.
// MikanService owns shared coordination, caching and binding confirmation.
func (c *Client) ReadSource(ctx context.Context, in SourceRequest) (SourcePage, error) {
	var page SourcePage
	if !ValidSourceRequest(in) {
		return page, ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	path := "/Home/Bangumi/" + in.Source.ItemID
	if in.Kind == "rss" {
		path = "/RSS/Bangumi?" + url.Values{"bangumiId": {in.Source.ItemID}, "subgroupid": {in.Source.GroupID}}.Encode()
	}
	if in.Kind == "history" {
		path = "/Home/ExpandEpisodeTable?" + url.Values{"bangumiId": {in.Source.ItemID}, "subtitleGroupId": {in.Source.GroupID}, "take": {strconv.Itoa(in.Take)}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return page, ErrInput
	}
	req.Header.Set("User-Agent", "Cinema115-Anime/1.0 (https://github.com/jli452195/Cinema115)")
	req.Header.Set("Accept", "text/html")
	if in.Kind == "rss" {
		req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	}
	// Even an injected client must not follow a redirect into another origin.
	httpClient := *c.HTTP
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return page, ctx.Err()
		}
		return page, ErrNetwork
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return page, responseError(resp, time.Now())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, in.BodyLimit+1))
	page.ReadBytes = int64(len(body))
	if err != nil {
		if ctx.Err() != nil {
			return page, ctx.Err()
		}
		return page, ErrNetwork
	}
	if int64(len(body)) > in.BodyLimit {
		return page, &LimitError{Kind: "body"}
	}
	observed := time.Now()
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if in.Kind == "rss" {
		page, err = parseSourceRSS(body, in)
	} else {
		page, err = parseSourceHTML(body, in)
	}
	page.ReadBytes = int64(len(body))
	if err != nil {
		return SourcePage{ReadBytes: page.ReadBytes}, err
	}
	page.Kind, page.Source, page.ObservedAt = in.Kind, in.Source, observed
	if err := ctx.Err(); err != nil {
		return SourcePage{ReadBytes: page.ReadBytes}, err
	}
	return page, nil
}

type ResponseError struct {
	StatusCode int
	RetryAt    time.Time
}

func (e *ResponseError) Error() string { return fmt.Sprintf("蜜柑返回 HTTP %d", e.StatusCode) }
func (e *ResponseError) Unwrap() error {
	if e.StatusCode == 403 || e.StatusCode == 429 || e.StatusCode == 503 {
		return ErrBlocked
	}
	return ErrNetwork
}

func responseError(resp *http.Response, now time.Time) error {
	err := &ResponseError{StatusCode: resp.StatusCode}
	if resp.StatusCode != 403 && resp.StatusCode != 429 && resp.StatusCode != 503 {
		return err
	}
	err.RetryAt = now.Add(30 * time.Second)
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if seconds, parseErr := strconv.ParseUint(raw, 10, 64); parseErr == nil {
		maxSeconds := uint64((time.Duration(1<<63 - 1)) / time.Second)
		if seconds > maxSeconds {
			err.RetryAt = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		} else if until := now.Add(time.Duration(seconds) * time.Second); until.After(err.RetryAt) {
			err.RetryAt = until
		}
	} else if until, parseErr := http.ParseTime(raw); parseErr == nil && until.After(err.RetryAt) {
		err.RetryAt = until
	} else if raw != "" && strings.Trim(raw, "0123456789") == "" {
		// An overflowing positive server delay must not become a short retry.
		err.RetryAt = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	}
	return err
}
