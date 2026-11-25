package bot

import (
	"context"
	"fmt"
	"log"
	"time"
)

func (b *Telegram) queueTextReply(state *telegramState, update telegramUpdate, message *telegramMessage, text string) {
	state.Replies = append(state.Replies, &pendingReply{UpdateID: update.ID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID, Text: text})
}

// 所有资源对整条消息只作一次用途和目录决策，选择后才进入业务队列。
func (b *Telegram) enqueueUpdates(ctx context.Context, state *telegramState, updates []telegramUpdate, now time.Time) {
	for _, update := range updates {
		if update.ID < state.Offset {
			continue
		}
		state.Offset = update.ID + 1
		if update.CallbackQuery != nil {
			b.enqueueResourceCallback(ctx, state, update.CallbackQuery, now)
			continue
		}
		m := update.Message
		if m == nil || m.From.IsBot || !b.AllowedChats[m.Chat.ID] {
			continue
		}
		if m.Document.isText() {
			b.enqueueTextDocument(state, update, now)
			continue
		}
		b.enqueueResourceInput(ctx, state, update, parseTelegramResources(m), false, now)
	}
	// 先让本轮过期回调清除原键盘，再清理其余已过期的选择记录。
	state.ResourceSelections = pruneResourceSelections(state.ResourceSelections, now)
}

func (b *Telegram) enqueueResourceInput(ctx context.Context, state *telegramState, update telegramUpdate, resources telegramResources, textDocument bool, now time.Time) {
	m := update.Message
	command := telegramMessageText(m)
	kind := ""
	if isED2KSTRMCommand(command) {
		kind = "ed2k_strm"
	} else if isCloudCommand(command) {
		kind = "cloud"
	}
	if len(resources.Shares) > 0 && (len(resources.CloudLinks) > 0 || kind != "") {
		b.queueTextReply(state, update, m, "115 分享链接与磁力、ED2K 请分开发送，本条消息尚未提交任务。")
		return
	}
	if kind == "ed2k_strm" && resources.HasMagnet {
		b.queueTextReply(state, update, m, "ED2K STRM 仅支持 ED2K 文件链接，磁力不能生成 ED2K STRM，本条消息尚未提交任务。")
		return
	}
	if resources.HasMagnet {
		// 磁力和 ED2K 共用一批云下载。
		kind = "cloud"
	}
	if len(resources.CloudLinks) > 0 || kind != "" {
		items := make([]messageShare, len(resources.CloudLinks))
		for i, link := range resources.CloudLinks {
			items[i] = messageShare{URL: link}
		}
		b.enqueueResourceMessage(ctx, state, update, m, items, kind, textDocument, now)
		return
	}
	if len(resources.Shares) > 0 && (textDocument || b.ShareEnabled || len(b.ShareTransferTargets) > 0) {
		b.enqueueResourceMessage(ctx, state, update, m, resources.Shares, "share", textDocument, now)
	}
}

// 普通分享可越过尚未到期的转存批次，执行中的云端操作仍保持串行。
func (b *Telegram) startReadyBatch(state *telegramState, now time.Time) bool {
	// 普通分享与云下载不等待合并窗口；同一 Bot 仍串行执行云端操作。
	for _, selectedMode := range []string{"direct", "ed2k_strm", "cloud", "share_transfer", "reshare"} {
		for i, batch := range state.Queue {
			if batch.Mode != selectedMode {
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
			mode := map[string]string{"direct": "普通分享", "ed2k_strm": "ED2K STRM 导入", "cloud": "115 云下载", "share_transfer": "115 分享转存", "reshare": "合并转存再分享"}[batch.Mode]
			b.Logs.Append("Telegram", fmt.Sprintf("批次-%d", batch.UpdateID), "Bot", "INFO", fmt.Sprintf("开始处理批次：%d 个链接，%s", len(batch.Items), mode))
			if batch.Mode == "cloud" || batch.Mode == "ed2k_strm" {
				// 入队操作只发送最终汇总；执行前先持久保存整个批次。
				state.Pending = nil
				return true
			}
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
	for _, document := range state.TextDocuments {
		if document.ReadyAt.Before(deadline) {
			deadline = document.ReadyAt
		}
	}
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	// 队列过长时停止继续确认新消息，让 Telegram 暂存，避免本地无限积压。
	if len(state.Queue) >= maxQueuedResourceBatches {
		<-pollCtx.Done()
		return nil
	}
	timeout := min(25, max(0, int(time.Until(deadline).Seconds())))
	var updates []telegramUpdate
	if err := b.call(pollCtx, "getUpdates", map[string]any{"offset": state.Offset, "timeout": timeout, "limit": 10, "allowed_updates": []string{"message", "callback_query"}}, &updates); err != nil {
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
	b.enqueueUpdates(ctx, state, updates, time.Now())
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 接收队列: %w", err)
	}
	return nil
}
