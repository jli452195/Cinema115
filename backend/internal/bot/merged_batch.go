package bot

import (
	"context"
	"errors"
	"fmt"

	"strm115/backend/internal/service"
)

func (b *Telegram) processMergedBatch(ctx context.Context, state *telegramState, generator service.BatchGenerator, decorate func(context.Context) context.Context) error {
	batch := state.Batch
	for batch.Next < len(batch.Items) {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := batch.Next
		end := min(start+telegramTextBatchSize, len(batch.Items))
		if err := b.processMergedBatchPart(ctx, state, generator, decorate, start, end); err != nil {
			return err
		}
	}
	return nil
}

func (b *Telegram) processMergedBatchPart(ctx context.Context, state *telegramState, generator service.BatchGenerator, decorate func(context.Context) context.Context, start, end int) error {
	batch := state.Batch
	settings := *batch.ShareSettings
	inputs := make([]service.GenerateInput, end-start)
	for i, item := range batch.Items[start:end] {
		inputs[i] = settings.Generation
		inputs[i].URL, inputs[i].Password, inputs[i].Mode = item.URL, item.Password, "reshare"
	}
	interrupted := fmt.Sprintf("⚠️ 【115网盘】合并转存任务被中断（%d 个链接）\n请核对任务记录和临时目录，本批次不自动重复转存。", len(inputs))
	if err := b.saveBatchCheckpoint(state, end, interrupted); err != nil {
		return fmt.Errorf("记录 Telegram 合并任务: %w", err)
	}
	taskCtx, cancel := context.WithTimeout(ctx, service.ShareGenerationTimeout)
	defer cancel()
	taskCtx = service.WithGenerationBatch(service.WithGenerationSource(decorate(taskCtx), "Telegram"), batch.UpdateID, start+1, len(batch.Items))
	taskCtx = service.WithShareReplyManaged(taskCtx)
	outcomes, batchErr := generator.GenerateBatch(taskCtx, inputs)
	if batch.summarizeLinks() {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
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
	if batch.summarizeLinks() {
		for i, outcome := range outcomes {
			b.recordShareTextResult(batch, "合并转存", start+i, outcome.Result, outcome.Err)
		}
		return b.saveTextBatchProgress(state, "合并转存")
	}
	state.Pending.Text, state.Pending.PreviewURL = formatMergedReply(inputs, outcomes)
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 合并结果: %w", err)
	}
	return nil
}
