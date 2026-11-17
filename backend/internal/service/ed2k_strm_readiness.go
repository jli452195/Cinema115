package service

import (
	"context"
	"errors"
	"fmt"

	"strm115/backend/internal/storage"
)

// CheckImportReady 仅检查本地服务配置与主账号，不读取网盘或建立离线、输出任务。
func (s *ED2KSTRMService) CheckImportReady(ctx context.Context) error {
	if s.Store == nil || s.Accounts == nil {
		return generationInputError("ED2K STRM 模块尚未就绪")
	}
	settings, err := s.Store.GetED2KSTRMSettings(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		return generationInputError("请先保存 ED2K STRM 服务地址")
	}
	if err != nil {
		return fmt.Errorf("检查 ED2K STRM 服务配置: %w", err)
	}
	if _, err := ValidateBaseURL(settings.BaseURL); err != nil {
		return generationInputError("请先保存有效的 ED2K STRM 服务地址")
	}
	return s.CheckCacheReady(settings)
}
