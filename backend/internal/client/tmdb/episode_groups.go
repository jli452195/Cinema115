package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

// NumberingInfo identifies an administrator's durable choice, independently of caches.
type NumberingInfo struct {
	GroupID  string `json:"groupId"`
	Name     string `json:"name"`
	Type     int    `json:"type"`
	Revision int64  `json:"revision"`
	SavedAt  int64  `json:"savedAt,omitempty"`
}

type EpisodeGroupSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Type         int    `json:"type"`
	GroupCount   int    `json:"groupCount"`
	EpisodeCount int    `json:"episodeCount"`
}

type EpisodeGroup struct {
	EpisodeGroupSummary
	Seasons []SeasonDetail `json:"seasons"`
}

func ValidEpisodeGroupID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func (c *Client) EpisodeGroups(ctx context.Context, id int) ([]EpisodeGroupSummary, error) {
	if id < 1 {
		return nil, ErrInput
	}
	var raw struct {
		ID      int `json:"id"`
		Results []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Description  string `json:"description"`
			Type         int    `json:"type"`
			GroupCount   int    `json:"group_count"`
			EpisodeCount int    `json:"episode_count"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/tv/"+strconv.Itoa(id)+"/episode_groups", url.Values{"language": {"zh-CN"}}, &raw); err != nil {
		return nil, err
	}
	if raw.ID != id || raw.Results == nil {
		return nil, fmt.Errorf("TMDB 未返回有效的剧集组列表")
	}
	result := make([]EpisodeGroupSummary, 0, len(raw.Results))
	for _, item := range raw.Results {
		if !ValidEpisodeGroupID(item.ID) || item.Name == "" {
			continue
		}
		result = append(result, EpisodeGroupSummary{ID: item.ID, Name: item.Name, Description: item.Description, Type: item.Type, GroupCount: item.GroupCount, EpisodeCount: item.EpisodeCount})
	}
	return result, nil
}

// Group order is the season number (including season zero); episode order is zero based.
// Every entry retains its original identity: a group is an ordering, not a merge map.
func (c *Client) EpisodeGroup(ctx context.Context, seriesID int, id string) (*EpisodeGroup, error) {
	if seriesID < 1 || !ValidEpisodeGroupID(id) {
		return nil, ErrInput
	}
	var raw struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		Type         int    `json:"type"`
		GroupCount   int    `json:"group_count"`
		EpisodeCount int    `json:"episode_count"`
		Groups       []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Order    int    `json:"order"`
			Episodes []struct {
				ID       int     `json:"id"`
				Order    int     `json:"order"`
				ShowID   int     `json:"show_id"`
				Season   int     `json:"season_number"`
				Number   int     `json:"episode_number"`
				Name     string  `json:"name"`
				Overview string  `json:"overview"`
				Date     string  `json:"air_date"`
				Runtime  int     `json:"runtime"`
				Rating   float64 `json:"vote_average"`
				Votes    int     `json:"vote_count"`
				Still    string  `json:"still_path"`
			} `json:"episodes"`
		} `json:"groups"`
	}
	if err := c.getLimited(ctx, "/tv/episode_group/"+id, url.Values{"language": {"zh-CN"}}, &raw, seasonResponseLimit); err != nil {
		return nil, err
	}
	if raw.ID != id || raw.Name == "" || len(raw.Groups) == 0 || len(raw.Groups) > 1001 {
		return nil, fmt.Errorf("TMDB 剧集组结构无效")
	}
	result := &EpisodeGroup{EpisodeGroupSummary: EpisodeGroupSummary{ID: id, Name: raw.Name, Description: raw.Description, Type: raw.Type}, Seasons: make([]SeasonDetail, 0, len(raw.Groups))}
	seasons, identities := make(map[int]bool, len(raw.Groups)), make(map[int]bool)
	for _, group := range raw.Groups {
		if group.Order < 0 || group.Order > 1000 || seasons[group.Order] || len(group.Episodes) == 0 {
			return nil, fmt.Errorf("TMDB 剧集组季编号重复或无效")
		}
		seasons[group.Order] = true
		season := SeasonDetail{ID: group.Order, Number: group.Order, Name: group.Name, Episodes: make([]Episode, 0, len(group.Episodes))}
		numbers := make(map[int]bool, len(group.Episodes))
		for _, ep := range group.Episodes {
			if ep.ShowID != seriesID || ep.ID < 1 || ep.Order < 0 || ep.Order >= 10000 || numbers[ep.Order] || identities[ep.ID] || ep.Season < 0 || ep.Number < 1 {
				return nil, fmt.Errorf("TMDB 剧集组分集归属或编号无效")
			}
			numbers[ep.Order], identities[ep.ID] = true, true
			season.Episodes = append(season.Episodes, Episode{ID: ep.ID, Number: ep.Order + 1, SourceSeason: ep.Season, SourceNumber: ep.Number, Name: ep.Name, Overview: ep.Overview, Date: ep.Date, Runtime: ep.Runtime, Rating: ep.Rating, Votes: ep.Votes, StillURL: imageURL(ep.Still, "w300")})
		}
		sort.Slice(season.Episodes, func(i, j int) bool { return season.Episodes[i].Number < season.Episodes[j].Number })
		for i, ep := range season.Episodes {
			if ep.Number != i+1 {
				return nil, fmt.Errorf("TMDB 剧集组分集顺序不完整")
			}
		}
		result.EpisodeCount += len(season.Episodes)
		result.Seasons = append(result.Seasons, season)
	}
	if raw.EpisodeCount != result.EpisodeCount || raw.GroupCount != len(result.Seasons) {
		return nil, fmt.Errorf("TMDB 剧集组数量与分集内容不一致")
	}
	result.GroupCount = len(result.Seasons)
	sort.Slice(result.Seasons, func(i, j int) bool { return result.Seasons[i].Number < result.Seasons[j].Number })
	return result, nil
}
