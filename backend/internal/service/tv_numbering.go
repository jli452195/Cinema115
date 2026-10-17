package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/storage"
)

type tvNumberingStore interface {
	TVNumbering(context.Context, int) (storage.TVNumberingRecord, error)
	SaveTVNumbering(context.Context, int, storage.TVNumberingRecord, int64) error
}
type episodeGroupReader interface {
	EpisodeGroups(context.Context, int) ([]tmdb.EpisodeGroupSummary, error)
	EpisodeGroup(context.Context, int, string) (*tmdb.EpisodeGroup, error)
}
type numberingKey struct{ id int }
type numberingSelection struct {
	Info  tmdb.NumberingInfo
	Group *tmdb.EpisodeGroup
}

func (s *TMDBService) numbering(ctx context.Context, id int) (*numberingSelection, error) {
	if value, ok := ctx.Value(numberingKey{id}).(*numberingSelection); ok {
		return value, nil
	}
	value := &numberingSelection{Info: tmdb.NumberingInfo{Name: "TMDB 默认"}}
	db, ok := s.db.(tvNumberingStore)
	if !ok {
		return value, nil
	}
	r, err := db.TVNumbering(ctx, id)
	if err != nil {
		return nil, err
	}
	value.Info = tmdb.NumberingInfo{GroupID: r.GroupID, Name: r.Name, Type: r.Type, Revision: r.Revision, SavedAt: r.SavedAt}
	if r.GroupID == "" {
		value.Info.Name = "TMDB 默认"
		return value, nil
	}
	value.Group, err = decodeTMDB[tmdb.EpisodeGroup](r.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("读取已保存季集编排: %w", err)
	}
	if value.Group.ID != r.GroupID || len(value.Group.Seasons) == 0 {
		return nil, fmt.Errorf("已保存季集编排无效")
	}
	return value, nil
}

// A multi-call operation uses one immutable numbering selection, even across a save.
func (s *TMDBService) PinNumbering(ctx context.Context, id int) (context.Context, error) {
	value, err := s.numbering(ctx, id)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, numberingKey{id}, value), nil
}
func (s *TMDBService) NumberingRevision(ctx context.Context, id int) (int64, error) {
	value, err := s.numbering(ctx, id)
	if err != nil {
		return 0, err
	}
	return value.Info.Revision, nil
}
func pinNumbering(ctx context.Context, metadata any, id int) (context.Context, error) {
	if source, ok := metadata.(interface {
		PinNumbering(context.Context, int) (context.Context, error)
	}); ok {
		return source.PinNumbering(ctx, id)
	}
	return ctx, nil
}

func groupSeason(group *tmdb.EpisodeGroup, number int, info tmdb.NumberingInfo) (*tmdb.SeasonDetail, error) {
	for _, season := range group.Seasons {
		if season.Number == number {
			season.Episodes = append([]tmdb.Episode{}, season.Episodes...)
			season.CacheInfo = tmdb.CacheInfo{FetchedAt: info.SavedAt}
			season.NumberingRevision = info.Revision
			return &season, nil
		}
	}
	return nil, tmdb.ErrNotFound
}
func groupSummaries(group *tmdb.EpisodeGroup) []tmdb.SeasonSummary {
	result := make([]tmdb.SeasonSummary, 0, len(group.Seasons))
	for _, season := range group.Seasons {
		date := ""
		for _, episode := range season.Episodes {
			if episode.Date != "" && (date == "" || episode.Date < date) {
				date = episode.Date
			}
		}
		result = append(result, tmdb.SeasonSummary{ID: season.ID, Number: season.Number, Name: season.Name, EpisodeCount: len(season.Episodes), Date: date})
	}
	return result
}
func scheduleFromSeason(season *tmdb.SeasonDetail) *tmdb.Schedule {
	result := &tmdb.Schedule{CacheInfo: season.CacheInfo, NumberingRevision: season.NumberingRevision, Number: season.Number, Episodes: make([]tmdb.ScheduledEpisode, 0, len(season.Episodes))}
	for _, episode := range season.Episodes {
		result.Episodes = append(result.Episodes, tmdb.ScheduledEpisode{Number: episode.Number, Date: episode.Date})
	}
	return result
}

type NumberingChoices struct {
	Current             tmdb.NumberingInfo         `json:"current"`
	Snapshot            *tmdb.EpisodeGroup         `json:"snapshot"`
	DefaultSeasons      []tmdb.SeasonSummary       `json:"defaultSeasons"`
	DefaultEpisodeCount int                        `json:"defaultEpisodeCount"`
	Groups              []tmdb.EpisodeGroupSummary `json:"groups"`
	DefaultError        string                     `json:"defaultError,omitempty"`
	GroupsError         string                     `json:"groupsError,omitempty"`
}
type NumberingPreview struct {
	Group       *tmdb.EpisodeGroup `json:"group"`
	Fingerprint string             `json:"fingerprint"`
	Changed     bool               `json:"changed"`
}

func groupFingerprint(group *tmdb.EpisodeGroup) (string, error) {
	// Votes change frequently and do not affect an administrator's ordering decision.
	// Exclude them from preview conflicts without mutating the saved snapshot.
	var normalized *tmdb.EpisodeGroup
	if group != nil {
		copyGroup := *group
		copyGroup.Seasons = append([]tmdb.SeasonDetail{}, group.Seasons...)
		for i := range copyGroup.Seasons {
			season := &copyGroup.Seasons[i]
			season.CacheInfo = tmdb.CacheInfo{}
			season.NumberingRevision = 0
			season.Episodes = append([]tmdb.Episode{}, season.Episodes...)
			for j := range season.Episodes {
				season.Episodes[j].Rating, season.Episodes[j].Votes = 0, 0
			}
		}
		normalized = &copyGroup
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("编码季集预览: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func (s *TMDBService) NumberingChoices(ctx context.Context, id int) (*NumberingChoices, error) {
	if id < 1 {
		return nil, tmdb.ErrInput
	}
	selection, err := s.numbering(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &NumberingChoices{Current: selection.Info, Snapshot: selection.Group, Groups: []tmdb.EpisodeGroupSummary{}, DefaultSeasons: []tmdb.SeasonSummary{}}
	if source, ok := s.source.(episodeGroupReader); ok {
		groups, err := source.EpisodeGroups(tmdb.WithFresh(ctx), id)
		if err != nil {
			result.GroupsError = "TMDB 剧集组列表暂时不可用，已保存编排仍可查看或恢复默认。"
		} else {
			result.Groups = groups
		}
	} else {
		result.GroupsError = "TMDB 剧集组服务未配置，已保存编排仍可查看或恢复默认。"
	}
	detail, _, err := cachedTMDB[tmdb.Detail](ctx, s, "detail:v2:tv:"+strconv.Itoa(id), 6*time.Hour, func(ctx context.Context) (any, error) { return s.source.Details(ctx, "tv", id) })
	if err != nil {
		result.DefaultError = "TMDB 默认季集资料暂时不可用，恢复默认后将随常规资料刷新读取。"
	} else {
		result.DefaultSeasons, result.DefaultEpisodeCount = detail.Seasons, detail.EpisodeCount
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, nil
}
func (s *TMDBService) PreviewNumbering(ctx context.Context, id int, groupID string) (*NumberingPreview, error) {
	if id < 1 || !tmdb.ValidEpisodeGroupID(groupID) {
		return nil, tmdb.ErrInput
	}
	source, ok := s.source.(episodeGroupReader)
	if !ok {
		return nil, tmdb.ErrNotConfigured
	}
	// The group endpoint has no parent in its URL. Verify membership separately.
	groups, err := source.EpisodeGroups(ctx, id)
	if err != nil {
		return nil, err
	}
	found := false
	for _, group := range groups {
		if group.ID == groupID {
			found = true
			break
		}
	}
	if !found {
		return nil, tmdb.ErrNotFound
	}
	group, err := source.EpisodeGroup(tmdb.WithFresh(ctx), id, groupID)
	if err != nil {
		return nil, err
	}
	fingerprint, err := groupFingerprint(group)
	if err != nil {
		return nil, err
	}
	current, err := s.numbering(ctx, id)
	if err != nil {
		return nil, err
	}
	old, err := groupFingerprint(current.Group)
	if err != nil {
		return nil, err
	}
	return &NumberingPreview{Group: group, Fingerprint: fingerprint, Changed: old != fingerprint}, nil
}

type SaveNumberingInput struct {
	GroupID     string `json:"groupId"`
	Revision    int64  `json:"revision"`
	Fingerprint string `json:"fingerprint"`
}

func (s *TMDBService) SaveNumbering(ctx context.Context, id int, input SaveNumberingInput) (*tmdb.NumberingInfo, error) {
	if id < 1 || input.Revision < 0 || input.GroupID != "" && (!tmdb.ValidEpisodeGroupID(input.GroupID) || len(input.Fingerprint) != 64) {
		return nil, tmdb.ErrInput
	}
	db, ok := s.db.(tvNumberingStore)
	if !ok {
		return nil, tmdb.ErrNotConfigured
	}
	current, err := s.numbering(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.Info.Revision != input.Revision {
		return nil, storage.ErrNumberingConflict
	}
	r := storage.TVNumberingRecord{GroupID: input.GroupID, Name: "TMDB 默认", SavedAt: s.now().Unix(), Snapshot: []byte{}}
	if input.GroupID != "" {
		preview, err := s.PreviewNumbering(tmdb.WithFresh(ctx), id, input.GroupID)
		if err != nil {
			return nil, err
		}
		if preview.Fingerprint != input.Fingerprint {
			return nil, fmt.Errorf("%w：TMDB 内容已变化，请重新预览", storage.ErrNumberingConflict)
		}
		hasEpisodes := false
		for _, season := range preview.Group.Seasons {
			if len(season.Episodes) > 0 {
				hasEpisodes = true
				break
			}
		}
		if !hasEpisodes {
			return nil, fmt.Errorf("%w：剧集组暂无分集，无法保存", tmdb.ErrInput)
		}
		r.Name, r.Type = preview.Group.Name, preview.Group.Type
		raw, err := json.Marshal(preview.Group)
		if err != nil {
			return nil, fmt.Errorf("编码季集快照: %w", err)
		}
		if len(raw) > 8<<20 {
			return nil, fmt.Errorf("季集快照超过大小限制")
		}
		r.Snapshot, err = encodeTMDB(json.RawMessage(raw))
		if err != nil {
			return nil, err
		}
	}
	if err = db.SaveTVNumbering(ctx, id, r, input.Revision); err != nil {
		return nil, err
	}
	return &tmdb.NumberingInfo{GroupID: r.GroupID, Name: r.Name, Type: r.Type, Revision: input.Revision + 1, SavedAt: r.SavedAt}, nil
}
