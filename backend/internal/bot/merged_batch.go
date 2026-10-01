package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/service"
)

func (b *Telegram) processMergedBatch(ctx context.Context, state *telegramState, generator service.BatchGenerator) error {
	batch := state.Batch
	settings := b.Settings.ShareValue()
	inputs := make([]service.GenerateInput, len(batch.Items))
	for i, item := range batch.Items {
		inputs[i] = settings.Generation
		inputs[i].URL, inputs[i].Password, inputs[i].Mode = item.URL, item.Password, "reshare"
	}
	reply := &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID, EditMessageID: batch.StatusMessageID, Text: fmt.Sprintf("⚠️ 【115网盘】合并转存任务被中断（%d 个链接）\n请核对任务记录和临时目录，本批次不自动重复转存。", len(inputs))}
	// 云端变更前一次保存整个批次的中断检查点，避免重启重复转存其中任何来源。
	batch.Next = len(batch.Items)
	state.Pending, state.Replies = reply, nil
	if err := b.save(*state); err != nil {
		return fmt.Errorf("记录 Telegram 合并任务: %w", err)
	}
	taskCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	taskCtx = service.WithGenerationBatch(service.WithGenerationSource(taskCtx, "Telegram"), batch.UpdateID, 1, len(inputs))
	taskCtx = service.WithShareReplyManaged(taskCtx)
	outcomes, batchErr := generator.GenerateBatch(taskCtx, inputs)
	if len(outcomes) != len(inputs) {
		outcomes = make([]service.BatchOutcome, len(inputs))
		batchErr = errors.Join(batchErr, errors.New("合并转存未返回完整结果，请检查任务记录"))
	}
	for i := range outcomes {
		if outcomes[i].Result == nil && outcomes[i].Err == nil {
			outcomes[i].Err = batchErr
			if outcomes[i].Err == nil {
				outcomes[i].Err = errors.New("生成器未返回处理结果")
			}
		}
	}
	reply.Text, reply.PreviewURL = formatMergedReply(inputs, outcomes)
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 合并结果: %w", err)
	}
	return nil
}
