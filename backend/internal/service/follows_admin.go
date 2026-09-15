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
	more := len(items) > 20
	if more {
		items = items[:20]
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
	if (status != "pending" && status != "processing") || revision < 1 || !utf8.ValidString(note) || utf8.RuneCountInString(note) > 500 {
		return fmt.Errorf("%w：状态仅支持待处理或处理中，说明最多 500 字", ErrFollowInput)
	}
	return s.Store.UpdateFollow(ctx, kind, id, status, note, revision)
}
func (s *FollowService) SaveBaseURL(ctx context.Context, value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		u, err := url.Parse(value)
		if err != nil || len(value) > 2048 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("%w：请输入 http 或 https 网站根地址，不包含路径、账号和查询参数", ErrFollowInput)
		}
		value = strings.TrimSuffix(u.String(), "/")
	}
	return s.Store.SaveFollowBaseURL(ctx, value)
}
