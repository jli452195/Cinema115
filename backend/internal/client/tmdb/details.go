package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type CastMember struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Character  string `json:"character"`
	ProfileURL string `json:"profileUrl"`
}

type SeasonSummary struct {
	ID           int    `json:"id"`
	Number       int    `json:"number"`
	Name         string `json:"name"`
	PosterURL    string `json:"posterUrl"`
	EpisodeCount int    `json:"episodeCount"`
	Date         string `json:"date"`
}

type Collection struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Detail struct {
	CacheInfo
	Item
	BackdropURL  string          `json:"backdropUrl"`
	Tagline      string          `json:"tagline"`
	Status       string          `json:"status"`
	Runtime      int             `json:"runtime"`
	EpisodeCount int             `json:"episodeCount"`
	Genres       []Genre         `json:"genres"`
	Countries    []string        `json:"countries"`
	CountryCodes []string        `json:"countryCodes"`
	Cast         []CastMember    `json:"cast"`
	Seasons      []SeasonSummary `json:"seasons"`
	Collection   *Collection     `json:"collection"`
	IMDBID       string          `json:"imdbId"`
	IMDBURL      string          `json:"imdbUrl"`
	TMDBURL      string          `json:"tmdbUrl"`
	Numbering    *NumberingInfo  `json:"numbering,omitempty"`
}

type Episode struct {
	ID           int     `json:"id"`
	Number       int     `json:"number"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	Date         string  `json:"date"`
	Runtime      int     `json:"runtime"`
	Rating       float64 `json:"rating"`
	Votes        int     `json:"votes"`
	StillURL     string  `json:"stillUrl"`
	SourceSeason int     `json:"sourceSeason"`
	SourceNumber int     `json:"sourceNumber"`
}

type SeasonDetail struct {
	CacheInfo
	NumberingRevision int64     `json:"numberingRevision"`
	ID                int       `json:"id"`
	Number            int       `json:"number"`
	Name              string    `json:"name"`
	Overview          string    `json:"overview"`
	Episodes          []Episode `json:"episodes"`
}

// 这些字段由缓存读取时计算，不作为过期状态持久化。
type CacheInfo struct {
	FetchedAt  int64 `json:"fetchedAt,omitempty"`
	CacheStale bool  `json:"cacheStale,omitempty"`
}
type ScheduledEpisode struct {
	Number int    `json:"number"`
	Date   string `json:"date"`
}
type Schedule struct {
	CacheInfo
	NumberingRevision int64              `json:"numberingRevision"`
	Number            int                `json:"number"`
	Episodes          []ScheduledEpisode `json:"episodes"`
}

type rawCast struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Character string `json:"character"`
	Profile   string `json:"profile_path"`
	Roles     []struct {
		Character string `json:"character"`
	} `json:"roles"`
}

// 图片尺寸由调用处固定，路径复用海报校验，避免把上游任意地址传给浏览器。
func imageURL(path, size string) string {
	if posterURL(path) == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}

func validIMDBID(id string) string {
	if !strings.HasPrefix(id, "tt") || len(id) < 3 || len(id) > 16 {
		return ""
	}
	for _, r := range id[2:] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}

func imdbURL(id string) string {
	id = validIMDBID(id)
	if id == "" {
		return ""
	}
	return "https://www.imdb.com/title/" + id + "/"
}

// Details 将电影和剧集的不同字段归一化，剧集使用全季演员信息。
func (c *Client) Details(ctx context.Context, kind string, id int) (*Detail, error) {
	if !validKind(kind) || id < 1 {
		return nil, ErrInput
	}
	var raw struct {
		ID             int         `json:"id"`
		Title          string      `json:"title"`
		Name           string      `json:"name"`
		OriginalTitle  string      `json:"original_title"`
		OriginalName   string      `json:"original_name"`
		Overview       string      `json:"overview"`
		Poster         string      `json:"poster_path"`
		Backdrop       string      `json:"backdrop_path"`
		ReleaseDate    string      `json:"release_date"`
		AirDate        string      `json:"first_air_date"`
		Rating         float64     `json:"vote_average"`
		Votes          int         `json:"vote_count"`
		Tagline        string      `json:"tagline"`
		Status         string      `json:"status"`
		Runtime        int         `json:"runtime"`
		EpisodeRuntime []int       `json:"episode_run_time"`
		EpisodeCount   int         `json:"number_of_episodes"`
		Genres         []Genre     `json:"genres"`
		Collection     *Collection `json:"belongs_to_collection"`
		Countries      []struct {
			Code string `json:"iso_3166_1"`
			Name string `json:"name"`
		} `json:"production_countries"`
		ExternalIDs struct {
			IMDB string `json:"imdb_id"`
		} `json:"external_ids"`
		Credits struct {
			Cast []rawCast `json:"cast"`
		} `json:"credits"`
		AggregateCredits struct {
			Cast []rawCast `json:"cast"`
		} `json:"aggregate_credits"`
		Seasons []struct {
			ID           int    `json:"id"`
			Number       int    `json:"season_number"`
			Name         string `json:"name"`
			Poster       string `json:"poster_path"`
			EpisodeCount int    `json:"episode_count"`
			Date         string `json:"air_date"`
		} `json:"seasons"`
	}
	appendices := "credits,external_ids"
	if kind == "tv" {
		appendices = "aggregate_credits,external_ids"
	}
	endpoint := "/" + kind + "/" + strconv.Itoa(id)
	if err := c.get(ctx, endpoint, url.Values{"language": {"zh-CN"}, "append_to_response": {appendices}}, &raw); err != nil {
		return nil, err
	}
	if raw.ID != id {
		return nil, fmt.Errorf("TMDB 未返回有效的影视详情")
	}
	title, original, date, cast := raw.Title, raw.OriginalTitle, raw.ReleaseDate, raw.Credits.Cast
	if kind == "tv" {
		title, original, date, cast = raw.Name, raw.OriginalName, raw.AirDate, raw.AggregateCredits.Cast
		if len(raw.EpisodeRuntime) > 0 {
			raw.Runtime = raw.EpisodeRuntime[0]
		}
	}
	if title == "" {
		title = original
	}
	if title == "" {
		title = "未命名"
	}
	if raw.Genres == nil {
		raw.Genres = []Genre{}
	}
	detail := &Detail{
		Item:        Item{ID: id, Kind: kind, Title: title, OriginalTitle: original, Overview: raw.Overview, PosterURL: imageURL(raw.Poster, "w500"), Date: date, Rating: raw.Rating, Votes: raw.Votes},
		BackdropURL: imageURL(raw.Backdrop, "w1280"), Tagline: raw.Tagline, Status: raw.Status, Runtime: raw.Runtime, EpisodeCount: raw.EpisodeCount,
		Genres: raw.Genres, Countries: make([]string, 0, len(raw.Countries)), Cast: make([]CastMember, 0, min(len(cast), 20)),
		Seasons: make([]SeasonSummary, 0, len(raw.Seasons)), Collection: raw.Collection,
		IMDBID: validIMDBID(raw.ExternalIDs.IMDB), IMDBURL: imdbURL(raw.ExternalIDs.IMDB), TMDBURL: "https://www.themoviedb.org" + endpoint + "?language=zh-CN",
	}
	for _, country := range raw.Countries {
		detail.Countries = append(detail.Countries, country.Name)
		if country.Code != "" {
			detail.CountryCodes = append(detail.CountryCodes, country.Code)
		}
	}
	for _, member := range cast {
		if len(detail.Cast) == 20 {
			break
		}
		if member.ID < 1 || member.Name == "" {
			continue
		}
		character := member.Character
		if len(member.Roles) > 0 {
			character = member.Roles[0].Character
		}
		detail.Cast = append(detail.Cast, CastMember{ID: member.ID, Name: member.Name, Character: character, ProfileURL: imageURL(member.Profile, "w185")})
	}
	for _, season := range raw.Seasons {
		if season.Number < 0 {
			continue
		}
		detail.Seasons = append(detail.Seasons, SeasonSummary{ID: season.ID, Number: season.Number, Name: season.Name, PosterURL: imageURL(season.Poster, "w500"), EpisodeCount: season.EpisodeCount, Date: season.Date})
	}
	return detail, nil
}

// Season 仅在用户展开某季时查询，不在详情页一次拉取所有集数。
func (c *Client) Season(ctx context.Context, id, number int) (*SeasonDetail, error) {
	if id < 1 || number < 0 || number > 10000 {
		return nil, ErrInput
	}
	var raw struct {
		ID       int    `json:"id"`
		Number   int    `json:"season_number"`
		Name     string `json:"name"`
		Overview string `json:"overview"`
		Episodes []struct {
			ID       int     `json:"id"`
			Number   int     `json:"episode_number"`
			Name     string  `json:"name"`
			Overview string  `json:"overview"`
			Date     string  `json:"air_date"`
			Runtime  int     `json:"runtime"`
			Rating   float64 `json:"vote_average"`
			Votes    int     `json:"vote_count"`
			Still    string  `json:"still_path"`
		} `json:"episodes"`
	}
	endpoint := "/tv/" + strconv.Itoa(id) + "/season/" + strconv.Itoa(number)
	// TMDB 按季返回全部分集，数千集的长季需要高于普通列表的读取上限。
	if err := c.getLimited(ctx, endpoint, url.Values{"language": {"zh-CN"}}, &raw, seasonResponseLimit); err != nil {
		return nil, err
	}
	if raw.ID < 1 || raw.Number != number || raw.Episodes == nil {
		return nil, fmt.Errorf("TMDB 未返回有效的季信息")
	}
	season := &SeasonDetail{ID: raw.ID, Number: number, Name: raw.Name, Overview: raw.Overview, Episodes: make([]Episode, 0, len(raw.Episodes))}
	for _, episode := range raw.Episodes {
		season.Episodes = append(season.Episodes, Episode{ID: episode.ID, Number: episode.Number, SourceSeason: number, SourceNumber: episode.Number, Name: episode.Name, Overview: episode.Overview, Date: episode.Date, Runtime: episode.Runtime, Rating: episode.Rating, Votes: episode.Votes, StillURL: imageURL(episode.Still, "w300")})
	}
	return season, nil
}
