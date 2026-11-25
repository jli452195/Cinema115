package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

const maxPendingTextDocuments = 8
const maxTextDocumentReadAttempts = 3

type pendingTextDocument struct {
	Message  telegramMessage `json:"message"`
	UpdateID int64           `json:"updateID"`
	ReadyAt  time.Time       `json:"readyAt"`
	Attempts int             `json:"attempts,omitempty"`
}

func (b *Telegram) enqueueTextDocument(state *telegramState, update telegramUpdate, now time.Time) {
	message := update.Message
	if err := message.Document.validate(); err != nil {
		b.replyTextDocumentFailure(state, update, err.Error())
		return
	}
	pending := len(state.TextDocuments)
	for _, batch := range state.Queue {
		if batch.TextDocument {
			pending++
		}
	}
	for _, selection := range state.ResourceSelections {
		if selection != nil && selection.TextDocument && now.Before(selection.Expires) {
			pending++
		}
	}
	if state.Batch != nil && state.Batch.TextDocument {
		pending++
	}
	if pending >= maxPendingTextDocuments {
		b.replyTextDocumentFailure(state, update, "Bot 待处理的 TXT 附件已满，请稍后重新发送")
		return
	}
	state.TextDocuments = append(state.TextDocuments, &pendingTextDocument{Message: *message, UpdateID: update.ID, ReadyAt: now})
}

// 附件下载与接收游标分开处理，先持久保存待下载项和游标，网络失败可以重试同一项。
func (b *Telegram) processReadyTextDocument(ctx context.Context, state *telegramState, now time.Time) bool {
	for i, pending := range state.TextDocuments {
		message := &pending.Message
		if !b.AllowedChats[message.Chat.ID] {
			state.TextDocuments = slices.Delete(state.TextDocuments, i, i+1)
			return true
		}
		if now.Before(pending.ReadyAt) {
			continue
		}
		update := telegramUpdate{ID: pending.UpdateID, Message: message}
		readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		text, err := b.readTextDocument(readCtx, message.Document)
		cancel()
		if ctx.Err() != nil {
			return false
		}
		if err != nil && b.retryTextDocument(pending, err) {
			return true
		}
		state.TextDocuments = slices.Delete(state.TextDocuments, i, i+1)
		if err != nil {
			b.replyTextDocumentFailure(state, update, err.Error())
			return true
		}
		b.enqueueTextDocumentLinks(ctx, state, update, text, time.Now())
		return true
	}
	return false
}

func (b *Telegram) retryTextDocument(pending *pendingTextDocument, err error) bool {
	pending.Attempts++
	if pending.Attempts >= maxTextDocumentReadAttempts || errors.Is(err, errTelegramTextDocumentInvalid) ||
		errors.Is(err, errTelegramTextDocumentTooLarge) || errors.Is(err, errTelegramTextDocumentUnavailable) {
		return false
	}
	pending.ReadyAt = time.Now().Add(time.Duration(5*pending.Attempts) * time.Second)
	b.Logs.Append("Telegram", fmt.Sprintf("消息-%d", pending.Message.ID), "TXT 附件", "WARNING",
		fmt.Sprintf("读取失败（第 %d/%d 次），稍后重试。原因：%s", pending.Attempts, maxTextDocumentReadAttempts, err))
	return true
}

func (b *Telegram) enqueueTextDocumentLinks(ctx context.Context, state *telegramState, update telegramUpdate, text string, now time.Time) {
	resources := parseTelegramResources(update.Message, text)
	if resources.InvalidDocumentLine != 0 {
		b.replyTextDocumentFailure(state, update, fmt.Sprintf("TXT 第 %d 行含无效链接，请检查文件后重新发送", resources.InvalidDocumentLine))
		return
	}
	count := len(resources.CloudLinks) + len(resources.Shares)
	if count == 0 {
		b.replyTextDocumentFailure(state, update, "TXT 中没有识别到 ED2K、磁力或 115 分享链接")
		return
	}
	b.Logs.Append("Telegram", fmt.Sprintf("消息-%d", update.Message.ID), "TXT 附件", "INFO",
		fmt.Sprintf("已识别 %d 条链接，去除 %d 条重复链接", count, resources.Duplicates))
	b.enqueueResourceInput(ctx, state, update, resources, true, now)
}

func (b *Telegram) replyTextDocumentFailure(state *telegramState, update telegramUpdate, reason string) {
	b.Logs.Append("Telegram", fmt.Sprintf("消息-%d", update.Message.ID), "TXT 附件", "WARNING", reason)
	b.queueTextReply(state, update, update.Message, reason+"。本条消息未提交任务。")
}
