package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

const (
	selectionLifetime        = 10 * time.Minute
	maxPendingSelections     = 50
	maxQueuedResourceBatches = 100
	maxMessageResourceLinks  = 100
	maxDocumentResourceLinks = 1000
)

func resourceLinkLimit(textDocument bool) int {
	if textDocument {
		return maxDocumentResourceLinks
	}
	return maxMessageResourceLinks
}

// 选择只保留链接和已分配目标的快照，不包含凭据，也不在选择前建立业务任务。
type pendingResourceSelection struct {
	UpdateID        int64            `json:"updateID"`
	ChatID          int64            `json:"chatID"`
	UserID          int64            `json:"userID"`
	MessageID       int64            `json:"messageID"`
	PromptMessageID int64            `json:"promptMessageID"`
	ThreadID        int64            `json:"threadID,omitempty"`
	TextDocument    bool             `json:"textDocument,omitempty"`
	Expires         time.Time        `json:"expires"`
	Items           []messageShare   `json:"items"`
	Choices         []resourceChoice `json:"choices"`
}

type resourceChoice struct {
	Kind          string                      `json:"kind"`
	STRMTarget    *model.ED2KSTRMTarget       `json:"strmTarget,omitempty"`
	CloudTarget   *TelegramTransferTarget     `json:"cloudTarget,omitempty"`
	ShareTarget   *TelegramTransferTarget     `json:"shareTarget,omitempty"`
	ShareSettings *service.AutomationSettings `json:"shareSettings,omitempty"`
}

type ed2kSTRMReadiness interface {
	CheckImportReady(context.Context) error
}

func (b *Telegram) resourceChoices(kind string) []resourceChoice {
	choices := make([]resourceChoice, 0, len(b.ED2KSTRMTargets)+len(b.CloudDownloadTargets)+len(b.ShareTransferTargets)+1)
	if kind == "share" {
		if b.ShareEnabled {
			settings, _ := b.Settings.TelegramShareSettings(b.BotID)
			choices = append(choices, resourceChoice{Kind: "share_strm", ShareSettings: &settings})
		}
		for _, assigned := range b.ShareTransferTargets {
			target := assigned
			if current, ok := b.currentShareTransferTarget(assigned.ID); ok && current.AssignedBotID == b.BotID {
				target = current
			}
			choices = append(choices, resourceChoice{Kind: "share_transfer", ShareTarget: &target})
		}
		return choices
	}
	// 不剔除暂不可用的已分配目标，避免把配置失效误判为用户只分配了另一种用途。
	for _, assigned := range b.ED2KSTRMTargets {
		if kind == "cloud" {
			break
		}
		target := assigned
		if current, ok := b.currentED2KSTRMTarget(assigned.ID); ok {
			target = current
		}
		choices = append(choices, resourceChoice{Kind: "ed2k_strm", STRMTarget: &target})
	}
	for _, assigned := range b.CloudDownloadTargets {
		if kind == "ed2k_strm" {
			break
		}
		target := assigned
		if current, ok := b.currentCloudTarget(assigned.ID); ok && current.AssignedBotID == b.BotID {
			target = current
		}
		choices = append(choices, resourceChoice{Kind: "cloud", CloudTarget: &target})
	}
	return choices
}

func (b *Telegram) ed2kSTRMSetupHint(ctx context.Context) string {
	if b.ED2KSTRM == nil {
		return "ED2K STRM 模块尚未就绪，请检查配置"
	}
	if checker, ok := b.ED2KSTRM.(ed2kSTRMReadiness); ok {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := checker.CheckImportReady(checkCtx); err != nil {
			return "请检查 ED2K STRM 服务地址和主账号配置"
		}
	}
	return ""
}

func (b *Telegram) resourceChoiceHint(choice resourceChoice, strmHint string) string {
	switch {
	case choice.Kind == "share_strm" && choice.ShareSettings != nil:
		if !b.ShareEnabled || b.Settings == nil {
			return "分享 STRM 的 Bot 分配已变化，请重新发送"
		}
		current, err := b.Settings.TelegramShareSettings(b.BotID)
		if err != nil {
			return "请检查分享 STRM 的主账号、输出目录、服务地址和 Bot 配置"
		}
		if current != *choice.ShareSettings {
			return "分享 STRM 设置已变化，请重新发送"
		}
		if b.Generator == nil {
			return "分享 STRM 服务尚未就绪"
		}
		if _, ok := b.Generator.(telegramGenerationPreparer); !ok {
			return "分享 STRM 批次凭据服务尚未初始化"
		}
		return ""
	case choice.Kind == "share_transfer" && choice.ShareTarget != nil:
		current, ok := b.currentShareTransferTarget(choice.ShareTarget.ID)
		if !ok || current.AssignedBotID != b.BotID || !sameTransferTarget(current, *choice.ShareTarget) {
			return "转存目录或 Bot 分配已变化，请重新配置后发送"
		}
		if b.ShareTransfers == nil || !current.Ready {
			return "请检查 115 分享转存的主账号和目标目录配置"
		}
		return ""
	case choice.Kind == "ed2k_strm" && choice.STRMTarget != nil:
		current, ok := b.currentED2KSTRMTarget(choice.STRMTarget.ID)
		if !ok || !sameED2KSTRMTarget(current, *choice.STRMTarget) || strings.TrimSpace(current.OutputDir) == "" {
			return "输出目录或 Bot 分配已变化，请重新配置后发送"
		}
		return strmHint
	case choice.Kind == "cloud" && choice.CloudTarget != nil:
		current, ok := b.currentCloudTarget(choice.CloudTarget.ID)
		if !ok || current.AssignedBotID != b.BotID || !sameCloudTarget(current, *choice.CloudTarget) || current.AssignedBotID != choice.CloudTarget.AssignedBotID {
			return "转存目录或 Bot 分配已变化，请重新配置后发送"
		}
		if b.CloudDownloads == nil || !current.Ready {
			return "请检查磁力/ED2K 转存的主账号和目标目录配置"
		}
		return ""
	default:
		return "此用途或目录无效，请重新发送链接"
	}
}

func (choice resourceChoice) label() string {
	label := "无效目录"
	switch {
	case choice.Kind == "share_strm" && choice.ShareSettings != nil:
		label = "生成分享 STRM · " + choice.ShareSettings.Generation.OutputDir
	case choice.Kind == "share_transfer" && choice.ShareTarget != nil:
		label = "转存到网盘 · " + choice.ShareTarget.Name
	case choice.Kind == "ed2k_strm" && choice.STRMTarget != nil:
		label = "生成 STRM · " + choice.STRMTarget.Name
	case choice.Kind == "cloud" && choice.CloudTarget != nil:
		label = "普通转存 · " + choice.CloudTarget.Name
	}
	// 输出路径可能很长，只缩短展示，完整目标快照仍留在服务端。
	runes := []rune(label)
	if len(runes) > 64 {
		return string(runes[:40]) + "…" + string(runes[len(runes)-23:])
	}
	return label
}

func (b *Telegram) enqueueResourceMessage(ctx context.Context, state *telegramState, update telegramUpdate, message *telegramMessage, items []messageShare, kind string, textDocument bool, now time.Time) {
	// 仅分享 STRM 的群聊保持原接收方式，多用途与普通转存须在私聊明确选择。
	if message.Chat.Type != "private" && !(kind == "share" && b.ShareEnabled && len(b.ShareTransferTargets) == 0) {
		b.queueTextReply(state, update, message, "请在 Bot 私聊中直接发送链接，选择用途与目录；群聊不自动选择处理方式。")
		return
	}
	limit := resourceLinkLimit(textDocument)
	if len(items) == 0 {
		text := "请直接发送 magnet 磁力或 ED2K 文件链接"
		if kind == "share" {
			text = "请直接发送 115 分享链接及提取码"
		} else if kind == "ed2k_strm" {
			text = "请直接发送 ED2K 文件链接"
		}
		b.queueTextReply(state, update, message, fmt.Sprintf("%s，每条消息最多 %d 个，也可以上传 TXT 清单。", text, maxMessageResourceLinks))
		return
	}
	if len(items) > limit {
		if textDocument {
			b.queueTextReply(state, update, message, fmt.Sprintf("每份 TXT 最多接收 %d 个链接，请拆分清单后重新发送。本份清单未入队。", limit))
		} else {
			b.queueTextReply(state, update, message, fmt.Sprintf("每条消息最多接收 %d 个链接，请上传 TXT 清单或拆分后发送。本条消息未入队。", limit))
		}
		return
	}
	if len(state.Queue) >= maxQueuedResourceBatches {
		b.queueTextReply(state, update, message, "Bot 任务队列已满，请稍后重新发送。")
		return
	}
	choices := b.resourceChoices(kind)
	if len(choices) == 0 {
		text := "此 Bot 尚未分配 ED2K STRM 输出目录或磁力/ED2K 转存目标，请在 Telegram Bot 页面分配。"
		if kind == "share" {
			text = "此 Bot 尚未分配 115 分享转存或分享 STRM，请在 Telegram Bot 页面分配。"
		} else if kind == "cloud" {
			text = "此 Bot 尚未分配磁力/ED2K 转存目标，磁力只能用于云下载，请在 Telegram Bot 页面分配。"
		} else if kind == "ed2k_strm" {
			text = "此 Bot 尚未分配 ED2K STRM 输出目录，请在 Telegram Bot 页面分配。"
		}
		b.queueTextReply(state, update, message, text)
		return
	}
	if len(choices) > 99 {
		b.queueTextReply(state, update, message, "此 Bot 分配的处理目录超过 99 个，请减少分配后重新发送。")
		return
	}
	strmHint := ""
	if kind != "cloud" && kind != "share" && len(b.ED2KSTRMTargets) > 0 {
		strmHint = b.ed2kSTRMSetupHint(ctx)
	}
	ready := 0
	hints := make([]string, len(choices))
	var unavailable strings.Builder
	unavailableCount := 0
	for i, choice := range choices {
		hints[i] = b.resourceChoiceHint(choice, strmHint)
		if hints[i] == "" {
			ready++
		} else {
			unavailableCount++
			// 提示只列少量失效目录，防止超过 Telegram 正文长度后阻塞整个回复队列。
			if unavailableCount <= 3 {
				fmt.Fprintf(&unavailable, "\n%s 暂不可用：%s", choice.label(), hints[i])
			}
		}
	}
	if unavailableCount > 3 {
		fmt.Fprintf(&unavailable, "\n另有 %d 个目录暂不可用，请到管理页检查配置。", unavailableCount-3)
	}
	if ready == 0 {
		b.queueTextReply(state, update, message, "没有可用的处理目标，本条消息尚未提交任务。"+unavailable.String())
		return
	}
	if len(choices) == 1 {
		b.enqueueResourceBatch(state, update.ID, message, items, choices[0], textDocument, now)
		return
	}
	state.ResourceSelections = pruneResourceSelections(state.ResourceSelections, now)
	if len(state.ResourceSelections) >= maxPendingSelections {
		b.queueTextReply(state, update, message, "当前待选择的消息较多，请稍后重新发送。")
		return
	}
	state.ResourceSelections = append(state.ResourceSelections, &pendingResourceSelection{
		UpdateID: update.ID, ChatID: message.Chat.ID, UserID: message.From.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Expires: now.Add(selectionLifetime), Items: append([]messageShare(nil), items...), Choices: choices, TextDocument: textDocument,
	})
	keyboard := make([][]map[string]string, 0, len(choices)+1)
	for i, choice := range choices {
		label := choice.label()
		if hints[i] != "" {
			label = "暂不可用 · " + label
		}
		keyboard = append(keyboard, []map[string]string{{"text": label, "callback_data": resourceCallbackData(update.ID, strconv.Itoa(i))}})
	}
	keyboard = append(keyboard, []map[string]string{{"text": "取消", "callback_data": resourceCallbackData(update.ID, "cancel")}})
	state.Replies = append(state.Replies, &pendingReply{
		UpdateID: update.ID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Text:        fmt.Sprintf("识别到 %d 个链接，请选择用途与目录（10 分钟内有效）：", len(items)) + unavailable.String(),
		ReplyMarkup: map[string]any{"inline_keyboard": keyboard}, CaptureMessageID: true,
	})
}

func (b *Telegram) enqueueResourceBatch(state *telegramState, updateID int64, message *telegramMessage, items []messageShare, choice resourceChoice, textDocument bool, now time.Time) {
	switch {
	case choice.Kind == "share_strm" && choice.ShareSettings != nil:
		b.enqueueShareSTRMBatch(state, updateID, message, items, *choice.ShareSettings, textDocument, now)
	case choice.Kind == "share_transfer" && choice.ShareTarget != nil:
		b.enqueueShareTransferBatch(state, updateID, message, items, *choice.ShareTarget, textDocument, now)
	case choice.Kind == "ed2k_strm" && choice.STRMTarget != nil:
		b.enqueueED2KSTRMBatch(state, updateID, message, items, *choice.STRMTarget, textDocument, now)
	case choice.Kind == "cloud" && choice.CloudTarget != nil:
		b.enqueueCloudBatch(state, updateID, message, items, *choice.CloudTarget, textDocument, now)
	}
}

// 回调仅携带短索引，目录和账号信息留在服务端，避免超过 Telegram 的 64 字节限制。
func resourceCallbackData(updateID int64, choice string) string {
	return "resource:" + strconv.FormatInt(updateID, 10) + ":" + choice
}

func (b *Telegram) enqueueResourceCallback(ctx context.Context, state *telegramState, callback *telegramCallbackQuery, now time.Time) {
	if callback == nil || callback.ID == "" {
		return
	}
	answer := func(text string) {
		state.CallbackAnswers = append(state.CallbackAnswers, &pendingCallbackAnswer{ID: callback.ID, Text: text})
	}
	if callback.Message == nil || callback.Message.Chat.Type != "private" || !b.AllowedChats[callback.Message.Chat.ID] {
		answer("当前聊天没有权限，请重新私聊发送链接")
		return
	}
	parts := strings.Split(callback.Data, ":")
	if len(parts) != 3 || parts[0] != "resource" {
		answer("无法识别此选择")
		return
	}
	updateID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		answer("此选择已失效，请重新发送链接")
		return
	}
	index := -1
	for i, selection := range state.ResourceSelections {
		if selection != nil && selection.UpdateID == updateID {
			index = i
			break
		}
	}
	if index < 0 {
		answer("此选择已失效或已处理，请重新发送链接")
		return
	}
	selection := state.ResourceSelections[index]
	if selection.UserID != callback.From.ID || selection.ChatID != callback.Message.Chat.ID || selection.PromptMessageID == 0 || selection.PromptMessageID != callback.Message.ID {
		answer("请由发送原链接的用户在原提示中选择")
		return
	}
	finish := func(text string) {
		copy(state.ResourceSelections[index:], state.ResourceSelections[index+1:])
		state.ResourceSelections[len(state.ResourceSelections)-1] = nil
		state.ResourceSelections = state.ResourceSelections[:len(state.ResourceSelections)-1]
		state.Replies = append(state.Replies, &pendingReply{
			UpdateID: selection.UpdateID, ChatID: selection.ChatID, MessageID: selection.MessageID, ThreadID: selection.ThreadID,
			EditMessageID: selection.PromptMessageID, OptionalEdit: true, Text: text,
			ReplyMarkup: map[string]any{"inline_keyboard": [][]map[string]string{}},
		})
		answer(text)
	}
	if !now.Before(selection.Expires) {
		finish("选择已超时，请重新发送链接")
		return
	}
	if parts[2] == "cancel" {
		finish("已取消，本条消息未提交任务")
		return
	}
	choiceIndex, err := strconv.Atoi(parts[2])
	if err != nil || choiceIndex < 0 || choiceIndex >= len(selection.Choices) {
		answer("此用途或目录无效，请重新选择")
		return
	}
	choice := selection.Choices[choiceIndex]
	strmHint := ""
	if choice.Kind == "ed2k_strm" {
		strmHint = b.ed2kSTRMSetupHint(ctx)
	}
	if hint := b.resourceChoiceHint(choice, strmHint); hint != "" {
		finish(hint + "，本条消息未提交任务，请重新发送链接")
		return
	}
	if len(state.Queue) >= maxQueuedResourceBatches {
		answer("Bot 任务队列已满，请稍后重新选择")
		return
	}
	if len(selection.Items) == 0 || len(selection.Items) > resourceLinkLimit(selection.TextDocument) {
		finish("保存的链接已失效，请重新发送链接")
		return
	}
	message := &telegramMessage{ID: selection.MessageID, ThreadID: selection.ThreadID}
	message.Chat.ID, message.Chat.Type = selection.ChatID, "private"
	b.enqueueResourceBatch(state, selection.UpdateID, message, selection.Items, choice, selection.TextDocument, now)
	finish("已选择：" + choice.label() + "，任务已加入队列")
}

func pruneResourceSelections(selections []*pendingResourceSelection, now time.Time) []*pendingResourceSelection {
	kept := selections[:0]
	for _, selection := range selections {
		if selection != nil && now.Before(selection.Expires) {
			kept = append(kept, selection)
		}
	}
	clear(selections[len(kept):])
	return kept
}
