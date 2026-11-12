// Package bangumidata 读取社区维护的 bangumi-data 交叉引用数据。
package bangumidata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/netproxy"
)

const (
	defaultURL  = "https://raw.githubusercontent.com/bangumi-data/bangumi-data/master/dist/data.json"
	maxBodySize = 16 << 20
	cacheTTL    = 24 * time.Hour
)

var (
	ErrUnavailable = errors.New("bangumi-data unavailable")
	subjectPattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	tvPattern      = regexp.MustCompile(`^tv/([1-9][0-9]{0,9})(?:/season/([0-9]{1,4})(?:/episode/([1-9][0-9]{0,4}))?)?$`)
)

type Candidate struct {
	TMDBID  string `json:"tmdbId"`
	Season  *int   `json:"season,omitempty"`
	Episode *int   `json:"episode,omitempty"`
	Title   string `json:"title"`
	Year    string `json:"year,omitempty"`
}

type Result struct {
	Candidates []Candidate `json:"candidates"`
	FetchedAt  time.Time   `json:"fetchedAt"`
	Stale      bool        `json:"stale"`
}

type crossReference struct {
	Items []struct {
		Title string `json:"title"`
		Begin string `json:"begin"`
		Sites []struct {
			Site string `json:"site"`
			ID   string `json:"id"`
		} `json:"sites"`
	} `json:"items"`
}

type snapshot struct {
	bySubject map[string][]Candidate
	fetchedAt time.Time
	expiresAt time.Time
}

type flight struct {
	done chan struct{}
	err  error
}

// Client 在查询间共享一份有界的静态数据快照。
type Client struct {
	HTTP *http.Client
	URL  string

	mu       sync.Mutex
	data     *snapshot
	updating *flight
	retryAt  time.Time
	lastErr  error
}

func New() *Client {
	return &Client{
		HTTP: &http.Client{Transport: netproxy.Transport("github"), Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		URL:  defaultURL,
	}
}

// Lookup 只返回 Bangumi 条目 ID 的精确关联。成功但结果为空表示当前数据集没有该条目的受支持 TV 映射。
func (c *Client) Lookup(ctx context.Context, subjectID string) (Result, error) {
	if !subjectPattern.MatchString(subjectID) {
		return Result{}, fmt.Errorf("invalid Bangumi subject ID")
	}
	if c.HTTP == nil || c.URL == "" {
		return Result{}, fmt.Errorf("bangumi-data client is not configured")
	}
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		now := time.Now()
		c.mu.Lock()
		cached := c.data
		if cached != nil && now.Before(cached.expiresAt) {
			result := resultFor(cached, subjectID, false)
			c.mu.Unlock()
			return result, nil
		}
		if c.lastErr != nil && now.Before(c.retryAt) {
			err := c.lastErr
			c.mu.Unlock()
			if cached != nil {
				return resultFor(cached, subjectID, true), err
			}
			return Result{}, err
		}
		if active := c.updating; active != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-active.done:
				if err := ctx.Err(); err != nil {
					return Result{}, err
				}
				if active.err != nil {
					if cached != nil {
						return resultFor(cached, subjectID, true), active.err
					}
					return Result{}, active.err
				}
				continue
			}
		}
		active := &flight{done: make(chan struct{})}
		c.updating = active
		c.mu.Unlock()

		fresh, err := c.fetch(ctx)
		c.mu.Lock()
		if err == nil {
			c.data = fresh
			c.retryAt = time.Time{}
			c.lastErr = nil
		} else if ctx.Err() == nil {
			c.retryAt = time.Now().Add(time.Minute)
			c.lastErr = err
		}
		active.err = err
		c.updating = nil
		close(active.done)
		current := c.data
		c.mu.Unlock()
		if err != nil {
			if current != nil {
				return resultFor(current, subjectID, true), err
			}
			return Result{}, err
		}
		return resultFor(fresh, subjectID, false), nil
	}
}

func resultFor(data *snapshot, subjectID string, stale bool) Result {
	candidates := append([]Candidate(nil), data.bySubject[subjectID]...)
	for i := range candidates {
		if candidates[i].Season != nil {
			season := *candidates[i].Season
			candidates[i].Season = &season
		}
		if candidates[i].Episode != nil {
			episode := *candidates[i].Episode
			candidates[i].Episode = &episode
		}
	}
	return Result{Candidates: candidates, FetchedAt: data.fetchedAt, Stale: stale}
}

func (c *Client) fetch(ctx context.Context) (*snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create bangumi-data request: %w", err)
	}
	request.Header.Set("User-Agent", "Cinema115 (https://github.com/jli452195/Cinema115)")
	request.Header.Set("Accept", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read bangumi-data: %w", err)
	}
	if len(body) > maxBodySize {
		return nil, fmt.Errorf("bangumi-data exceeds %d bytes", maxBodySize)
	}
	var document crossReference
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode bangumi-data: %w", err)
	}
	if document.Items == nil {
		return nil, fmt.Errorf("decode bangumi-data: missing items")
	}
	indexed := make(map[string][]Candidate, 4096)
	seen := make(map[string]struct{}, 4096)
	for _, item := range document.Items {
		var subjectID string
		ambiguousSubject := false
		var targets []Candidate
		for _, site := range item.Sites {
			switch site.Site {
			case "bangumi":
				if subjectPattern.MatchString(site.ID) {
					if subjectID != "" && subjectID != site.ID {
						ambiguousSubject = true
					}
					subjectID = site.ID
				}
			case "tmdb":
				match := tvPattern.FindStringSubmatch(site.ID)
				if match == nil {
					continue
				}
				candidate := Candidate{TMDBID: match[1], Title: item.Title}
				if len(item.Begin) >= 4 {
					candidate.Year = item.Begin[:4]
				}
				if match[2] != "" {
					season, parseErr := strconv.Atoi(match[2])
					if parseErr != nil {
						continue
					}
					candidate.Season = &season
				}
				// 首集定位只作为待核对的线索，不据此断言整篇章的逐集映射。
				if match[3] != "" {
					episode, parseErr := strconv.Atoi(match[3])
					if parseErr != nil {
						continue
					}
					candidate.Episode = &episode
				}
				targets = append(targets, candidate)
			}
		}
		if subjectID == "" || ambiguousSubject {
			continue
		}
		for _, target := range targets {
			key := subjectID + ":" + target.TMDBID + ":"
			if target.Season != nil {
				key += strconv.Itoa(*target.Season)
			}
			if target.Episode != nil {
				key += ":" + strconv.Itoa(*target.Episode)
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			indexed[subjectID] = append(indexed[subjectID], target)
		}
	}
	if len(indexed) == 0 {
		return nil, fmt.Errorf("decode bangumi-data: no valid Bangumi to TMDB TV references")
	}
	now := time.Now().UTC()
	return &snapshot{bySubject: indexed, fetchedAt: now, expiresAt: now.Add(cacheTTL)}, nil
}

// ParseTMDBID 判断 value 是否为数字形式的 TMDB 剧集作品 ID。
func ParseTMDBID(value string) bool {
	return strings.TrimSpace(value) == value && subjectPattern.MatchString(value)
}
