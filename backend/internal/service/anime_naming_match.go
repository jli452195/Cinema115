package service

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"time"

	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/bangumidata"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

// SearchSubject 优先读取精确条目关联；TMDB 临时不可用时保留关联，缺少关联再按名称搜索。
func (s *AnimeNamingService) SearchSubject(ctx context.Context, id string, names ...string) (AnimeNamingSearch, error) {
	if s == nil || s.Metadata == nil {
		return AnimeNamingSearch{Items: []tmdb.Item{}}, tmdb.ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	notice := ""
	if s != nil && s.Links != nil {
		linkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		links, err := s.Links.Lookup(linkCtx, id)
		cancel()
		if err != nil || links.Stale {
			notice = "bangumi-data 暂时无法核验，已使用名称搜索，请核对作品。"
		} else if len(links.Candidates) > 0 {
			out := AnimeNamingSearch{Items: []tmdb.Item{}, Source: "bangumi-data"}
			seen := make(map[string]bool, len(links.Candidates))
			failed := false
			unavailable := false
			for _, candidate := range links.Candidates {
				if seen[candidate.TMDBID] {
					continue
				}
				seen[candidate.TMDBID] = true
				if len(seen) > 12 {
					failed = true
					break
				}
				n, err := strconv.Atoi(candidate.TMDBID)
				if err != nil || n < 1 {
					failed = true
					continue
				}
				detail, err := s.Metadata.AnimeDetails(ctx, n)
				if err != nil && !errors.Is(err, tmdb.ErrNotFound) || err == nil && detail != nil && detail.ID == n && detail.CacheStale {
					failed, unavailable = true, true
					item := tmdb.Item{ID: n, Kind: "tv", Title: candidate.Title}
					if detail != nil && detail.ID == n {
						item = detail.Item
						item.Kind = "tv"
					}
					out.Items = append(out.Items, item)
					continue
				}
				if err != nil || detail == nil || detail.CacheStale || detail.ID != n || detail.Title == "" {
					failed = true
					continue
				}
				item := detail.Item
				item.Kind = "tv"
				out.Items = append(out.Items, item)
			}
			if len(out.Items) > 0 {
				if len(out.Items) == 1 && !failed {
					out.RecommendedID = strconv.Itoa(out.Items[0].ID)
				}
				if failed {
					out.Notice = "部分 bangumi-data 关联未能通过 TMDB 核验，请手动选择作品。"
				}
				if unavailable {
					out.Notice = "已保留 bangumi-data 精确关联；TMDB 资料暂时无法读取，请稍后重新读取目标资料。"
				}
				return out, nil
			}
			notice = "bangumi-data 关联未能通过 TMDB 核验，已改用名称搜索，请核对作品。"
		}
	}
	out, err := s.Search(ctx, names...)
	out.Source, out.Notice = "name", notice
	return out, err
}

// AnimeNamingTargetView 的推导结果仅供配置页参考，不写入命名快照和指纹。
type AnimeNamingTargetView struct {
	model.AnimeNamingTarget
	ChapterMapping *AnimeEpisodeSuggestion `json:"chapterMapping,omitempty"`
}

func animeNamingEpisodes(raw []tmdb.Episode) []model.AnimeNamingEpisode {
	out := make([]model.AnimeNamingEpisode, 0, len(raw))
	for _, episode := range raw {
		out = append(out, model.AnimeNamingEpisode{ID: strconv.Itoa(episode.ID), Number: episode.Number, Name: episode.Name, Date: episode.Date, SourceSeason: episode.SourceSeason, SourceNumber: episode.SourceNumber})
	}
	return out
}

// 只核验与命名指纹相同的季表，避免缓存刷新期间把旧建议附到新快照。
func (s *AnimeNamingService) currentAnimeSeason(ctx context.Context, target model.AnimeNamingTarget) bool {
	id, _ := strconv.Atoi(target.TMDBID)
	detail, err := s.Metadata.AnimeDetails(ctx, id)
	if err != nil || detail == nil || detail.CacheStale || detail.ID != id {
		return false
	}
	var raw *tmdb.SeasonDetail
	if target.GroupID == "" {
		season, err := s.Metadata.AnimeSeason(ctx, id, target.Season)
		if err != nil || season == nil || season.CacheStale || season.Number != target.Season {
			return false
		}
		raw = season
	} else {
		group, err := s.Metadata.AnimeGroup(ctx, id, target.GroupID)
		if err != nil || group == nil || group.CacheStale {
			return false
		}
		for i := range group.Seasons {
			if group.Seasons[i].Number == target.Season {
				raw = &group.Seasons[i]
				break
			}
		}
	}
	if raw == nil {
		return false
	}
	for _, selected := range target.Seasons {
		if selected.Number == target.Season {
			return slices.Equal(selected.Episodes, animeNamingEpisodes(raw.Episodes))
		}
	}
	return false
}

// TargetWithChapters 先读取正常命名快照，再用公共章节核验编号；推导失败不妨碍手动配置。
func (s *AnimeNamingService) TargetWithChapters(ctx context.Context, discovery AnimeEpisodeDiscovery, in AnimeNamingTargetInput, date string, names ...string) (AnimeNamingTargetView, error) {
	target, err := s.Target(ctx, in, date, names...)
	view := AnimeNamingTargetView{AnimeNamingTarget: target}
	if err != nil || in.BangumiID == "" {
		return view, err
	}
	if discovery == nil {
		view.ChapterMapping = &AnimeEpisodeSuggestion{Status: "unresolved", Reason: "Bangumi 章节读取不可用，请手动核对集数偏移。"}
		return view, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	unavailable := func(reason string) (AnimeNamingTargetView, error) {
		view.ChapterMapping = &AnimeEpisodeSuggestion{Status: "unresolved", Reason: reason}
		return view, nil
	}
	chapters := make([]bangumi.Episode, 0, bangumi.EpisodePageSize)
	total := -1
	// 自动建议最多读取三页；超长篇章仍可在章节弹层按页查看并手动设置。
	for offset := 0; offset < 3*bangumi.EpisodePageSize; offset += bangumi.EpisodePageSize {
		page, err := discovery.Episodes(readCtx, in.BangumiID, offset)
		if err != nil || page.Stale || page.Issue != nil {
			return unavailable("Bangumi 章节暂时无法核验，当前目标已保留，请手动核对集数偏移。")
		}
		if total < 0 {
			total = page.Total
		}
		if total > 3*bangumi.EpisodePageSize || page.Total != total || page.Offset != offset || len(page.Items) != min(bangumi.EpisodePageSize, max(0, total-offset)) {
			return unavailable("章节数量过多或读取期间资料变化，请查看 Bangumi 集数并手动核对。")
		}
		chapters = append(chapters, page.Items...)
		if len(chapters) >= total {
			break
		}
	}
	if len(chapters) == 0 {
		return unavailable("Bangumi 暂未收录正片章节，无法推导集数对应关系。")
	}
	seasons := make([]model.AnimeNamingSeason, 0, 4)
	if !s.currentAnimeSeason(readCtx, target) {
		return unavailable("当前 TMDB 资料暂时无法核验，请重新读取目标资料后重试。")
	}
	for _, season := range target.Seasons {
		if season.Number == target.Season {
			seasons = append(seasons, season)
		}
	}
	// 用户明确选择的季和自定义编排只核验当前季，避免推导替换手动选择。
	var links bangumidata.Result
	if in.GroupID == "" {
		hint := -1
		if s.Links != nil {
			linkCtx, stop := context.WithTimeout(readCtx, time.Second)
			result, linkErr := s.Links.Lookup(linkCtx, in.BangumiID)
			stop()
			if linkErr == nil && !result.Stale {
				links = result
				for _, candidate := range links.Candidates {
					if candidate.TMDBID == in.TMDBID && candidate.Season != nil {
						if hint >= 0 && hint != *candidate.Season {
							hint = -1
							break
						}
						hint = *candidate.Season
					}
				}
			}
		}
		others := make([]model.AnimeNamingSeason, 0, len(target.Seasons))
		for _, season := range target.Seasons {
			if (season.Number > 0 || season.Number == 0 && hint == 0) && season.Number != target.Season {
				others = append(others, season)
			}
		}
		if len(others) > 8 {
			return unavailable("TMDB 季数较多，尚未核对全部季集，不能确认唯一偏移。")
		}
		chapterDate := date
		for _, chapter := range chapters {
			if chapter.Date != "" {
				chapterDate = chapter.Date
				break
			}
		}
		distance := func(value string) time.Duration {
			a, e1 := time.Parse("2006-01-02", value)
			b, e2 := time.Parse("2006-01-02", chapterDate)
			if e1 != nil || e2 != nil {
				return time.Duration(1<<63 - 1)
			}
			d := a.Sub(b)
			if d < 0 {
				return -d
			}
			return d
		}
		sort.SliceStable(others, func(i, j int) bool {
			if (others[i].Number == hint) != (others[j].Number == hint) {
				return others[i].Number == hint
			}
			return distance(others[i].Date) < distance(others[j].Date)
		})
		id, _ := strconv.Atoi(in.TMDBID)
		for _, summary := range others {
			raw, err := s.Metadata.AnimeSeason(readCtx, id, summary.Number)
			if err != nil || raw == nil || raw.CacheStale || raw.Number != summary.Number || len(raw.Episodes) > 2000 {
				return unavailable("部分 TMDB 季资料暂时无法核验，请手动选择目标季后重试。")
			}
			summary.Episodes = animeNamingEpisodes(raw.Episodes)
			seasons = append(seasons, summary)
		}
	}
	suggestion := InferAnimeEpisodes(chapters, seasons)
	suggestion = animeLinkFirstEpisode(suggestion, links, in.TMDBID, chapters, seasons)
	if (suggestion.Status == "verified" || suggestion.Status == "partial") && suggestion.Season != target.Season && in.Season == nil {
		selected := suggestion.Season
		in.Season = &selected
		matched, err := s.Target(readCtx, in, date, names...)
		if err != nil {
			return unavailable("章节建议已找到，但目标季暂时无法读取，请手动选择后重试。")
		}
		view.AnimeNamingTarget = matched
		if !s.currentAnimeSeason(readCtx, matched) {
			return unavailable("目标季资料读取期间发生变化，请重新读取后核对。")
		}
		// 使用最终返回的季表重核一次，与其它已读取季共同排除歧义。
		for _, season := range matched.Seasons {
			if season.Number == matched.Season {
				for i := range seasons {
					if seasons[i].Number == matched.Season {
						seasons[i] = season
					}
				}
			}
		}
		suggestion = InferAnimeEpisodes(chapters, seasons)
		suggestion = animeLinkFirstEpisode(suggestion, links, in.TMDBID, chapters, seasons)
	}
	view.ChapterMapping = &suggestion
	return view, nil
}

// 首集链接与独立日期或具体标题核对后，只补充首集对应，不把整篇章标为已核对。
func animeLinkFirstEpisode(out AnimeEpisodeSuggestion, links bangumidata.Result, tmdbID string, chapters []bangumi.Episode, seasons []model.AnimeNamingSeason) AnimeEpisodeSuggestion {
	var link *bangumidata.Candidate
	conflict := func() AnimeEpisodeSuggestion {
		return AnimeEpisodeSuggestion{Status: "conflict", Reason: "bangumi-data 首集定位与章节资料不一致，请手动核对集数偏移。"}
	}
	for _, candidate := range links.Candidates {
		if candidate.TMDBID != tmdbID || candidate.Season == nil || candidate.Episode == nil {
			continue
		}
		if link != nil && (*link.Season != *candidate.Season || *link.Episode != *candidate.Episode) {
			return conflict()
		}
		copy := candidate
		link = &copy
	}
	if link == nil || out.Status == "conflict" {
		return out
	}
	points, first, _, ok := animeInferenceChapters(chapters)
	if !ok || len(points) == 0 {
		return out
	}
	if out.Status == "verified" || out.Status == "partial" {
		if out.Season != *link.Season || out.TargetFirst != *link.Episode {
			return conflict()
		}
		return out
	}
	// 特别篇由精确首集链接定位；普通多章节仍不以单点覆盖原有歧义。
	if len(points) != 1 && *link.Season != 0 {
		return out
	}
	for _, season := range seasons {
		if season.Number != *link.Season {
			continue
		}
		episodes, valid := animeInferenceTMDB(season.Episodes)
		if !valid {
			return out
		}
		chapterTitles, episodeTitles := animeTitleNumberIndex(points), animeTitleNumberIndex(episodes)
		chapter := points[0]
		for _, episode := range episodes {
			if episode.number != *link.Episode {
				continue
			}
			// 同日连播的特别篇通过精确链接区分首集，日期仅用于核对该定位。
			dateMatch := chapter.dateOK && episode.dateOK && animeDateDistance(chapter.date, episode.date) <= 24*time.Hour && (*link.Season == 0 || !animeDateHasAmbiguousMatch(chapter, points) && !animeDateHasAmbiguousMatch(episode, episodes))
			titleMatch := false
			for _, title := range chapter.titles {
				if len(chapterTitles[title]) == 1 && len(episodeTitles[title]) == 1 && episodeTitles[title][0] == episode.number {
					titleMatch = true
				}
			}
			if chapter.dateOK && episode.dateOK && animeDateDistance(chapter.date, episode.date) > 24*time.Hour {
				return conflict()
			}
			if !dateMatch && !titleMatch {
				return out
			}
			return AnimeEpisodeSuggestion{Status: "partial", Reason: "bangumi-data 首集定位已按日期或具体标题核对，仅确认首集对应", Season: season.Number, First: first, Last: first, TargetFirst: episode.number, TargetLast: episode.number, Offset: episode.number - first, Matched: 1, Compared: 1, Pairs: []AnimeEpisodeSuggestionPair{{BangumiNumber: first, TMDBEpisode: episode.number, Evidence: "bangumi-data,first"}}}
		}
	}
	return out
}
