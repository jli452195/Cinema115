// Package bot 将 Telegram 消息转换为网页共用的分享生成任务。
package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/service"
)

// ShareGenerator 与网页复用同一生成服务。
type ShareGenerator interface {
	Generate(context.Context, service.GenerateInput) (*service.GenerationResult, error)
}

var _ ShareGenerator = (*service.ShareRuns)(nil)

type telegramGenerationPreparer interface {
	PrepareTelegramGeneration(service.AutomationSettings) (func(context.Context) context.Context, error)
}

// 正文仅在配置相同的情况下共享等待窗口，TXT 清单始终独立入队。
func (b *Telegram) enqueueShareSTRMBatch(state *telegramState, updateID int64, message *telegramMessage, items []messageShare, settings service.AutomationSettings, textDocument bool, now time.Time) {
	mode := "direct"
	wait := time.Duration(0)
	if settings.ReshareEnabled {
		mode = "reshare"
		if !textDocument {
			wait = time.Duration(settings.TGBatchWaitSeconds) * time.Second
		}
	}
	var target *messageBatch
	if wait > 0 {
		for _, queued := range state.Queue {
			if !queued.TextDocument && queued.ChatID == message.Chat.ID && queued.ThreadID == message.ThreadID && queued.Mode == mode &&
				queued.ShareSettings != nil && *queued.ShareSettings == settings && now.Before(queued.ReadyAt) {
				target = queued
				break
			}
		}
	}
	if target == nil {
		target = &messageBatch{UpdateID: updateID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
			Mode: mode, ReadyAt: now.Add(wait), ShareSettings: &settings, TextDocument: textDocument}
		state.Queue = append(state.Queue, target)
	}
	for _, item := range items {
		if wait > 0 {
			duplicate := false
			for _, existing := range target.Items {
				if existing == item {
					duplicate = true
					break
				}
			}
			if !duplicate {
				target.ReadyAt = now.Add(wait)
			}
		}
		target.Items = append(target.Items, item)
		target.SourceMessageIDs = append(target.SourceMessageIDs, message.ID)
	}
}

// 设置可持久化，凭据装饰器只保存在内存；恢复已开始批次时停止剩余链接。
func (b *Telegram) shareGenerationContext(batch *messageBatch) (func(context.Context) context.Context, error) {
	if batch.generationContext != nil {
		return batch.generationContext, nil
	}
	if batch.Next > 0 {
		return nil, errTransferBatchResumed
	}
	if batch.ShareSettings == nil {
		return nil, errors.New("分享 STRM 批次缺少生成设置，请重新发送链接")
	}
	if hint := b.resourceChoiceHint(resourceChoice{Kind: "share_strm", ShareSettings: batch.ShareSettings}, ""); hint != "" {
		return nil, errors.New(hint)
	}
	preparer, ok := b.Generator.(telegramGenerationPreparer)
	if !ok {
		return nil, errors.New("分享 STRM 批次凭据服务尚未初始化")
	}
	decorate, err := preparer.PrepareTelegramGeneration(*batch.ShareSettings)
	if err != nil {
		return nil, fmt.Errorf("准备分享 STRM 批次: %w", err)
	}
	if decorate == nil {
		return nil, errors.New("分享 STRM 未能固定批次凭据")
	}
	batch.generationContext = decorate
	return decorate, nil
}

func (b *Telegram) stopShareGeneration(state *telegramState, cause error) error {
	batch := state.Batch
	remaining := len(batch.Items) - batch.Next
	batch.Next = len(batch.Items)
	state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID,
		EditMessageID: batch.StatusMessageID, Text: fmt.Sprintf("分享 STRM 批次已停止，%d 个尚未开始的链接未提交。\n原因：%s", remaining, cause.Error())}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 分享 STRM 停止状态: %w", err)
	}
	return nil
}
