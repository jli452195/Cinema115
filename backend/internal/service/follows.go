package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

var ErrFollowAvailable = errors.New("电影已入库，无需申请追更")
var ErrFollowMetadata = errors.New("无法读取影视信息，请稍后重试")
var ErrFollowLibrary = errors.New("无法确认入库状态，请稍后重试")

type FollowDetails interface {
	Details(context.Context, string, int) (*tmdb.Detail, error)
}
type FollowLibrary interface {
	Matches(context.Context, []model.EmbyMatch, bool) ([]model.EmbyMatch, error)
}
type FollowService struct {
	Store              *storage.Store
	TMDB               FollowDetails
	Library            FollowLibrary
	Notifications      FollowNotificationFactory
	BotSearch          FollowBotSearcher
	ShareRuns          *ShareRuns
	GenerationSettings *AutomationStore
	Logs               *GenerationLogs
}

// 通知工厂只快照目标并格式化消息，不进行网络发送。
type FollowNotificationFactory interface {
	PrepareFollowNotification(model.Follow, string) (*model.TelegramNotification, error)
	FollowNotificationsConfigured() bool
}

func (s *FollowService) libraryStates(ctx context.Context, items []model.Follow) (map[string]string, error) {
	queries := make([]model.EmbyMatch, 0, len(items))
	for _, f := range items {
		queries = append(queries, model.EmbyMatch{Kind: f.Kind, TMDB: strconv.Itoa(f.TMDBID)})
	}
	states := make(map[string]string, len(items))
	if len(queries) == 0 {
		return states, nil
	}
	matches, err := s.Library.Matches(ctx, queries, false)
	if err != nil {
		return nil, fmt.Errorf("读取追更入库状态: %w", err)
	}
	for _, m := range matches {
		states[m.Kind+":"+m.TMDB] = m.State
	}
	return states, nil
}
func (s *FollowService) State(ctx context.Context, userID, kind string, id int) (model.FollowState, error) {
	f, err := s.Store.Follow(ctx, userID, kind, id)
	if err != nil {
		return model.FollowState{}, err
	}
	state := model.FollowState{Subscribed: f != nil, Request: f, LibraryState: "unknown"}
	states, err := s.libraryStates(ctx, []model.Follow{{Kind: kind, TMDBID: id}})
	// 查询入库失败不隐藏已保存的申请，也不能误报为未入库。
	if err == nil && states[kind+":"+strconv.Itoa(id)] != "" {
		state.LibraryState = states[kind+":"+strconv.Itoa(id)]
	}
	if f != nil {
		f.LibraryState = state.LibraryState
		items := []model.Follow{*f}
		if err := s.attachProgress(ctx, items); err != nil {
			return state, err
		}
		f.Progress = items[0].Progress
	}
	return state, nil
}
func (s *FollowService) Subscribe(ctx context.Context, userID, kind string, id int) (model.FollowState, error) {
	existing, err := s.Store.Follow(ctx, userID, kind, id)
	state := model.FollowState{LibraryState: "unknown"}
	if err != nil {
		return state, err
	}
	if existing != nil {
		return s.State(ctx, userID, kind, id)
	}
	if kind == "movie" {
		states, err := s.libraryStates(ctx, []model.Follow{{Kind: kind, TMDBID: id}})
		if err != nil {
			return state, ErrFollowLibrary
		}
		if states[kind+":"+strconv.Itoa(id)] == "present" {
			return state, ErrFollowAvailable
		}
	}
	detail, err := s.TMDB.Details(ctx, kind, id)
	if errors.Is(err, tmdb.ErrNotFound) {
		return state, tmdb.ErrNotFound
	}
	if err != nil || detail == nil || detail.ID != id || detail.Kind != kind || detail.Title == "" {
		return state, ErrFollowMetadata
	}
	f := model.Follow{Kind: kind, TMDBID: id, Title: detail.Title, PosterURL: detail.PosterURL, Date: detail.Date}
	var notification *model.TelegramNotification
	if s.Notifications != nil {
		baseURL, err := s.Store.FollowBaseURL(ctx)
		if err != nil {
			return state, err
		}
		notification, err = s.Notifications.PrepareFollowNotification(f, baseURL)
		if err != nil {
			return state, err
		}
	}
	if err := s.Store.SubscribeWithConfiguredAutomation(ctx, userID, f, notification); err != nil {
		return state, err
	}
	if s.Logs != nil {
		s.Logs.Append("订阅", kind+":"+strconv.Itoa(id), "订阅", "INFO", "订阅已保存；首位订阅者创建的自动任务将先检查入库状态")
	}
	return s.State(ctx, userID, kind, id)
}
func (s *FollowService) List(ctx context.Context, userID, kind, status string, offset int) ([]model.Follow, bool, error) {
	items, err := s.Store.Follows(ctx, userID, kind, status, offset)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(items) > model.FollowPageSize
	if hasMore {
		items = items[:model.FollowPageSize]
	}
	states, err := s.libraryStates(ctx, items)
	if err != nil {
		// 入库服务异常时仍返回持久化申请，入库标识降级为未知。
		states = nil
	}
	for i := range items {
		items[i].LibraryState = states[items[i].Kind+":"+strconv.Itoa(items[i].TMDBID)]
		if items[i].LibraryState == "" {
			items[i].LibraryState = "unknown"
		}
	}
	if err := s.attachProgress(ctx, items); err != nil {
		return nil, false, err
	}
	return items, hasMore, nil
}
