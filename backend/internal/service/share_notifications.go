package service

import "context"

// ShareSuccessNotifier 只接收完成后的分享信息，不接收 Cookie 或账号配置。
type ShareSuccessNotifier interface {
	NotifyShareSuccess(context.Context, string, string, GenerateInput, *GenerationResult, error) error
}

type ShareBatchSuccessNotifier interface {
	NotifyShareBatchSuccess(context.Context, string, string, []GenerateInput, []BatchOutcome) error
}

type shareReplyManagedKey struct{}
type shareBatchNotificationManagedKey struct{}

// WithShareReplyManaged 表示调用者会更新原 Telegram 回执，避免再次发送独立通知。
// 标记只属于当前调用；后台审核重试会重新走统一通知。
func WithShareReplyManaged(ctx context.Context) context.Context {
	return context.WithValue(ctx, shareReplyManagedKey{}, true)
}

func withShareBatchNotificationManaged(ctx context.Context) context.Context {
	return context.WithValue(ctx, shareBatchNotificationManagedKey{}, true)
}

func (s *ShareTasks) notifySuccess(ctx context.Context, taskID string, input GenerateInput, result *GenerationResult, generationErr error) {
	managed, _ := ctx.Value(shareReplyManagedKey{}).(bool)
	batchManaged, _ := ctx.Value(shareBatchNotificationManagedKey{}).(bool)
	if s.Notifier == nil || managed || batchManaged || result == nil || result.Written < 1 {
		return
	}
	settings, _ := ctx.Value(automationContextKey{}).(AutomationSettings)
	if !settings.TelegramEnabled {
		return
	}
	if err := s.Notifier.NotifyShareSuccess(ctx, settings.BotID, taskID, input, result, generationErr); err != nil {
		// 通知属于独立副作用，不能触发重新生成或重复转存。
		if s.Logs != nil {
			s.Logs.Append("Telegram", taskID, "生成通知", "ERROR", "STRM 已生成，但通知未能入队："+err.Error())
		}
	}
}

func (s *ShareTasks) notifyBatchSuccess(ctx context.Context, taskID string, requests []shareTaskRequest, outcomes []BatchOutcome) {
	managed, _ := ctx.Value(shareReplyManagedKey{}).(bool)
	notifier, ok := s.Notifier.(ShareBatchSuccessNotifier)
	if managed || !ok || len(requests) < 2 || len(requests) != len(outcomes) {
		return
	}
	settings := requests[0].Settings
	if !settings.TelegramEnabled {
		return
	}
	written := 0
	inputs := make([]GenerateInput, len(requests))
	for i := range requests {
		inputs[i] = requests[i].Input
		if outcomes[i].Result != nil {
			written += outcomes[i].Result.Written
		}
	}
	if written < 1 {
		return
	}
	if err := notifier.NotifyShareBatchSuccess(ctx, settings.BotID, taskID, inputs, outcomes); err != nil && s.Logs != nil {
		s.Logs.Append("Telegram", taskID, "生成通知", "ERROR", "STRM 批次已生成，但汇总通知未能入队："+err.Error())
	}
}
