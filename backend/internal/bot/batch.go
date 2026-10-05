package bot

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/service"
)

type messageBatch struct {
	SourceMessageIDs []int64        `json:"sourceMessageIDs,omitempty"`
	ReadyAt          time.Time      `json:"readyAt,omitempty"`
	Mode             string         `json:"mode,omitempty"`
	CloudRedo        bool           `json:"cloudRedo,omitempty"`
	UpdateID         int64          `json:"updateID"`
	ChatID           int64          `json:"chatID"`
	Items            []messageShare `json:"items"`
	Next             int            `json:"next"`
	MessageID        int64          `json:"messageID,omitempty"`
	ThreadID         int64          `json:"threadID,omitempty"`
	StatusMessageID  int64          `json:"statusMessageID,omitempty"`
}

// 每个链接分别保存开始与结束状态；重启只继续尚未开始的链接，不重复转存。
func (b *Telegram) processBatchLink(ctx context.Context, state *telegramState) error {
	batch := state.Batch
	if batch.Mode == "cloud" {
		return b.processCloudDownload(ctx, state)
	}
	if generator, ok := b.Generator.(service.BatchGenerator); ok && batch.Mode == "reshare" && batch.Next == 0 {
		return b.processMergedBatch(ctx, state, generator)
	}
	index := batch.Next
	item := batch.Items[index]
	batch.Next++
	state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID, Text: fmt.Sprintf("⚠️ 【115网盘】【%d/%d】任务被中断\n\n原链接：%s\n请检查 STRM 和转存任务记录；本链接未自动重做，后续链接继续处理。", index+1, len(batch.Items), item.URL)}
	state.Pending.EditMessageID = batch.StatusMessageID
	if index < len(batch.SourceMessageIDs) && batch.SourceMessageIDs[index] > 0 {
		state.Pending.MessageID = batch.SourceMessageIDs[index]
	}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("记录 Telegram 链接任务: %w", err)
	}
	settings := b.Settings.ShareValue()
	input := settings.Generation
	input.URL = item.URL
	input.Password = item.Password
	input.Mode = batch.Mode
	taskCtx, cancel := context.WithTimeout(ctx, service.ShareGenerationTimeout)
	defer cancel()
	taskCtx = service.WithGenerationSource(taskCtx, "Telegram")
	taskCtx = service.WithGenerationBatch(taskCtx, batch.UpdateID, index+1, len(batch.Items))
	taskCtx = service.WithShareReplyManaged(taskCtx)
	result, genErr := b.Generator.Generate(taskCtx, input)
	state.Pending.Text, state.Pending.PreviewURL = formatShareReply(input, settings, result, genErr, index+1, len(batch.Items))
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 链接结果: %w", err)
	}
	return nil
}
