// Package bangumi provides bounded, public anime discovery reads. It does not
// create subscriptions or derive resource availability/episode mappings.
package bangumi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/netproxy"
)

const (
	baseURL          = "https://api.bgm.tv"
	userAgent        = "jli452195/Cinema115/1.0 (https://github.com/jli452195/Cinema115)"
	requestTimeout   = 20 * time.Second
	maxResponseBytes = 2 << 20
	maxCacheBytes    = 8 << 20
	maxCacheEntries  = 128
	maxFlights       = 16
	retention        = 24 * time.Hour
)

var (
	ErrInput       = errors.New("Bangumi 查询参数无效")
	ErrNotFound    = errors.New("Bangumi 资料不存在或已删除")
	ErrAccess      = errors.New("Bangumi 拒绝访问")
	ErrCooldown    = errors.New("Bangumi 请求冷却中")
	ErrUnavailable = errors.New("Bangumi 服务暂时不可用")
	ErrNetwork     = errors.New("无法连接 Bangumi")
	ErrTimeout     = errors.New("Bangumi 请求超时")
	ErrInvalid     = errors.New("Bangumi 返回的数据不完整或格式异常")
	ErrBusy        = errors.New("Bangumi 查询繁忙")
)

// UpstreamError carries only safe classifications, never an upstream body/URL.
// Cause remains available to errors.Is/As without being printed to the UI.
type UpstreamError struct {
	Kind         error
	Status       int
	RetryAfterAt time.Time
	Cause        error
}

func (e *UpstreamError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s (HTTP %d)", e.Kind, e.Status)
	}
	return e.Kind.Error()
}
func (e *UpstreamError) Unwrap() []error {
	if e.Cause != nil {
		return []error{e.Kind, e.Cause}
	}
	return []error{e.Kind}
}

type Issue struct {
	Code         string `json:"code"`
	Status       int    `json:"status,omitempty"`
	RetryAfterAt *int64 `json:"retryAfterAt"`
}

type CacheMeta struct {
	FetchedAt   int64  `json:"fetchedAt"`
	ExpiresAt   int64  `json:"expiresAt"`
	Stale       bool   `json:"stale"`
	DataVersion string `json:"dataVersion"`
	Issue       *Issue `json:"issue"`
}

type Card struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	CoverURL      string `json:"coverUrl"`
	Date          string `json:"date"`
	Weekday       *int   `json:"weekday"`
	URL           string `json:"url"`
}

type Day struct {
	Weekday int    `json:"weekday"`
	Items   []Card `json:"items"`
}

type CalendarResult struct {
	CacheMeta
	Days []Day `json:"days"`
}

type SearchOptions struct {
	Keyword string `json:"keyword"`
	Sort    string `json:"sort"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

type SearchResult struct {
	CacheMeta
	Items  []Card `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type Subject struct {
	Card
	Synopsis      string `json:"synopsis"`
	Platform      string `json:"platform"`
	Episodes      *int   `json:"episodes"`      // eps: regular episodes; zero means unknown.
	TotalEpisodes *int   `json:"totalEpisodes"` // All chapters, including OP/ED; not an archive target.
}

type SubjectResult struct {
	CacheMeta
	Subject Subject `json:"subject"`
}

type Relation struct {
	Card
	Relation string `json:"relation"`
}

type RelationsResult struct {
	CacheMeta
	Items []Relation `json:"items"`
}

func normalizeSearch(in SearchOptions) (SearchOptions, error) {
	in.Keyword = strings.TrimSpace(in.Keyword)
	if !utf8.ValidString(in.Keyword) || utf8.RuneCountInString(in.Keyword) < 1 || utf8.RuneCountInString(in.Keyword) > 128 || strings.ContainsFunc(in.Keyword, unicode.IsControl) {
		return in, ErrInput
	}
	if in.Sort == "" {
		in.Sort = "match"
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Sort != "match" || in.Limit < 1 || in.Limit > 50 || in.Offset < 0 || in.Offset > 1000 {
		return in, ErrInput
	}
	return in, nil
}

func issueFrom(err error, retry time.Time) *Issue {
	code := "UPSTREAM_UNAVAILABLE"
	switch {
	case errors.Is(err, ErrCooldown):
		code = "SOURCE_COOLDOWN"
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		code = "UPSTREAM_TIMEOUT"
	case errors.Is(err, ErrInvalid):
		code = "UPSTREAM_INVALID"
	case errors.Is(err, ErrNotFound):
		code = "UPSTREAM_NOT_FOUND"
	}
	i := &Issue{Code: code}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		i.Status = upstream.Status
	}
	if !retry.IsZero() {
		ms := retry.UTC().UnixMilli()
		i.RetryAfterAt = &ms
	}
	return i
}

func newHTTPClient() *http.Client {
	return &http.Client{Transport: netproxy.Transport("bangumi"), Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (m CacheMeta) clone() CacheMeta {
	if m.Issue != nil {
		copy := *m.Issue
		if copy.RetryAfterAt != nil {
			value := *copy.RetryAfterAt
			copy.RetryAfterAt = &value
		}
		m.Issue = &copy
	}
	return m
}
