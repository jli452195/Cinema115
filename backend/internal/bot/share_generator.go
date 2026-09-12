// Package bot 将 Telegram 消息转换为网页共用的分享生成任务。
package bot

import (
	"context"
	"strm115/backend/internal/service"
)

// ShareGenerator 与网页复用同一生成服务。
type ShareGenerator interface {
	Generate(context.Context, service.GenerateInput) (*service.GenerationResult, error)
}

var _ ShareGenerator = (*service.STRMGenerator)(nil)
