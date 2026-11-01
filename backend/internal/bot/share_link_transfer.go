package bot

import (
	"context"
	"fmt"

	"strm115/backend/internal/service"
)

func (b *Telegram) processShareLinkTransfer(ctx context.Context, state *telegramState) error {
	batch := state.Batch
	if err := ctx.Err(); err != nil {
		return err
	}
	execution, err := b.shareBatchExecution(batch)
	if err != nil {
		remaining := len(batch.Items) - batch.Next
		batch.Next = len(batch.Items)
		state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID,
			Text: fmt.Sprintf("115 分享转存批次已停止，%d 个尚未开始的链接未提交。\n原因：%s", remaining, err.Error()), EditMessageID: batch.StatusMessageID}
		if err := b.save(*state); err != nil {
			return fmt.Errorf("保存 Telegram 分享转存停止状态: %w", err)
		}
		return nil
	}
	index := batch.Next
	item := batch.Items[index]
	batch.Next++
	state.Pending = &pendingReply{
		UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID,
		Text: "115 分享链接正在提交到指定目录。若 Bot 或服务中断，本条不会自动重试；恢复前请检查目标目录。",
	}
	state.Pending.EditMessageID = batch.StatusMessageID
	if index < len(batch.SourceMessageIDs) && batch.SourceMessageIDs[index] > 0 {
		state.Pending.MessageID = batch.SourceMessageIDs[index]
	}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 分享转存进度: %w", err)
	}
	taskCtx, cancel := context.WithTimeout(ctx, service.ShareGenerationTimeout)
	receipt, err := execution(taskCtx, item.URL, item.Password)
	cancel()
	state.Pending.Text = formatShareLinkTransferReply(receipt, err)
	level := "INFO"
	if err != nil {
		level = "ERROR"
	}
	b.Logs.Append("Telegram", fmt.Sprintf("消息-%d", batch.UpdateID), "115 转存", level, state.Pending.Text)
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 分享转存结果: %w", err)
	}
	return nil
}

func formatShareLinkTransferReply(receipt service.ShareLinkTransferReceipt, err error) string {
	if err != nil {
		text := "115 分享转存未确认完成。\n目标：" + receipt.TargetPath
		if receipt.ItemCount > 0 {
			text += fmt.Sprintf("\n已确认提交：%d/%d 个顶层条目", receipt.AcceptedItems, receipt.ItemCount)
		}
		return text + "\n原因：" + err.Error()
	}
	return fmt.Sprintf("115 已接受分享转存请求。\n目标：%s\n已提交：%d 个顶层条目（%d 批）\n115 仍可能在后台接收文件。", receipt.TargetPath, receipt.AcceptedItems, receipt.BatchCount)
}
