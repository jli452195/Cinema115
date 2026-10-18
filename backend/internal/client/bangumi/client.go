package bangumi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"strm115/backend/internal/model"
)

// Calendar reads the weekly snapshot once. Weekday filtering is local and
// cannot create seven upstream cache keys or trigger per-card detail reads.
func (c *Client) Calendar(ctx context.Context, weekday int) (CalendarResult, error) {
	var out CalendarResult
	if weekday < 0 || weekday > 7 {
		return out, ErrInput
	}
	read, err := c.cachedRead(ctx, requestSpec{key: "calendar", method: "GET", path: "/calendar", ttl: time.Hour, normalize: normalizeCalendar})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(read.data, &out.Days); err != nil {
		return out, ErrInvalid
	}
	out.CacheMeta = read.meta.clone()
	if weekday != 0 {
		out.Days = out.Days[weekday-1 : weekday]
	}
	return out, nil
}

func (c *Client) Search(ctx context.Context, input SearchOptions) (SearchResult, error) {
	var out SearchResult
	in, err := normalizeSearch(input)
	if err != nil {
		return out, err
	}
	q := url.Values{"limit": {strconv.Itoa(in.Limit)}, "offset": {strconv.Itoa(in.Offset)}}
	var query struct {
		Keyword string `json:"keyword"`
		Sort    string `json:"sort"`
		Filter  struct {
			Type []int `json:"type"`
		} `json:"filter"`
	}
	query.Keyword, query.Sort, query.Filter.Type = in.Keyword, in.Sort, []int{2}
	body, err := json.Marshal(query)
	if err != nil {
		return out, ErrInput
	}
	path := "/v0/search/subjects?" + q.Encode()
	read, err := c.cachedRead(ctx, requestSpec{key: "POST " + path + " " + string(body), method: "POST", path: path, body: body, ttl: 5 * time.Minute, normalize: func(raw []byte) ([]byte, error) { return normalizeSearchBody(raw, in) }})
	if err != nil {
		return out, err
	}
	var payload searchPayload
	if err := json.Unmarshal(read.data, &payload); err != nil {
		return out, ErrInvalid
	}
	out.Items, out.Total, out.Limit, out.Offset = payload.Items, payload.Total, payload.Limit, payload.Offset
	out.CacheMeta = read.meta.clone()
	return out, nil
}

func (c *Client) Subject(ctx context.Context, id string) (SubjectResult, error) {
	var out SubjectResult
	if !model.ValidAnimeExternalID(id) {
		return out, ErrInput
	}
	read, err := c.cachedRead(ctx, requestSpec{key: "subject:" + id, method: "GET", path: "/v0/subjects/" + id, ttl: time.Hour, normalize: func(raw []byte) ([]byte, error) { return normalizeSubject(raw, id) }})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(read.data, &out.Subject); err != nil {
		return out, ErrInvalid
	}
	out.CacheMeta = read.meta.clone()
	return out, nil
}

func (c *Client) Relations(ctx context.Context, id string) (RelationsResult, error) {
	var out RelationsResult
	if !model.ValidAnimeExternalID(id) {
		return out, ErrInput
	}
	read, err := c.cachedRead(ctx, requestSpec{key: "relations:" + id, method: "GET", path: "/v0/subjects/" + id + "/subjects", ttl: time.Hour, normalize: normalizeRelations})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(read.data, &out.Items); err != nil {
		return out, ErrInvalid
	}
	out.CacheMeta = read.meta.clone()
	return out, nil
}
