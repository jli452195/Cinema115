// Package garden reads Anime Garden's native catalogue and attributed resources.
package garden

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

var ErrInvalid = errors.New("invalid Anime Garden response")
var ErrUnavailable = errors.New("Anime Garden unavailable")

type Client struct {
	HTTP                      *http.Client
	MetadataURL, ResourcesURL string
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, MetadataURL: "https://bgm.animes.garden", ResourcesURL: "https://api.animes.garden"}
}

// The adapter constructs paths; callers cannot choose a host or follow redirects.
func (c *Client) Fetch(ctx context.Context, path string, limit int64) ([]byte, error) {
	base, target := c.ResourcesURL, path
	if strings.HasPrefix(path, "/bgm/") {
		base, target = c.MetadataURL, strings.TrimPrefix(path, "/bgm")
	}
	u, err := url.Parse(target)
	if err != nil || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || u.Host != "" || u.Fragment != "" || limit < 1 || limit > 4<<20 || !(u.Path == "/resources" || validDetailPath(u.Path) || strings.HasPrefix(path, "/bgm/") && (u.Path == "/calendar" || u.Path == "/calendars" || u.Path == "/subjects" || strings.HasPrefix(u.Path, "/subject/"))) {
		return nil, animefeed.ErrInput
	}
	h := *c.HTTP
	h.Jar = nil
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+target, nil)
	if err != nil {
		return nil, fmt.Errorf("construct Garden request: %w", err)
	}
	req.Header.Set("User-Agent", "Cinema115-Anime/1.0 (https://github.com/jli452195/Cinema115)")
	req.Header.Set("Accept", "application/json")
	resp, err := h.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read Garden: %w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e := &animefeed.HTTPError{Site: "garden", Status: resp.StatusCode}
		if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n >= 0 {
			e.RetryAt = time.Now().Add(time.Duration(min(n, 3600)) * time.Second)
		} else if t, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			e.RetryAt = minTime(t, time.Now().Add(time.Hour))
		}
		return nil, e
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return body, fmt.Errorf("read Garden body: %w: %w", ErrUnavailable, err)
	}
	if int64(len(body)) > limit || !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return body, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func ValidQuery(q string) bool {
	return strings.TrimSpace(q) != "" && len([]rune(q)) <= 128 && !strings.ContainsFunc(q, unicode.IsControl)
}
func decode(body []byte, out any) error {
	var v struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &v) != nil || !v.OK || len(v.Data) == 0 || string(v.Data) == "null" || json.Unmarshal(v.Data, out) != nil {
		return ErrInvalid
	}
	return nil
}

type nativeSubject struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Poster  string `json:"poster"`
	AirDate string `json:"onair_date"`
	Alias   struct {
		Chinese []string `json:"zh"`
	} `json:"alias"`
	Bangumi struct {
		ID       int64  `json:"id"`
		Platform string `json:"platform"`
		Date     string `json:"date"`
		Images   struct {
			Large string `json:"large"`
		} `json:"images"`
	} `json:"bangumi"`
}
type Item struct {
	ID            string `json:"id"`
	BangumiID     string `json:"bangumiId"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	CoverURL      string `json:"coverUrl"`
	Platform      string `json:"platform"`
	AirDate       string `json:"airDate"`
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
}

func item(a nativeSubject) (Item, error) {
	if a.ID < 1 || a.ID > 10000000 || a.Bangumi.ID != a.ID || strings.TrimSpace(a.Title) == "" || len(a.Title) > 4096 || len(a.Alias.Chinese) > 100 {
		return Item{}, ErrInvalid
	}
	title := a.Title
	if len(a.Alias.Chinese) > 0 && strings.TrimSpace(a.Alias.Chinese[0]) != "" {
		title = strings.TrimSpace(a.Alias.Chinese[0])
	}
	if len(title) > 4096 || len(a.Bangumi.Platform) > 64 {
		return Item{}, ErrInvalid
	}
	date := a.AirDate
	if date == "" {
		date = a.Bangumi.Date
	}
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			date = ""
		}
	}
	id := strconv.FormatInt(a.ID, 10)
	cover := ""
	if a.Poster != "" || a.Bangumi.Images.Large != "" {
		cover = "/api/garden/image/" + id + "/poster.jpeg"
	}
	return Item{ID: id, BangumiID: id, Title: title, OriginalTitle: a.Title, CoverURL: cover, Platform: a.Bangumi.Platform, AirDate: date}, nil
}
func CatalogPath(season, keyword string) (string, error) {
	if keyword != "" {
		if !ValidQuery(keyword) {
			return "", animefeed.ErrInput
		}
		return "/bgm/subjects?" + url.Values{"q": {keyword}, "cursor": {"0"}, "limit": {"100"}}.Encode(), nil
	}
	t, err := time.Parse("2006-01", season)
	if err != nil || t.Year() < 2000 || t.Year() > 2100 || (t.Month()-1)%3 != 0 {
		return "", animefeed.ErrInput
	}
	return "/bgm/calendar?" + url.Values{"season": {season}}.Encode(), nil
}
func ParseCatalog(body []byte, season string) (Catalog, error) {
	out := Catalog{Provider: "garden", Season: season, Days: []Day{}, Items: []Item{}}
	if season == "" {
		var items []nativeSubject
		if err := decode(body, &items); err != nil {
			return out, err
		}
		if len(items) > 100 {
			return out, ErrInvalid
		}
		var page struct {
			Next int64 `json:"next_cursor"`
		}
		if json.Unmarshal(body, &page) != nil {
			return out, ErrInvalid
		}
		out.Truncated = page.Next != 0
		seen := make(map[string]bool, len(items))
		for _, a := range items {
			i, err := item(a)
			if err != nil || seen[i.ID] {
				return out, ErrInvalid
			}
			seen[i.ID] = true
			out.Items = append(out.Items, i)
		}
		return out, nil
	}
	var v struct {
		Seasons  []string          `json:"seasons"`
		Calendar [][]nativeSubject `json:"calendar"`
		Web      []nativeSubject   `json:"web"`
		Korean   []nativeSubject   `json:"korean"`
		Short    []nativeSubject   `json:"short"`
		Motion   []nativeSubject   `json:"motion"`
		Adult    []nativeSubject   `json:"adult"`
	}
	if err := decode(body, &v); err != nil {
		return out, err
	}
	if len(v.Seasons) != 1 || v.Seasons[0] != season || len(v.Calendar) != 7 {
		return out, ErrInvalid
	}
	seen := make(map[string]bool, 500)
	for day, items := range v.Calendar {
		if len(items) > 500 {
			return out, ErrInvalid
		}
		group := Day{Day: day + 1, Items: make([]Item, 0, len(items))}
		for _, a := range items {
			i, err := item(a)
			if err != nil || seen[i.ID] {
				return out, ErrInvalid
			}
			seen[i.ID] = true
			group.Items = append(group.Items, i)
			out.Items = append(out.Items, i)
		}
		out.Days = append(out.Days, group)
	}
	for index, subjects := range [][]nativeSubject{v.Web, v.Korean, v.Short, v.Motion, v.Adult} {
		if len(subjects) > 500 {
			return out, ErrInvalid
		}
		group := Day{Day: index + 8, Items: make([]Item, 0, len(subjects))}
		for _, subject := range subjects {
			i, err := item(subject)
			if err != nil {
				return out, err
			}
			group.Items = append(group.Items, i)
			if !seen[i.ID] {
				seen[i.ID] = true
				out.Items = append(out.Items, i)
			}
		}
		if len(group.Items) > 0 {
			out.Days = append(out.Days, group)
		}
	}
	return out, nil
}

func ParseCatalogSeasons(body []byte) ([]string, error) {
	var rows []struct {
		Season string `json:"season"`
		Count  int    `json:"count"`
	}
	if err := decode(body, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(rows) > 404 {
		return nil, ErrInvalid
	}
	out := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, err := CatalogPath(row.Season, ""); err != nil || row.Count < 0 || seen[row.Season] {
			return nil, ErrInvalid
		}
		seen[row.Season] = true
		out = append(out, row.Season)
	}
	return out, nil
}
func SubjectPath(id string) (string, error) {
	if !animefeed.ValidItem(id) {
		return "", animefeed.ErrInput
	}
	return "/bgm/subject/" + id, nil
}
func ParseSubject(body []byte, id string) (Item, error) {
	var a struct {
		Subject nativeSubject `json:"subject"`
	}
	if err := decode(body, &a); err != nil {
		return Item{}, err
	}
	i, err := item(a.Subject)
	if err != nil || i.ID != id {
		return Item{}, ErrInvalid
	}
	return i, nil
}

func validDetailPath(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) != 4 || parts[1] != "detail" {
		return false
	}
	expected, err := DetailPath(parts[2], parts[3])
	return err == nil && expected == p
}
