package bangumi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"strm115/backend/internal/model"
)

type wireImages struct {
	Large  string `json:"large"`
	Common string `json:"common"`
	Medium string `json:"medium"`
	Small  string `json:"small"`
	Grid   string `json:"grid"`
}

type wireSubject struct {
	ID            json.Number `json:"id"`
	Type          *int        `json:"type"`
	Name          string      `json:"name"`
	NameCN        string      `json:"name_cn"`
	AirDate       string      `json:"air_date"`
	Date          string      `json:"date"`
	AirWeekday    *int        `json:"air_weekday"`
	Images        *wireImages `json:"images"`
	Summary       string      `json:"summary"`
	Platform      string      `json:"platform"`
	Episodes      *int        `json:"eps"`
	TotalEpisodes *int        `json:"total_episodes"`
	Relation      string      `json:"relation"`
}

// External schemas are allowed to add fields. Required shape and identity are
// checked here; null/wrong containers and trailing values never become empty
// successful results. Normalized cache payloads contain only these DTOs.
func decodeWire(body []byte, dst any) error {
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	if err := d.Decode(dst); err != nil {
		return &UpstreamError{Kind: ErrInvalid, Cause: err}
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func validText(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max
}

var coverPath = regexp.MustCompile(`^/pic/cover/[lcmsg]/[a-fA-F0-9]{2}/[a-fA-F0-9]{2}/[A-Za-z0-9_-]+\.(jpg|jpeg|png|webp)$`)

// Only known Bangumi covers are retained, with HTTP upgraded to HTTPS. The
// image proxy will be connected at the UI/API stage; no image is fetched here.
func normalizeCover(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host != "lain.bgm.tv" || u.Scheme != "https" && u.Scheme != "http" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || !coverPath.MatchString(u.Path) {
		return ""
	}
	return "https://lain.bgm.tv" + u.Path
}

func (w wireSubject) card() (Card, error) {
	id := w.ID.String()
	if !model.ValidAnimeExternalID(id) || w.Type == nil || *w.Type < 1 || *w.Type > 6 || !validText(w.Name, 512) || !validText(w.NameCN, 512) {
		return Card{}, ErrInvalid
	}
	title := strings.TrimSpace(w.NameCN)
	if title == "" {
		title = strings.TrimSpace(w.Name)
	}
	if title == "" {
		return Card{}, ErrInvalid
	}
	date := w.Date
	if date == "" {
		date = w.AirDate
	}
	if !validText(date, 64) || w.AirWeekday != nil && (*w.AirWeekday < 0 || *w.AirWeekday > 7) {
		return Card{}, ErrInvalid
	}
	card := Card{ID: id, Title: title, OriginalTitle: w.Name, Date: date, URL: "https://bgm.tv/subject/" + id}
	if w.AirWeekday != nil && *w.AirWeekday != 0 {
		n := *w.AirWeekday
		card.Weekday = &n
	}
	if w.Images != nil {
		for _, raw := range []string{w.Images.Medium, w.Images.Common, w.Images.Small, w.Images.Large, w.Images.Grid} {
			if card.CoverURL = normalizeCover(raw); card.CoverURL != "" {
				break
			}
		}
	}
	return card, nil
}

func normalizeCalendar(body []byte) ([]byte, error) {
	var raw []struct {
		Weekday *struct {
			ID int `json:"id"`
		} `json:"weekday"`
		Items []wireSubject `json:"items"`
	}
	if err := decodeWire(body, &raw); err != nil {
		return nil, err
	}
	days := make([]Day, 7)
	for i := range days {
		days[i] = Day{Weekday: i + 1, Items: make([]Card, 0)}
	}
	seen := make(map[int]bool, 7)
	for _, day := range raw {
		if day.Weekday == nil || day.Weekday.ID < 1 || day.Weekday.ID > 7 || day.Items == nil || seen[day.Weekday.ID] {
			return nil, ErrInvalid
		}
		seen[day.Weekday.ID] = true
		items := make([]Card, 0, len(day.Items))
		for _, item := range day.Items {
			card, err := item.card()
			if err != nil {
				return nil, err
			}
			if *item.Type == 2 {
				items = append(items, card)
			}
		}
		days[day.Weekday.ID-1].Items = items
	}
	return json.Marshal(days)
}

type searchPayload struct {
	Items  []Card `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

func normalizeSearchBody(body []byte, in SearchOptions) ([]byte, error) {
	var raw struct {
		Total  *int          `json:"total"`
		Limit  *int          `json:"limit"`
		Offset *int          `json:"offset"`
		Data   []wireSubject `json:"data"`
	}
	if err := decodeWire(body, &raw); err != nil {
		return nil, err
	}
	if raw.Total == nil || *raw.Total < 0 || int64(*raw.Total) > model.AnimeMaxRevision || raw.Limit == nil || *raw.Limit != in.Limit || raw.Offset == nil || *raw.Offset != in.Offset || raw.Data == nil || len(raw.Data) > in.Limit || len(raw.Data) > *raw.Total || len(raw.Data) > 0 && in.Offset >= *raw.Total {
		return nil, ErrInvalid
	}
	if in.Offset < *raw.Total && len(raw.Data) > *raw.Total-in.Offset {
		return nil, ErrInvalid
	}
	out := searchPayload{Total: *raw.Total, Limit: in.Limit, Offset: in.Offset, Items: make([]Card, 0, len(raw.Data))}
	for _, item := range raw.Data {
		card, err := item.card()
		if err != nil {
			return nil, err
		}
		// A response that ignored the mandatory anime filter is not a valid
		// zero-result anime search, even when its JSON is otherwise valid.
		if *item.Type != 2 {
			return nil, ErrInvalid
		}
		out.Items = append(out.Items, card)
	}
	return json.Marshal(out)
}

func normalizeSubject(body []byte, id string) ([]byte, error) {
	var raw wireSubject
	if err := decodeWire(body, &raw); err != nil {
		return nil, err
	}
	card, err := raw.card()
	if err != nil {
		return nil, err
	}
	if card.ID != id || *raw.Type != 2 || !validText(raw.Summary, 16000) || !validText(raw.Platform, 128) {
		return nil, ErrInvalid
	}
	for _, count := range []*int{raw.Episodes, raw.TotalEpisodes} {
		if count != nil && (*count < 0 || *count > 99999) {
			return nil, ErrInvalid
		}
	}
	return json.Marshal(Subject{Card: card, Synopsis: raw.Summary, Platform: raw.Platform, Episodes: raw.Episodes, TotalEpisodes: raw.TotalEpisodes})
}

func normalizeRelations(body []byte) ([]byte, error) {
	var raw []wireSubject
	if err := decodeWire(body, &raw); err != nil {
		return nil, err
	}
	items := make([]Relation, 0, len(raw))
	for _, item := range raw {
		card, err := item.card()
		if err != nil {
			return nil, err
		}
		if !validText(item.Relation, 128) {
			return nil, ErrInvalid
		}
		if *item.Type == 2 {
			items = append(items, Relation{Card: card, Relation: item.Relation})
		}
	}
	return json.Marshal(items)
}
