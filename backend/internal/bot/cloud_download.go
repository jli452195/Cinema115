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
	Redo(context.Context, service.CloudDownloadConfig, string) (service.CloudDownloadReceipt, error)
}

type cloudBatchOutcome struct {
	label   string
	redo    bool
	removed bool
	err     error
}

// processCloudDownload handles one Telegram input message as one batch and
// creates exactly one normal reply. Before every upstream mutation it advances
// and persists the item checkpoint, so a restart never repeats an uncertain
// deletion or submission.
func (b *Telegram) processCloudDownload(ctx context.Context, state *telegramState) error {
	batch := state.Batch
	outcomes := make([]cloudBatchOutcome, 0, len(batch.Items)-batch.Next)
	for batch.Next < len(batch.Items) {
		index := batch.Next
		item := batch.Items[index]
		batch.Next++
		state.Pending = &pendingReply{
			UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID,
			Text: "⚠️ 115 云下载提交过程被中断。为避免重复删除或重复提交，本批次不会自动重做已经开始的链接。",
		}
		if err := b.save(*state); err != nil {
			return fmt.Errorf("记录 Telegram 云下载任务: %w", err)
		}

		taskCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		var receipt service.CloudDownloadReceipt
		var err error
		if batch.CloudRedo {
			receipt, err = b.CloudDownloads.Redo(taskCtx, b.CloudDownloadConfig.ServiceConfig(), item.URL)
		} else {
			receipt, err = b.CloudDownloads.Submit(taskCtx, b.CloudDownloadConfig.ServiceConfig(), item.URL)
		}
		cancel()
		outcomes = append(outcomes, cloudBatchOutcome{label: cloudResourceLabel(item.URL), redo: batch.CloudRedo, removed: receipt.RemovedOldTask, err: err})
		if err != nil {
			b.Logs.Append("Telegram", fmt.Sprintf("云下载-%d", batch.UpdateID), "云下载", "ERROR", fmt.Sprintf("第 %d/%d 个任务提交失败：%s", index+1, len(batch.Items), cloudDownloadError(err)))
		} else {
			operation := "提交"
			if receipt.RemovedOldTask {
				operation = "重新提交"
			}
			b.Logs.Append("Telegram", fmt.Sprintf("云下载-%d", batch.UpdateID), "云下载", "INFO", fmt.Sprintf("第 %d/%d 个任务已%s到 115", index+1, len(batch.Items), operation))
		}
	}
	state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID, Text: formatCloudBatchReply(outcomes, b.CloudDownloadConfig.TargetPath)}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 云下载结果: %w", err)
	}
	return nil
}

func cloudResourceLabel(rawURL string) string {
	resource, err := p115.ParseCloudResource(rawURL)
	if err != nil {
		return "未知资源"
	}
	name := "BT"
	if resource.Protocol == "ed2k" {
		name = "ED2K"
	}
	hash := resource.Hash
	if len(hash) > 8 {
		hash = hash[len(hash)-8:]
	}
	return name + " …" + hash
}

func formatCloudBatchReply(outcomes []cloudBatchOutcome, targetPath string) string {
	if len(outcomes) == 1 {
		outcome := outcomes[0]
		if outcome.err != nil {
			prefix := "❌ 115 云下载提交失败"
			if outcome.redo && outcome.removed {
				prefix = "❌ 旧任务记录已移除，但重新提交失败\n已下载文件没有被删除"
			}
			return prefix + "\n资源：" + outcome.label + "\n原因：" + cloudDownloadError(outcome.err)
		}
		if outcome.removed {
			return "✅ 已重新提交到 115 云下载\n资源：" + outcome.label + "\n目录：" + targetPath + "\n旧任务记录已移除，原有网盘文件已保留"
		}
		return "✅ 已提交到 115 云下载\n资源：" + outcome.label + "\n目录：" + targetPath
	}

	submitted, redone := 0, 0
	failures := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.err != nil {
			detail := outcome.label + "：" + cloudDownloadError(outcome.err)
			if outcome.redo && outcome.removed {
				detail += "（旧任务已移除，文件保留）"
			}
			failures = append(failures, detail)
		} else if outcome.removed {
			redone++
		} else {
			submitted++
		}
	}
	var text strings.Builder
	text.WriteString("☁️ 115 云下载提交完成")
	if submitted > 0 {
		fmt.Fprintf(&text, "\n✅ 已提交：%d 个", submitted)
	}
	if redone > 0 {
		fmt.Fprintf(&text, "\n♻️ 已重新提交：%d 个", redone)
	}
	if len(failures) > 0 {
		fmt.Fprintf(&text, "\n❌ 失败：%d 个\n\n失败明细：", len(failures))
		for _, failure := range failures {
			text.WriteString("\n• ")
			text.WriteString(failure)
		}
	}
	text.WriteString("\n目录：")
	text.WriteString(targetPath)
	return text.String()
}

func cloudDownloadError(err error) string {
	switch {
	case errors.Is(err, p115.ErrCloudInput):
		return "链接格式无效，请发送完整的 magnet 或 ED2K 链接"
	case errors.Is(err, service.ErrCloudTaskRunning):
		return "相同任务仍在 115 中运行，未删除或重新提交"
	case errors.Is(err, service.ErrCloudTaskUnclear):
		return "无法安全确认旧任务状态，未执行删除"
	case errors.Is(err, context.DeadlineExceeded):
		return "连接 115 超时，请先检查 115 任务列表再决定是否重试"
	case errors.Is(err, service.ErrGenerationInput):
		return strings.TrimPrefix(err.Error(), service.ErrGenerationInput.Error()+"：")
	default:
		return err.Error()
	}
}
