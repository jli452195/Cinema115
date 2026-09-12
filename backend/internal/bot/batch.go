package bot

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type messageBatch struct {
	UpdateID int64          `json:"updateID"`
	ChatID   int64          `json:"chatID"`
	Items    []messageShare `json:"items"`
	Next     int            `json:"next"`
}

// 每个链接分别保存开始与结束状态；重启只继续尚未开始的链接，不重复转存。
func (b *Telegram) processBatchLink(ctx context.Context, state *telegramState) error {
	batch := state.Batch
	index := batch.Next
	item := batch.Items[index]
	label := ""
	if len(batch.Items) > 1 {
		label = fmt.Sprintf("【%d/%d】", index+1, len(batch.Items))
		if share, err := p115.ParseShareURL(item.URL); err == nil {
			label += "分享 " + share.Code
		}
		label += "\n"
	}
	batch.Next++
	state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, Text: label + "Cinema115 任务被中断，请检查 STRM 和 data/reshare-jobs 任务记录；本链接未自动重做，后续链接将继续处理。"}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("记录 Telegram 链接任务: %w", err)
	}
	input := b.Settings.Value().Generation
	input.URL = item.URL
	input.Password = item.Password
	taskCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	result, genErr := b.Generator.Generate(service.WithGenerationSource(taskCtx, "Telegram"), input)
	text := ""
	if genErr != nil {
		text = "处理失败：" + genErr.Error()
	} else {
		text = fmt.Sprintf("STRM 生成完成：新增 %d。", result.Written)
		if result.ReshareURL != "" {
			text += "\n自己的分享链接：" + result.ReshareURL
			if result.Cleaned {
				text += "\n本次临时目录已移入 115 回收站。"
			}
		}
	}
	if genErr != nil && result != nil && result.ReshareURL != "" {
		text += "\n已创建的分享：" + result.ReshareURL
	}
	// 逐条回复结果，避免整批汇总超过 Telegram 长度限制而丢失后半部分。
	runes := []rune(label + text)
	if len(runes) > 1800 {
		runes = append(runes[:1800], '…')
	}
	state.Pending.Text = string(runes)
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 链接结果: %w", err)
	}
	return nil
}
