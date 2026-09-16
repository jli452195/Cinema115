package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

type NotificationStore interface {
	EnqueueTelegramNotification(context.Context, model.TelegramNotification) error
	DueTelegramNotifications(context.Context, string, int64, time.Time) ([]model.TelegramNotification, error)
	CompleteTelegramNotification(context.Context, int64, bool) error
	RetryTelegramNotification(context.Context, int64, time.Time) error
	PruneTelegramNotifications(context.Context, time.Time) error
}

// NotifyShareSuccess 只进行本地入队，Telegram 网络请求不占用生成请求。
func (m *Manager) NotifyShareSuccess(ctx context.Context, taskID string, input service.GenerateInput, result *service.GenerationResult, generationErr error) error {
	text, preview := formatShareReply(input, service.AutomationSettings{}, result, generationErr, 1, 1)
	return m.enqueueShareNotification(ctx, taskID, text, preview)
}

func (m *Manager) NotifyShareBatchSuccess(ctx context.Context, taskID string, inputs []service.GenerateInput, outcomes []service.BatchOutcome) error {
	text, preview := formatMergedReply(inputs, outcomes)
	return m.enqueueShareNotification(ctx, taskID, text, preview)
}

func (m *Manager) enqueueShareNotification(ctx context.Context, taskID, text, preview string) error {
	m.mu.RLock()
	cfg := m.config
	m.mu.RUnlock()
	if cfg.Token == "" || cfg.NotificationChatID == "" {
		return nil
	}
	if m.Notifications == nil {
		return fmt.Errorf("通知存储未初始化")
	}
	chatID, err := strconv.ParseInt(cfg.NotificationChatID, 10, 64)
	if err != nil || chatID == 0 {
		return fmt.Errorf("通知聊天 ID 无效")
	}
	n := model.TelegramNotification{TaskID: taskID, BotID: strings.SplitN(cfg.Token, ":", 2)[0], ChatID: chatID, Text: text, PreviewURL: preview}
	if err := m.Notifications.EnqueueTelegramNotification(ctx, n); err != nil {
		return err
	}
	m.Logs.Append("Telegram", taskID, "生成通知", "DEBUG", "STRM 成功通知已入队")
	return nil
}

// RunNotifications 与消息接收独立，由主进程取消并等待退出。
// 每秒至多发送一条，通知失败只重试发送，不重新运行生成任务。
func (m *Manager) RunNotifications(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	nextPrune := time.Time{}
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		if m.Notifications != nil {
			if !now.Before(nextPrune) {
				if err := m.Notifications.PruneTelegramNotifications(ctx, now.Add(-7*24*time.Hour)); err != nil && ctx.Err() == nil {
					m.Logs.Append("Telegram", "", "生成通知", "ERROR", err.Error())
				}
				nextPrune = now.Add(time.Hour)
			}
			if err := m.deliverNotification(ctx, now); err != nil && ctx.Err() == nil {
				m.Logs.Append("Telegram", "", "生成通知", "ERROR", err.Error())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) deliverNotification(ctx context.Context, now time.Time) error {
	m.mu.RLock()
	cfg := m.config
	m.mu.RUnlock()
	if cfg.Token == "" || cfg.NotificationChatID == "" {
		return nil
	}
	botID := strings.SplitN(cfg.Token, ":", 2)[0]
	chatID, err := strconv.ParseInt(cfg.NotificationChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("通知聊天 ID 无效")
	}
	// 只取当前明确配置的目标，不能把旧通知转发给其他 Bot 或聊天。
	pending, err := m.Notifications.DueTelegramNotifications(ctx, botID, chatID, now)
	if err != nil || len(pending) == 0 {
		return err
	}
	n := pending[0]
	logKind, successMessage := notificationLabels(n.TaskID)
	cfg.AllowedChatIDs = cfg.NotificationChatID
	worker, err := m.newWorker(cfg)
	if err != nil || worker == nil {
		return fmt.Errorf("通知 Bot 配置无效")
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	reply := pendingReply{ChatID: n.ChatID, Text: n.Text, PreviewURL: n.PreviewURL}
	err = worker.call(sendCtx, "sendMessage", reply.request(), nil)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		if err := m.Notifications.CompleteTelegramNotification(ctx, n.ID, true); err != nil {
			return err
		}
		m.Logs.Append("Telegram", n.TaskID, logKind, "INFO", successMessage)
		return nil
	}
	if n.Attempts >= 7 {
		if saveErr := m.Notifications.CompleteTelegramNotification(ctx, n.ID, false); saveErr != nil {
			return saveErr
		}
		m.Logs.Append("Telegram", n.TaskID, logKind, "ERROR", "通知发送失败，已达到 8 次尝试上限："+err.Error())
		return nil
	}
	delay := min(30*time.Second*time.Duration(1<<n.Attempts), 15*time.Minute)
	if saveErr := m.Notifications.RetryTelegramNotification(ctx, n.ID, now.Add(delay)); saveErr != nil {
		return saveErr
	}
	m.Logs.Append("Telegram", n.TaskID, logKind, "WARNING", "通知发送失败，稍后自动重试："+err.Error())
	return nil
}
