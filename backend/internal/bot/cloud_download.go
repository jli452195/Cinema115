package bot

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type CloudDownloadPreparer interface {
	PrepareBatch(service.CloudDownloadConfig, []string) (service.CloudDownloadBatchExecution, error)
}

func (b *Telegram) enqueueCloudBatch(state *telegramState, updateID int64, message *telegramMessage, items []messageShare, target TelegramTransferTarget, textDocument bool, now time.Time) {
	// 普通云下载按任务身份去重，同一 Hash 的不同链接别名只提交一次。
	seen := make(map[p115.CloudResource]struct{}, len(items))
	links := make([]messageShare, 0, len(items))
	for _, item := range items {
		resource, err := p115.ParseCloudResource(item.URL)
		if err == nil {
			if _, exists := seen[resource]; exists {
				continue
			}
			seen[resource] = struct{}{}
		}
		links = append(links, item)
	}
	state.Queue = append(state.Queue, &messageBatch{
		UpdateID: updateID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Mode: "cloud", ReadyAt: now, CloudTargetID: target.ID, CloudTargetName: target.Name,
		CloudTarget: service.CloudDownloadConfig{AccountID: target.AccountID, TargetCID: target.TargetCID, TargetPath: target.TargetPath},
		Items:       links, TextDocument: textDocument,
	})
}

func sameCloudTarget(a, b TelegramTransferTarget) bool {
	return a.ID == b.ID && a.Name == b.Name && a.AccountID == b.AccountID && a.TargetCID == b.TargetCID && a.TargetPath == b.TargetPath
}

func (b *Telegram) currentCloudTarget(id string) (TelegramTransferTarget, bool) {
	if b.CurrentCloudTarget != nil {
		return b.CurrentCloudTarget(id)
	}
	for _, target := range b.CloudDownloadTargets {
		if target.ID == id {
			return target, true
		}
	}
	return TelegramTransferTarget{}, false
}

// 每组在写入前保存游标，提交后直接汇总本次回执。
func (b *Telegram) processCloudDownload(ctx context.Context, state *telegramState) error {
	const operation = "115 云下载"
	if err := ctx.Err(); err != nil {
		return err
	}
	batch := state.Batch
	execution, err := b.cloudBatchExecution(batch)
	if err != nil {
		return b.stopTextBatch(state, operation, err)
	}
	start := batch.Next
	end := min(start+service.CloudDownloadBatchSize, len(batch.Items))
	if err := b.saveBatchCheckpoint(state, end, ""); err != nil {
		return fmt.Errorf("保存 Telegram 云下载提交进度: %w", err)
	}
	results, stopped := execution.SubmitChunk(ctx, start)
	for _, event := range results {
		b.recordCloudBatchResult(batch, operation, event)
	}
	// 即使服务此刻停止，也先保存已经收到的提交结果。
	if ctx.Err() == nil && stopped && end < len(batch.Items) {
		return b.stopTextBatch(state, operation, nil)
	}
	if err := b.saveTextBatchProgress(state, operation); err != nil {
		return err
	}
	return ctx.Err()
}

func (b *Telegram) recordCloudBatchResult(batch *messageBatch, operation string, event service.CloudDownloadBatchEvent) {
	result := batch.summary()
	if event.Accepted {
		result.Succeeded++
		return
	}
	reason, level := event.Error, "ERROR"
	if event.OutcomeUncertain {
		result.Uncertain++
		level = "WARNING"
		if reason == "" {
			reason = "提交结果未确认"
		}
	} else {
		result.Failed++
		if reason == "" {
			reason = "未提交"
		}
	}
	b.recordTextBatchIssue(batch, operation, event.Index, reason, level)
}
