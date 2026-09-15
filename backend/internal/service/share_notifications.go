package service

import "context"

// ShareSuccessNotifier 只接收完成后的分享信息，不接收 Cookie 或账号配置。
type ShareSuccessNotifier interface {
	NotifyShareSuccess(context.Context, string, GenerateInput, *GenerationResult) error
}

type shareReplyManagedKey struct{}

// WithShareReplyManaged 表示调用者会更新原 Telegram 回执，避免再次发送独立通知。
// 标记只属于当前调用；后台审核重试会重新走统一通知。
func WithShareReplyManaged(ctx context.Context) context.Context {
	return context.WithValue(ctx, shareReplyManagedKey{}, true)
}

func (s *ShareTasks) notifySuccess(ctx context.Context, taskID string, input GenerateInput, result *GenerationResult) {
	managed, _ := ctx.Value(shareReplyManagedKey{}).(bool)
	if s.Notifier == nil || managed || result == nil || result.Written < 1 || result.Failed > 0 || result.MetadataFailed > 0 {
		return
	}
	if err := s.Notifier.NotifyShareSuccess(ctx, taskID, input, result); err != nil {
		// 通知属于独立副作用，不能触发重新生成或重复转存。
		s.Logs.Append("Telegram", taskID, "生成通知", "ERROR", "STRM 已生成，但通知未能入队："+err.Error())
	}
}
