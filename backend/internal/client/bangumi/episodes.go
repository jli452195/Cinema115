package bangumi

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

const EpisodePageSize = 100
const MaxEpisodeItems = 2000

// Number is Bangumi's displayed sort, not its chapter-local ep or a TMDB number.
type Episode struct {
	ID            string   `json:"id"`
	Number        float64  `json:"number"`
	ChapterNumber *float64 `json:"chapterNumber"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"originalTitle"`
	Date          string   `json:"date"`
}

type EpisodesResult struct {
	CacheMeta
	Items    []Episode `json:"items"`
	Total    int       `json:"total"`
	Limit    int       `json:"limit"`
	Offset   int       `json:"offset"`
	MaxItems int       `json:"maxItems"`
}

// Episodes reads one cached page of regular chapters. Long-running shows do
// not trigger an unbounded crawl; the caller explicitly requests further pages.
func (c *Client) Episodes(ctx context.Context, id string, offset int) (EpisodesResult, error) {
	var out EpisodesResult
	if !model.ValidAnimeExternalID(id) || offset < 0 || offset >= MaxEpisodeItems || offset%EpisodePageSize != 0 {
		return out, ErrInput
	}
	q := url.Values{"subject_id": {id}, "type": {"0"}, "limit": {strconv.Itoa(EpisodePageSize)}, "offset": {strconv.Itoa(offset)}}
	path := "/v0/episodes?" + q.Encode()
	read, err := c.cachedRead(ctx, requestSpec{key: "episodes:" + id + ":" + strconv.Itoa(offset), method: "GET", path: path, ttl: time.Hour,
		normalize: func(raw []byte) ([]byte, error) { return normalizeEpisodes(raw, id, offset) }})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(read.data, &out); err != nil {
		return out, ErrInvalid
	}
	out.CacheMeta = read.meta.clone()
	return out, nil
}

func normalizeEpisodes(body []byte, subjectID string, offset int) ([]byte, error) {
	var raw struct {
		Total  *int `json:"total"`
		Limit  *int `json:"limit"`
		Offset *int `json:"offset"`
		Data   []struct {
			ID        json.Number `json:"id"`
			SubjectID json.Number `json:"subject_id"`
			Type      *int        `json:"type"`
			Sort      *float64    `json:"sort"`
			Ep        *float64    `json:"ep"`
			Name      string      `json:"name"`
			NameCN    string      `json:"name_cn"`
			Date      string      `json:"airdate"`
		} `json:"data"`
	}
	if err := decodeWire(body, &raw); err != nil {
		return nil, err
	}
	if raw.Total == nil || *raw.Total < 0 || *raw.Total > 100000 || raw.Limit == nil || *raw.Limit != EpisodePageSize || raw.Offset == nil || *raw.Offset != offset || raw.Data == nil || len(raw.Data) != min(EpisodePageSize, max(0, *raw.Total-offset)) {
		return nil, ErrInvalid
	}
	out := EpisodesResult{Items: make([]Episode, 0, len(raw.Data)), Total: *raw.Total, Limit: EpisodePageSize, Offset: offset, MaxItems: MaxEpisodeItems}
	seen := make(map[string]bool, len(raw.Data))
	validNumber := func(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 99999 }
	for _, item := range raw.Data {
		id := item.ID.String()
		if !model.ValidAnimeExternalID(id) || seen[id] || item.SubjectID.String() != subjectID || item.Type == nil || *item.Type != 0 || item.Sort == nil || !validNumber(*item.Sort) || item.Ep != nil && !validNumber(*item.Ep) || !validText(item.Name, 512) || !validText(item.NameCN, 512) || !validText(item.Date, 64) {
			return nil, ErrInvalid
		}
		seen[id] = true
		title := strings.TrimSpace(item.NameCN)
		if title == "" {
			title = strings.TrimSpace(item.Name)
		}
		date := item.Date
		if _, err := time.Parse("2006-01-02", date); err != nil {
			date = "" // Unknown/partial dates are not evidence for correspondence.
		}
		out.Items = append(out.Items, Episode{ID: id, Number: *item.Sort, ChapterNumber: item.Ep, Title: title, OriginalTitle: item.Name, Date: date})
	}
	return json.Marshal(out)
}
