package service

import (
	"context"
	"strings"
)

// telegramGenerationPinnedKey 标记 Bot 批次已经固定的内存凭据，不进入任务记录。
type telegramGenerationPinnedKey struct{}

// PrepareTelegramGeneration 在纯本地检查后固定批次设置和凭据，装饰器保留调用方上下文。
func (s *ShareTasks) PrepareTelegramGeneration(settings AutomationSettings) (func(context.Context) context.Context, error) {
	if s == nil || s.Settings == nil {
		return nil, generationInputError("分享 STRM 服务尚未初始化")
	}
	current, err := s.Settings.TelegramShareSettings(settings.BotID)
	if err != nil {
		return nil, err
	}
	if current != settings {
		return nil, generationInputError("分享 STRM 设置已变化，请重新选择处理方式")
	}
	cookie := ""
	if s.AccountCookie != nil {
		cookie = s.AccountCookie(settings.AccountID)
	} else if s.Cookie != nil {
		cookie = s.Cookie()
	}
	if strings.TrimSpace(cookie) == "" {
		return nil, generationInputError("原任务主账号已变化或未登录，请检查账号配置")
	}
	current, err = s.Settings.TelegramShareSettings(settings.BotID)
	if err != nil {
		return nil, err
	}
	if current != settings {
		return nil, generationInputError("分享 STRM 设置已变化，请重新选择处理方式")
	}
	return func(ctx context.Context) context.Context {
		ctx = context.WithValue(ctx, automationContextKey{}, settings)
		ctx = context.WithValue(ctx, generationCookieKey{}, cookie)
		ctx = context.WithValue(ctx, telegramGenerationPinnedKey{}, true)
		return s.withAccountCheck(ctx, settings.AccountID)
	}, nil
}

// PrepareTelegramGeneration 让 Bot 通过后台队列使用同一份固定批次凭据。
func (s *ShareRuns) PrepareTelegramGeneration(settings AutomationSettings) (func(context.Context) context.Context, error) {
	if s == nil || s.tasks == nil {
		return nil, generationInputError("分享 STRM 后台队列尚未初始化")
	}
	return s.tasks.PrepareTelegramGeneration(settings)
}
