package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type Person struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	OriginalName string   `json:"originalName"`
	ProfileURL   string   `json:"profileUrl"`
	KnownFor     []string `json:"knownFor"`
}
type PersonPage struct {
	Items      []Person `json:"items"`
	Page       int      `json:"page"`
	TotalPages int      `json:"totalPages"`
}
type PersonDetail struct {
	Person
	Biography    string `json:"biography"`
	Birthday     string `json:"birthday"`
	Deathday     string `json:"deathday"`
	PlaceOfBirth string `json:"placeOfBirth"`
	Items        []Item `json:"items"`
}

// SearchPeople 复用人物搜索，只展示表演部门，保留 TMDB 的分页和相关度。
func (c *Client) SearchPeople(ctx context.Context, query string, page int) (*PersonPage, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 100 || page < 1 || page > 500 {
		return nil, ErrInput
	}
	var raw struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
		Results    []struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			OriginalName string `json:"original_name"`
			Profile      string `json:"profile_path"`
			Department   string `json:"known_for_department"`
			Adult        bool   `json:"adult"`
			KnownFor     []struct {
				Title string `json:"title"`
				Name  string `json:"name"`
				Adult bool   `json:"adult"`
			} `json:"known_for"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/search/person", url.Values{"query": {query}, "page": {strconv.Itoa(page)}, "language": {"zh-CN"}, "include_adult": {"false"}}, &raw); err != nil {
		return nil, err
	}
	if raw.Page < 1 || raw.Results == nil {
		return nil, fmt.Errorf("TMDB 未返回有效演员列表")
	}
	result := &PersonPage{Page: raw.Page, TotalPages: min(raw.TotalPages, 500), Items: make([]Person, 0, len(raw.Results))}
	for _, p := range raw.Results {
		if p.ID < 1 || p.Adult || p.Department != "Acting" {
			continue
		}
		person := Person{ID: p.ID, Name: p.Name, OriginalName: p.OriginalName, ProfileURL: imageURL(p.Profile, "w342"), KnownFor: make([]string, 0, len(p.KnownFor))}
		if person.Name == "" {
			person.Name = p.OriginalName
		}
		for _, work := range p.KnownFor {
			if work.Adult {
				continue
			}
			name := work.Title
			if name == "" {
				name = work.Name
			}
			if name != "" {
				person.KnownFor = append(person.KnownFor, name)
			}
		}
		result.Items = append(result.Items, person)
	}
	return result, nil
}

// PersonDetails 一次请求读取演员信息和参演作品，去重时区分电影与剧集 ID。
func (c *Client) PersonDetails(ctx context.Context, id int) (*PersonDetail, error) {
	if id < 1 {
		return nil, ErrInput
	}
	var raw struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Profile      string `json:"profile_path"`
		Adult        bool   `json:"adult"`
		Biography    string `json:"biography"`
		Birthday     string `json:"birthday"`
		Deathday     string `json:"deathday"`
		PlaceOfBirth string `json:"place_of_birth"`
		Credits      *struct {
			Cast []struct {
				ID            int     `json:"id"`
				Kind          string  `json:"media_type"`
				Name          string  `json:"name"`
				Title         string  `json:"title"`
				OriginalName  string  `json:"original_name"`
				OriginalTitle string  `json:"original_title"`
				Overview      string  `json:"overview"`
				Poster        string  `json:"poster_path"`
				Date          string  `json:"release_date"`
				AirDate       string  `json:"first_air_date"`
				Rating        float64 `json:"vote_average"`
				Votes         int     `json:"vote_count"`
				Adult         bool    `json:"adult"`
			} `json:"cast"`
		} `json:"combined_credits"`
	}
	if err := c.get(ctx, "/person/"+strconv.Itoa(id), url.Values{"language": {"zh-CN"}, "append_to_response": {"combined_credits"}}, &raw); err != nil {
		return nil, err
	}
	if raw.Adult {
		return nil, ErrNotFound
	}
	if raw.ID != id || raw.Credits == nil || raw.Credits.Cast == nil {
		return nil, fmt.Errorf("TMDB 未返回有效演员详情")
	}
	result := &PersonDetail{Person: Person{ID: id, Name: raw.Name, ProfileURL: imageURL(raw.Profile, "w500"), KnownFor: []string{}}, Biography: raw.Biography, Birthday: raw.Birthday, Deathday: raw.Deathday, PlaceOfBirth: raw.PlaceOfBirth, Items: make([]Item, 0, len(raw.Credits.Cast))}
	seen := make(map[string]bool, len(raw.Credits.Cast))
	for _, work := range raw.Credits.Cast {
		key := work.Kind + ":" + strconv.Itoa(work.ID)
		if work.ID < 1 || !validKind(work.Kind) || work.Adult || seen[key] {
			continue
		}
		seen[key] = true
		title, original, date := work.Title, work.OriginalTitle, work.Date
		if work.Kind == "tv" {
			title, original, date = work.Name, work.OriginalName, work.AirDate
		}
		if title == "" {
			title = original
		}
		if title == "" {
			title = "未命名"
		}
		result.Items = append(result.Items, Item{ID: work.ID, Kind: work.Kind, Title: title, OriginalTitle: original, Date: date, Overview: work.Overview, PosterURL: posterURL(work.Poster), Rating: work.Rating, Votes: work.Votes})
	}
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].Date > result.Items[j].Date })
	return result, nil
}
