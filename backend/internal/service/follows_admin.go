package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"strm115/backend/internal/model"
)

var ErrFollowInput = errors.New("追更配置无效")

func (s *FollowService) AdminList(ctx context.Context, kind, status, query string, offset int) ([]model.AdminFollow, bool, error) {
	items, err := s.Store.AdminFollows(ctx, kind, status, query, offset)
	if err != nil {
		return nil, false, err
	}
	more := len(items) > model.FollowPageSize
	if more {
		items = items[:model.FollowPageSize]
	}
	media := make([]model.Follow, 0, len(items))
	for _, f := range items {
		media = append(media, f.Follow)
	}
	if err := s.attachProgress(ctx, media); err != nil {
		return nil, false, err
	}
	states, err := s.libraryStates(ctx, media)
	if err != nil {
		states = nil
	}
	for i := range items {
		items[i].Progress = media[i].Progress
		items[i].LibraryState = states[items[i].Kind+":"+strconv.Itoa(items[i].TMDBID)]
		if items[i].LibraryState == "" {
			items[i].LibraryState = "unknown"
		}
	}
	return items, more, nil
}
func (s *FollowService) Update(ctx context.Context, kind string, id int, status, note string, revision int64) error {
	note = strings.TrimSpace(note)
	// 处理状态不代表入库完成，禁止手动把未确认入库的影视标记完成。
	if (status != "pending" && status != "processing" && status != "closed") || revision < 1 || !utf8.ValidString(note) || utf8.RuneCountInString(note) > 500 || status == "closed" && note == "" {
		return fmt.Errorf("%w：状态仅支持待处理、处理中或已关闭；关闭原因必填，说明最多 500 字", ErrFollowInput)
	}
	if err := s.Store.UpdateFollow(ctx, kind, id, status, note, revision); err != nil {
		return err
	}
	if s.Logs != nil {
		message := "管理员更新追更状态为" + status
		if status == "closed" {
			message = "管理员关闭追更搜索"
		}
		s.Logs.Append("订阅", kind+":"+strconv.Itoa(id), "订阅", "INFO", message)
	}
	return nil
}
func (s *FollowService) SaveBaseURL(ctx context.Context, value string) error {
	settings, err := s.Store.FollowSettings(ctx)
	if err != nil {
		return err
	}
	settings.BaseURL = value
	return s.SaveSettings(ctx, settings)
}

func validateFollowBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" {
		u, err := url.Parse(value)
		if err != nil || len(value) > 2048 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return "", fmt.Errorf("%w：请输入 http 或 https 网站根地址，不包含路径、账号和查询参数", ErrFollowInput)
		}
		value = strings.TrimSuffix(u.String(), "/")
	}
	return value, nil
}

func (s *FollowService) SaveSettings(ctx context.Context, value model.FollowSettings) error {
	baseURL, err := validateFollowBaseURL(value.BaseURL)
	if err != nil {
		return err
	}
	value.BaseURL = baseURL
	if value.TelegramEnabled {
		if s.ValidateBot == nil {
			return fmt.Errorf("%w：Telegram Bot 校验服务尚未初始化", ErrFollowInput)
		}
		if err := s.ValidateBot(value.BotID, true); err != nil {
			return fmt.Errorf("%w：%v", ErrFollowInput, err)
		}
	}
	if value.AutoEnabled {
		if s.GenerationSettings == nil {
			return fmt.Errorf("%w：115分享STRM 服务尚未初始化", ErrFollowInput)
		}
		if err := s.GenerationSettings.ValidateCurrentAccount(); err != nil {
			return fmt.Errorf("%w：请先在 115分享STRM 中配置账号：%v", ErrFollowInput, err)
		}
	}
	return s.Store.SaveFollowSettings(ctx, value)
}
