package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	"strm115/backend/internal/service"
)

// 等待窗口从首条消息开始计算，持续发送不会无限延后；不同聊天和话题各自收集。
func (b *Telegram) enqueueUpdates(state *telegramState, updates []telegramUpdate, now time.Time) {
	settings := b.Settings.Value()
	for _, update := range updates {
		if update.ID < state.Offset {
			continue
		}
		state.Offset = update.ID + 1
		m := update.Message
		if m == nil || m.From.IsBot || !b.AllowedChats[m.Chat.ID] {
			continue
		}
		items := parseTelegramMessage(m)
		if len(items) > 0 {
			b.Logs.Append("Telegram", fmt.Sprintf("消息-%d", m.ID), "Bot", "INFO", fmt.Sprintf("收到分享消息，识别 %d 个链接并加入队列", len(items)))
		}
		for _, item := range items {
			mode := telegramShareMode(settings, item)
			wait := time.Duration(0)
			if mode == "reshare" {
				wait = time.Duration(settings.TGBatchWaitSeconds) * time.Second
			}
			var target *messageBatch
			for _, queued := range state.Queue {
				if queued.ChatID != m.Chat.ID || queued.ThreadID != m.ThreadID || queued.Mode != mode {
					continue
				}
				// 不等待的消息仍保留原来的单消息批次，跨消息不去重。
				if (wait > 0 && now.Before(queued.ReadyAt)) || queued.UpdateID == update.ID {
					target = queued
					break
				}
			}
			if target == nil {
				target = &messageBatch{UpdateID: update.ID, ChatID: m.Chat.ID, MessageID: m.ID, ThreadID: m.ThreadID, Mode: mode, ReadyAt: now.Add(wait)}
				state.Queue = append(state.Queue, target)
			}
			target.Items = append(target.Items, item)
			target.SourceMessageIDs = append(target.SourceMessageIDs, m.ID)
		}
	}
}

func telegramShareMode(settings service.AutomationSettings, _ messageShare) string {
	if !settings.ReshareEnabled {
		return "direct"
	}
	return "reshare"
}

// 普通分享可越过尚未到期的转存批次，执行中的云端操作仍保持串行。
func (b *Telegram) startReadyBatch(state *telegramState, now time.Time) bool {
	// 两类都已就绪时优先处理普通分享，不让它进入合并转存路径。
	for _, direct := range []bool{true, false} {
		for i, batch := range state.Queue {
			if (batch.Mode == "direct") != direct {
				continue
			}
			if now.Before(batch.ReadyAt) && b.AllowedChats[batch.ChatID] {
				continue
			}
			state.Queue = append(state.Queue[:i], state.Queue[i+1:]...)
			if !b.AllowedChats[batch.ChatID] {
				return true
			}
			state.Batch = batch
			mode := "普通分享"
			if batch.Mode == "reshare" {
				mode = "合并转存再分享"
			}
			b.Logs.Append("Telegram", fmt.Sprintf("批次-%d", batch.UpdateID), "Bot", "INFO", fmt.Sprintf("开始处理批次：%d 个链接，%s", len(batch.Items), mode))
			state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID, Text: fmt.Sprintf("🔄 开始处理 %d 个链接…", len(batch.Items))}
			if batch.Mode == "reshare" {
				state.Pending.Text += "\n本人分享直接生成，其他账号的正常分享合并转存，审核来源单独处理。"
			}
			return true
		}
	}
	return false
}

// 先把收到的全部更新和接收游标一起落盘，下一次轮询确认消息前保证队列可恢复。
func (b *Telegram) collectUpdates(ctx context.Context, state *telegramState) error {
	deadline := time.Now().Add(30 * time.Second)
	for _, batch := range state.Queue {
		if batch.ReadyAt.Before(deadline) {
			deadline = batch.ReadyAt
		}
	}
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	// 队列过长时停止继续确认新消息，让 Telegram 暂存，避免本地无限积压。
	if len(state.Queue) >= 100 {
		<-pollCtx.Done()
		return nil
	}
	timeout := min(25, max(0, int(time.Until(deadline).Seconds())))
	var updates []telegramUpdate
	if err := b.call(pollCtx, "getUpdates", map[string]any{"offset": state.Offset, "timeout": timeout, "limit": 10, "allowed_updates": []string{"message"}}, &updates); err != nil {
		if pollCtx.Err() != nil {
			return nil
		}
		log.Printf("Telegram 拉取消息失败，请检查网络、令牌以及是否已设置 Webhook")
		if b.OnStatus != nil {
			b.OnStatus(false, err.Error())
		}
		botDelay(pollCtx)
		return nil
	}
	if b.OnStatus != nil {
		b.OnStatus(true, "")
	}
	if len(updates) == 0 {
		// 不足一秒的窗口使用短轮询，避免空响应造成忙循环。
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-pollCtx.Done():
		case <-timer.C:
		}
		return nil
	}
	b.enqueueUpdates(state, updates, time.Now())
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 接收队列: %w", err)
	}
	return nil
}
