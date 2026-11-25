package bot

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

type messageBatch struct {
	cloudExecution    service.CloudDownloadBatchExecution
	shareExecution    service.ShareLinkTransferExecution
	generationContext func(context.Context) context.Context
	ShareSettings     *service.AutomationSettings `json:"shareSettings,omitempty"`
	SourceMessageIDs  []int64                     `json:"sourceMessageIDs,omitempty"`
	ReadyAt           time.Time                   `json:"readyAt,omitempty"`
	Mode              string                      `json:"mode,omitempty"`
	TextDocument      bool                        `json:"textDocument,omitempty"`
	TextResult        *textBatchResult            `json:"textResult,omitempty"`
	ED2KSTRMTarget    model.ED2KSTRMTarget        `json:"ed2kSTRMTarget,omitempty"`
	ED2KRequestID     string                      `json:"ed2kRequestID,omitempty"`
	CloudTargetID     string                      `json:"cloudTargetID,omitempty"`
	CloudTargetName   string                      `json:"cloudTargetName,omitempty"`
	CloudTarget       service.CloudDownloadConfig `json:"cloudTarget,omitempty"`
	ShareTarget       service.ShareTransferTarget `json:"shareTarget,omitempty"`
	UpdateID          int64                       `json:"updateID"`
	ChatID            int64                       `json:"chatID"`
	Items             []messageShare              `json:"items"`
	Next              int                         `json:"next"`
	MessageID         int64                       `json:"messageID,omitempty"`
	ThreadID          int64                       `json:"threadID,omitempty"`
	StatusMessageID   int64                       `json:"statusMessageID,omitempty"`
}

// 按业务保存提交进度。需要凭据快照的转存批次在重启后停止剩余链接。
func (b *Telegram) processBatchLink(ctx context.Context, state *telegramState) error {
	batch := state.Batch
	if batch.Mode == "ed2k_strm" {
		return b.processED2KSTRM(ctx, state)
	}
	if batch.Mode == "cloud" {
		return b.processCloudDownload(ctx, state)
	}
	if batch.Mode == "share_transfer" {
		return b.processShareLinkTransfer(ctx, state)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	decorate, err := b.shareGenerationContext(batch)
	if err != nil {
		if batch.summarizeLinks() {
			return b.stopTextBatch(state, "分享 STRM", err)
		}
		return b.stopShareGeneration(state, err)
	}
	if generator, ok := b.Generator.(service.BatchGenerator); ok && batch.Mode == "reshare" && batch.Next == 0 {
		return b.processMergedBatch(ctx, state, generator, decorate)
	}
	index := batch.Next
	item := batch.Items[index]
	interrupted := fmt.Sprintf("⚠️ 【115网盘】【%d/%d】任务被中断\n\n原链接：%s\n请检查 STRM 和转存任务记录；本链接未自动重做，未开始的链接需重新发送。", index+1, len(batch.Items), item.URL)
	if err := b.saveBatchCheckpoint(state, index+1, interrupted); err != nil {
		return fmt.Errorf("记录 Telegram 链接任务: %w", err)
	}
	settings := *batch.ShareSettings
	input := settings.Generation
	input.URL = item.URL
	input.Password = item.Password
	input.Mode = batch.Mode
	taskCtx, cancel := context.WithTimeout(ctx, service.ShareGenerationTimeout)
	defer cancel()
	taskCtx = service.WithGenerationSource(decorate(taskCtx), "Telegram")
	taskCtx = service.WithGenerationBatch(taskCtx, batch.UpdateID, index+1, len(batch.Items))
	taskCtx = service.WithShareReplyManaged(taskCtx)
	result, genErr := b.Generator.Generate(taskCtx, input)
	if batch.summarizeLinks() {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.recordShareTextResult(batch, "分享 STRM", index, result, genErr)
		return b.saveTextBatchProgress(state, "分享 STRM")
	}
	state.Pending.Text, state.Pending.PreviewURL = formatShareReply(input, settings, result, genErr, index+1, len(batch.Items))
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 链接结果: %w", err)
	}
	return nil
}
