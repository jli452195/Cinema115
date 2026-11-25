package bot

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

const (
	telegramTextBatchSize   = 100
	maxBatchFailureExamples = 5
	maxBatchShareLinks      = 10
)

// 只保留汇总所需的信息，完整的任务结果由相应业务模块记录。
type textBatchResult struct {
	Succeeded     int      `json:"succeeded"`
	Failed        int      `json:"failed"`
	Uncertain     int      `json:"uncertain,omitempty"`
	Warnings      int      `json:"warnings"`
	Examples      []string `json:"examples,omitempty"`
	Shares        []string `json:"shares,omitempty"`
	SharesOmitted bool     `json:"sharesOmitted,omitempty"`
}

func (batch *messageBatch) summarizeLinks() bool {
	return batch.Mode == "cloud" || batch.TextDocument || batch.Mode == "reshare" && len(batch.Items) > telegramTextBatchSize
}

func (batch *messageBatch) reply(text string) *pendingReply {
	return &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID,
		EditMessageID: batch.StatusMessageID, Text: text}
}

func (batch *messageBatch) summary() *textBatchResult {
	if batch.TextResult == nil {
		batch.TextResult = &textBatchResult{}
	}
	return batch.TextResult
}

// 保存写操作的待核对回执与游标。进程退出后不会重复执行这一段。
func (b *Telegram) saveBatchCheckpoint(state *telegramState, end int, interrupted string) error {
	batch := state.Batch
	start := batch.Next
	if batch.summarizeLinks() {
		where := fmt.Sprintf("第 %d 条", start+1)
		if start+1 != end {
			where = fmt.Sprintf("第 %d～%d 条", start+1, end)
		}
		if batch.Mode == "cloud" {
			interrupted = where + "提交结果未确认。"
		} else {
			interrupted = fmt.Sprintf("%s处理结果待核对。请检查管理页任务记录，已开始的链接不会自动重做。", where)
		}
		interrupted = batch.formatSummary(interrupted, len(batch.Items)-end)
		// 调用者已取出本段输入，继续保留序号即可，后续落盘无需反复写入已开始的链接。
		clear(batch.Items[start:end])
	}
	batch.Next = end
	state.Pending = batch.reply(interrupted)
	if start < len(batch.SourceMessageIDs) && batch.SourceMessageIDs[start] > 0 {
		state.Pending.MessageID = batch.SourceMessageIDs[start]
	}
	return b.save(*state)
}

func (b *Telegram) recordTextBatchResult(batch *messageBatch, operation string, index int, err error, warning string) {
	result := batch.summary()
	if err == nil {
		result.Succeeded++
		if warning == "" {
			return
		}
		result.Warnings++
	} else {
		result.Failed++
	}
	if err != nil {
		reason := textBatchFailureReason(err)
		if warning != "" {
			warning = "，" + warning
		}
		warning = reason + warning
	}
	level := "WARNING"
	if err != nil {
		level = "ERROR"
	}
	b.recordTextBatchIssue(batch, operation, index, warning, level)
}

func (b *Telegram) recordTextBatchIssue(batch *messageBatch, operation string, index int, reason, level string) {
	detail := fmt.Sprintf("第 %d/%d 条：%s", index+1, len(batch.Items), shortReply(reason, 300))
	result := batch.summary()
	if len(result.Examples) < maxBatchFailureExamples {
		result.Examples = append(result.Examples, detail)
	}
	b.Logs.Append("Telegram", fmt.Sprintf("批次-%d", batch.UpdateID), operation, level, detail)
}

func (b *Telegram) recordShareTextResult(batch *messageBatch, operation string, index int, result *service.GenerationResult, err error) {
	warning := ""
	if result == nil && err == nil {
		err = errors.New("生成器未返回处理结果")
	}
	if result != nil {
		if err != nil && result.Written > 0 {
			warning = fmt.Sprintf("已有 %d 个 STRM 文件生成", result.Written)
		} else if err == nil && (result.Failed > 0 || result.MetadataFailed > 0) {
			warning = fmt.Sprintf("STRM 失败 %d 个，元数据失败 %d 个", result.Failed, result.MetadataFailed)
		}
		if result.ReshareURL != "" {
			summary := batch.summary()
			if !slices.Contains(summary.Shares, result.ReshareURL) {
				if len(summary.Shares) < maxBatchShareLinks && telegramTextUnits(result.ReshareURL) <= 250 {
					summary.Shares = append(summary.Shares, result.ReshareURL)
				} else {
					summary.SharesOmitted = true
				}
			}
		}
	}
	b.recordTextBatchResult(batch, operation, index, err, warning)
}

func (b *Telegram) saveTextBatchProgress(state *telegramState, operation string) error {
	batch := state.Batch
	result := batch.summary()
	state.Pending = nil
	if batch.Next == len(batch.Items) {
		if batch.Mode == "cloud" && result.Succeeded == len(batch.Items) {
			state.Pending = batch.reply(fmt.Sprintf("已提交 %d 条到 115 云下载。", result.Succeeded) + batch.transferDestination())
		} else {
			title := operation + "处理完成"
			if batch.Mode == "cloud" {
				title = operation + "提交结果"
			}
			state.Pending = batch.reply(batch.formatSummary(title+batch.transferDestination(), 0))
		}
	}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 清单处理结果: %w", err)
	}
	if batch.Mode == "cloud" || batch.Next == len(batch.Items) || batch.Next%telegramTextBatchSize == 0 {
		b.Logs.Append("Telegram", fmt.Sprintf("批次-%d", batch.UpdateID), operation, "INFO",
			fmt.Sprintf("已处理 %d/%d 条，成功 %d 条，失败 %d 条，结果未确认 %d 条", batch.Next, len(batch.Items), result.Succeeded, result.Failed, result.Uncertain))
	}
	return nil
}

func (b *Telegram) stopTextBatch(state *telegramState, operation string, err error) error {
	batch := state.Batch
	remaining := len(batch.Items) - batch.Next
	batch.Next = len(batch.Items)
	text := operation + "批次已停止。"
	if err != nil {
		text += "\n原因：" + textBatchFailureReason(err)
	}
	text += batch.transferDestination()
	state.Pending = batch.reply(batch.formatSummary(text, remaining))
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram 清单停止状态: %w", err)
	}
	if err != nil && batch.Mode == "cloud" {
		b.Logs.Append("Telegram", fmt.Sprintf("批次-%d", batch.UpdateID), operation, "WARNING", textBatchFailureReason(err))
	}
	return nil
}

func (batch *messageBatch) transferDestination() string {
	var name, path string
	switch batch.Mode {
	case "cloud":
		name, path = batch.CloudTargetName, batch.CloudTarget.TargetPath
	case "share_transfer":
		name, path = batch.ShareTarget.Name, batch.ShareTarget.TargetPath
	default:
		return ""
	}
	if name == "" {
		name, path = path, ""
	}
	if name == "" {
		return ""
	}
	if path != "" && path != name {
		name += " · " + path
	}
	return "\n目标：" + shortReply(name, 240)
}

func (batch *messageBatch) formatSummary(title string, remaining int) string {
	result := batch.summary()
	successLabel := "成功"
	if batch.Mode == "cloud" {
		successLabel = "已提交"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n%s %d 条，失败 %d 条", title, successLabel, result.Succeeded, result.Failed)
	if result.Uncertain > 0 {
		fmt.Fprintf(&text, "，提交结果未确认 %d 条", result.Uncertain)
	}
	if result.Warnings > 0 {
		fmt.Fprintf(&text, "，其中 %d 条成功但有异常", result.Warnings)
	}
	if remaining > 0 {
		fmt.Fprintf(&text, "，尚未开始 %d 条", remaining)
	}
	if len(result.Shares) > 0 {
		text.WriteString("\n\n分享结果：")
		for _, url := range result.Shares {
			text.WriteString("\n" + url)
		}
	}
	if result.SharesOmitted {
		text.WriteString("\n另有分享结果，详见管理页任务记录。")
	}
	appendBatchFailureExamples(&text, result.Failed+result.Uncertain+result.Warnings, result.Examples)
	return text.String()
}

func appendBatchFailureExamples(text *strings.Builder, total int, examples []string) {
	if total == 0 {
		return
	}
	text.WriteString("\n\n异常示例：")
	for _, example := range examples {
		text.WriteString("\n• " + shortReply(strings.Join(strings.Fields(example), " "), 100))
	}
	if omitted := total - len(examples); omitted > 0 {
		fmt.Fprintf(text, "\n另有 %d 条异常。", omitted)
	}
	text.WriteString("\n完整结果请查看管理页任务记录或日志。")
}

func textBatchFailureReason(err error) string {
	var status *p115.HTTPError
	var deferred *p115.CloudRequestDeferredError
	switch {
	case errors.As(err, &status):
		return fmt.Sprintf("115 返回 HTTP %d", status.Status)
	case errors.As(err, &deferred):
		return "115 正在冷却，本次请求尚未发送"
	case errors.Is(err, errTransferBatchResumed):
		return "服务重启后原批次凭据快照已失效，请核对结果后重新发送剩余链接"
	case errors.Is(err, errCloudBatchTargetChanged):
		return errCloudBatchTargetChanged.Error()
	case errors.Is(err, errCloudBatchUnavailable):
		return errCloudBatchUnavailable.Error()
	case errors.Is(err, p115.ErrShareUnderReview):
		return "115 分享正在审核"
	case errors.Is(err, p115.ErrCloudInput), errors.Is(err, service.ErrCloudTaskRunning),
		errors.Is(err, service.ErrCloudTaskUnclear), errors.Is(err, service.ErrCloudTaskBusy), service.CloudDownloadUncertain(err):
		return service.CloudDownloadErrorMessage(err)
	case errors.Is(err, service.ErrShareTransferUncertain):
		return "115 转存结果待核对"
	case errors.Is(err, service.ErrGenerationInput):
		return "链接、账号或目标配置未通过校验"
	}
	for _, cause := range []error{p115.ErrShareLoginRequired, p115.ErrShareThrottled, p115.ErrSharePassword, p115.ErrShareNotReadable, p115.ErrShareSnapshotPending} {
		if errors.Is(err, cause) {
			return cause.Error()
		}
	}
	return "处理失败，详情请查看对应的任务记录"
}
