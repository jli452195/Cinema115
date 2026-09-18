package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type CloudDownloadSubmitter interface {
	Submit(context.Context, service.CloudDownloadConfig, string) (service.CloudDownloadReceipt, error)
}

func (b *Telegram) processCloudDownload(ctx context.Context, state *telegramState) error {
	batch := state.Batch
	index := batch.Next
	item := batch.Items[index]
	batch.Next++
	state.Pending = &pendingReply{
		UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID,
		EditMessageID: batch.StatusMessageID,
		Text:          fmt.Sprintf("⚠️ 【115 云下载】【%d/%d】任务被中断\n\n请到 115 云下载任务中确认；本链接不会自动重复提交，后续链接会继续处理。", index+1, len(batch.Items)),
	}
	if index < len(batch.SourceMessageIDs) && batch.SourceMessageIDs[index] > 0 {
		state.Pending.MessageID = batch.SourceMessageIDs[index]
	}
	// Persist the at-most-once checkpoint before changing upstream state.
	if err := b.save(*state); err != nil {
		return fmt.Errorf("记录 Telegram 云下载任务: %w", err)
	}

	taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	receipt, err := b.CloudDownloads.Submit(taskCtx, b.CloudDownloadConfig.ServiceConfig(), item.URL)
	cancel()
	state.Pending.Text = formatCloudDownloadReply(receipt, err, index+1, len(batch.Items))
	if err != nil {
		b.Logs.Append("Telegram", fmt.Sprintf("云下载-%d", batch.UpdateID), "云下载", "ERROR", fmt.Sprintf("第 %d/%d 个任务提交失败：%s", index+1, len(batch.Items), cloudDownloadError(err)))
	} else {
		b.Logs.Append("Telegram", fmt.Sprintf("云下载-%d", batch.UpdateID), "云下载", "INFO", fmt.Sprintf("第 %d/%d 个任务已提交到 115", index+1, len(batch.Items)))
	}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 云下载结果: %w", err)
	}
	return nil
}

func formatCloudDownloadReply(receipt service.CloudDownloadReceipt, err error, index, total int) string {
	prefix := fmt.Sprintf("【%d/%d】", index, total)
	if err != nil {
		return "❌ 【115 云下载】" + prefix + "提交失败\n\n原因：" + cloudDownloadError(err)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "✅ 【115 云下载】%s已提交\n\n账号：%s\n目录：%s", prefix, receipt.AccountName, receipt.TargetPath)
	if receipt.Task.Name != "" {
		fmt.Fprintf(&text, "\n任务：%s", receipt.Task.Name)
	}
	text.WriteString("\n状态：115 已接受任务（不代表下载完成）")
	return text.String()
}

func cloudDownloadError(err error) string {
	switch {
	case errors.Is(err, p115.ErrCloudInput):
		return "链接格式无效，请发送完整的 magnet 或 ED2K 链接"
	case errors.Is(err, context.DeadlineExceeded):
		return "连接 115 超时，请稍后重试并先检查 115 任务列表"
	case errors.Is(err, service.ErrGenerationInput):
		return strings.TrimPrefix(err.Error(), service.ErrGenerationInput.Error()+"：")
	default:
		return err.Error()
	}
}
