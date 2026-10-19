// Package anibt reads AniBT's anonymous, documented catalogue and feed APIs.
package anibt

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
	"time"
	"unicode"

	"strm115/backend/internal/client/animefeed"
)

const BaseURL = "https://anibt.net"

var ErrInvalid = errors.New("invalid AniBT response")
var ErrMoved = errors.New("AniBT source has been merged; select its current entry")
var ErrUnavailable = errors.New("AniBT unavailable")

type HTTPError struct {
	Status  int
	RetryAt time.Time
}

func (e *HTTPError) Error() string { return fmt.Sprintf("AniBT HTTP %d", e.Status) }

type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func New() *Client { return &Client{BaseURL: BaseURL, HTTP: &http.Client{Timeout: 20 * time.Second}} }

// Paths are constructed by the adapter. No user-supplied URL or credentials
// are accepted. A canonical same-source RSS redirect is bounded to two hops.
func (c *Client) Fetch(ctx context.Context, path string, limit int64) ([]byte, error) {
	if limit < 1 || limit > 4<<20 || !(strings.HasPrefix(path, "/api/seasons/anime?") || strings.HasPrefix(path, "/api/anime/groups?") || strings.HasPrefix(path, "/rss/anime.xml?")) {
		return nil, animefeed.ErrInput
	}
	client := *c.HTTP
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 2 {
			return ErrInvalid
		}
		first := via[0].URL
		if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host || req.URL.Path != "/rss/anime.xml" || first.Path != "/rss/anime.xml" || req.URL.User != nil || req.URL.Fragment != "" {
			return ErrInvalid
		}
		old, next := first.Query(), req.URL.Query()
		if old.Get("bgmId") != next.Get("bgmId") {
			return ErrMoved
		}
		if old.Get("groupSlug") != next.Get("groupSlug") || len(next) > 3 || len(next["bgmId"]) != 1 || len(next["groupSlug"]) != 1 || len(next["limit"]) > 1 || next.Get("limit") != "" && next.Get("limit") != old.Get("limit") {
			return ErrInvalid
		}
		for key := range next {
			if key != "bgmId" && key != "groupSlug" && key != "limit" {
				return ErrInvalid
			}
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("construct AniBT read: %w", err)
	}
	req.Header.Set("User-Agent", "Cinema115-Anime/1.0 (https://github.com/jli452195/Cinema115)")
	req.Header.Set("Accept", "application/json, application/xml")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrMoved) || errors.Is(err, ErrInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("read AniBT: %w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e := &HTTPError{Status: resp.StatusCode}
		if v := resp.Header.Get("Retry-After"); v != "" {
			if seconds, err := strconv.Atoi(v); err == nil && seconds >= 0 {
				e.RetryAt = time.Now().Add(time.Duration(min(seconds, 3600)) * time.Second)
			} else if t, err := http.ParseTime(v); err == nil {
				e.RetryAt = t
			}
		}
		return nil, e
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return body, fmt.Errorf("read AniBT body: %w: %w", ErrUnavailable, err)
	}
	if int64(len(body)) > limit {
		return body, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(path, "/api/") && !strings.Contains(ct, "application/json") || strings.HasPrefix(path, "/rss/") && !strings.Contains(ct, "xml") {
		return nil, ErrInvalid
	}
	return body, nil
}
func decode(body []byte, out any) error {
	var v struct {
		OK   *bool           `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &v) != nil || v.OK == nil || !*v.OK || len(v.Data) == 0 || string(v.Data) == "null" || json.Unmarshal(v.Data, out) != nil {
		return ErrInvalid
	}
	return nil
}
func ValidQuery(q string) bool {
	return strings.TrimSpace(q) != "" && len([]rune(q)) <= 120 && !strings.ContainsFunc(q, unicode.IsControl)
}
func CatalogPath(season, query string) (string, error) {
	q := url.Values{}
	if query != "" {
		if !ValidQuery(query) {
			return "", animefeed.ErrInput
		}
		q.Set("query", query)
	} else {
		if len(season) != 7 {
			return "", animefeed.ErrInput
		}
		year, err := strconv.Atoi(season[:4])
		if err != nil || year < 2000 || year > 2100 {
			return "", animefeed.ErrInput
		}
		name := map[string]string{"01": "WINTER", "04": "SPRING", "07": "SUMMER", "10": "FALL"}[season[5:]]
		if season[4] != '-' || name == "" {
			return "", animefeed.ErrInput
		}
		q.Set("season", season[:4]+"-"+name)
	}
	return "/api/seasons/anime?" + q.Encode(), nil
}

type Title struct {
	Primary  string `json:"primary"`
	Chinese  string `json:"chinese"`
	Japanese string `json:"japanese"`
}

func (t Title) Name() string {
	for _, v := range []string{t.Chinese, t.Primary, t.Japanese} {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

type Item struct {
	ID            string   `json:"id"`
	BangumiID     string   `json:"bangumiId"`
	AnimeID       string   `json:"animeId"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"originalTitle"`
	CoverURL      string   `json:"coverUrl"`
	Format        string   `json:"format"`
	Rating        *float64 `json:"rating"`
	Episodes      *int     `json:"episodes"`
	AiringAt      *int64   `json:"airingAt"`
	ReleaseCount  int      `json:"releaseCount"`
	HasRelease    bool     `json:"hasRelease"`
}
type Day struct {
	Day   int    `json:"day"`
	Items []Item `json:"items"`
}
type Catalog struct {
	Provider  string `json:"provider"`
	Season    string `json:"season"`
	Days      []Day  `json:"days"`
	Items     []Item `json:"items"`
	Truncated bool   `json:"truncated"`
	Total     int    `json:"total"`
}
type apiItem struct {
	BgmID        int64    `json:"bgmId"`
	AnimeID      string   `json:"animeId"`
	Title        Title    `json:"title"`
	Cover        string   `json:"cover"`
	Format       string   `json:"format"`
	Rating       *float64 `json:"rating"`
	Episodes     *int     `json:"episodes"`
	AiringAt     *int64   `json:"airingAt"`
	ReleaseCount int      `json:"rssReleaseCount"`
	HasRelease   bool     `json:"hasRelease"`
}

func ParseCatalog(body []byte, season string) (Catalog, error) {
	out := Catalog{Provider: "anibt", Season: season, Days: make([]Day, 0, 8), Items: []Item{}}
	var v struct {
		ByWeekday []struct {
			Weekday *int      `json:"weekday"`
			Animes  []apiItem `json:"animes"`
		} `json:"byWeekday"`
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
	}
	if err := decode(body, &v); err != nil {
		return out, err
	}
	if v.ByWeekday == nil || len(v.ByWeekday) > 8 || v.Total < 0 {
		return out, ErrInvalid
	}
	seen := make(map[int]bool, 8)
	ids := make(map[int64]bool, 200)
	for _, g := range v.ByWeekday {
		day := 8
		if g.Weekday != nil {
			day = *g.Weekday
			if day < 0 || day > 6 {
				return out, ErrInvalid
			}
			if day == 0 {
				day = 7
			}
		}
		if seen[day] || len(g.Animes) > 500 {
			return out, ErrInvalid
		}
		seen[day] = true
		group := Day{Day: day, Items: make([]Item, 0, len(g.Animes))}
		for _, a := range g.Animes {
			if a.BgmID < 1 || a.BgmID > 10000000 || ids[a.BgmID] || a.Title.Name() == "" || len(a.Title.Name()) > 4096 || a.ReleaseCount < 0 || a.Episodes != nil && (*a.Episodes < 0 || *a.Episodes > 99999) || a.Rating != nil && (*a.Rating < 0 || *a.Rating > 10) {
				return out, ErrInvalid
			}
			ids[a.BgmID] = true
			id := strconv.FormatInt(a.BgmID, 10)
			item := Item{ID: id, BangumiID: id, AnimeID: a.AnimeID, Title: a.Title.Name(), OriginalTitle: a.Title.Japanese, CoverURL: a.Cover, Format: a.Format, Rating: a.Rating, Episodes: a.Episodes, AiringAt: a.AiringAt, ReleaseCount: a.ReleaseCount, HasRelease: a.HasRelease}
			group.Items = append(group.Items, item)
			out.Items = append(out.Items, item)
		}
		out.Days = append(out.Days, group)
	}
	out.Total, out.Truncated = v.Total, v.Truncated
	return out, nil
}
func GroupsPath(id string) (string, error) {
	if !animefeed.ValidItem(id) {
		return "", animefeed.ErrInput
	}
	return "/api/anime/groups?bgmId=" + id, nil
}
func ParseEntry(body []byte, id string) (animefeed.SourceEntry, error) {
	out := animefeed.SourceEntry{ID: id, Groups: []animefeed.Group{}, BangumiIDs: []string{id}}
	var v struct {
		BgmID  int64 `json:"bgmId"`
		Title  Title `json:"title"`
		Groups []struct {
			Slug  string `json:"slug"`
			Name  string `json:"name"`
			Total *int   `json:"totalReleases"`
		} `json:"groups"`
	}
	if err := decode(body, &v); err != nil {
		return out, err
	}
	if strconv.FormatInt(v.BgmID, 10) != id || v.Title.Name() == "" || v.Groups == nil || len(v.Groups) > 2000 {
		return out, ErrInvalid
	}
	out.Title = v.Title.Name()
	seen := make(map[string]bool, len(v.Groups))
	for _, g := range v.Groups {
		if !animefeed.ValidGroup("anibt", g.Slug) || g.Name == "" || len([]rune(g.Name)) > 256 || seen[g.Slug] || g.Total != nil && *g.Total < 0 {
			return out, ErrInvalid
		}
		seen[g.Slug] = true
		out.Groups = append(out.Groups, animefeed.Group{ID: g.Slug, Name: g.Name, TotalReleases: g.Total})
	}
	return out, nil
}
