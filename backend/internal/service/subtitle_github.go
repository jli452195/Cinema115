package service

import (
	"context"
	"fmt"
	"strings"

	"strm115/backend/internal/client/subshare"
)

type SubtitleGitHubView struct {
	Configured bool `json:"configured"`
}

type SubtitleGitHubSettings struct {
	gate    chan struct{}
	store   accountSecretStore
	library *subshare.Client
}

func NewSubtitleGitHubSettings(store accountSecretStore, library *subshare.Client) (*SubtitleGitHubSettings, error) {
	token := store.Value()
	if token != "" && !subshare.ValidToken(token) {
		return nil, fmt.Errorf("字幕配置中的 GitHub Token 格式无效")
	}
	library.SetToken(token)
	return &SubtitleGitHubSettings{gate: make(chan struct{}, 1), store: store, library: library}, nil
}

func (s *SubtitleGitHubSettings) View() SubtitleGitHubView {
	return SubtitleGitHubView{Configured: s.store.Value() != ""}
}

// 配置写入串行执行，等待与验证均响应请求取消。
func (s *SubtitleGitHubSettings) acquire(ctx context.Context) (func(), error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-s.gate
		return nil, err
	}
	return func() { <-s.gate }, nil
}

func (s *SubtitleGitHubSettings) Configure(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if !subshare.ValidToken(token) {
		return subshare.ErrInput
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.library.ValidateToken(ctx, token); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.store.Save(token); err != nil {
		return fmt.Errorf("保存 GitHub 字幕库配置: %w", err)
	}
	s.library.SetToken(token)
	return nil
}

func (s *SubtitleGitHubSettings) Clear(ctx context.Context) error {
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.store.Save(""); err != nil {
		return fmt.Errorf("清除 GitHub 字幕库配置: %w", err)
	}
	s.library.SetToken("")
	return nil
}
