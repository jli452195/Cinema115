package bot

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/service"
)

type pendingShareTransferSelection struct {
	UpdateID        int64                    `json:"updateID"`
	ChatID          int64                    `json:"chatID"`
	UserID          int64                    `json:"userID"`
	MessageID       int64                    `json:"messageID"`
	PromptMessageID int64                    `json:"promptMessageID"`
	ThreadID        int64                    `json:"threadID,omitempty"`
	Expires         time.Time                `json:"expires"`
	Items           []messageShare           `json:"items"`
	Targets         []TelegramTransferTarget `json:"targets"`
}

func (b *Telegram) enqueueShareTransferMessage(state *telegramState, update telegramUpdate, message *telegramMessage, items []messageShare, now time.Time) {
	if message.Chat.Type != "private" {
		b.queueTextReply(state, update, message, "115 分享转存只允许在 Bot 私聊中使用。")
		return
	}
	if len(b.ShareTransferTargets) == 0 {
		b.queueTextReply(state, update, message, "此 Bot 尚未分配 115 分享转存目标，请到 Telegram Bot 页面分配目标目录。")
		return
	}
	ready := make([]TelegramTransferTarget, 0, len(b.ShareTransferTargets))
	for _, target := range b.ShareTransferTargets {
		if target.Ready {
			ready = append(ready, target)
		}
	}
	if len(ready) == 0 {
		hint := b.ShareTransferTargets[0].SetupHint
		if hint == "" {
			hint = "请检查分享转存目标配置"
		}
		b.queueTextReply(state, update, message, "没有可用的 115 分享转存目标。"+hint)
		return
	}
	if len(state.Queue) >= 100 {
		b.queueTextReply(state, update, message, "Bot 任务队列已满，请稍后重新发送。")
		return
	}
	if len(ready) == 1 {
		b.enqueueShareTransferBatch(state, update.ID, message, items, ready[0], now)
		return
	}
	if len(state.ShareTransferSelections) >= maxPendingCloudSelections {
		b.queueTextReply(state, update, message, "当前待选择的转存消息较多，请稍后重新发送。")
		return
	}
	selection := &pendingShareTransferSelection{
		UpdateID: update.ID, ChatID: message.Chat.ID, UserID: message.From.ID, MessageID: message.ID,
		ThreadID: message.ThreadID, Expires: now.Add(cloudSelectionLifetime), Targets: ready,
		Items: append([]messageShare(nil), items...),
	}
	state.ShareTransferSelections = append(state.ShareTransferSelections, selection)
	keyboard := make([][]map[string]string, 0, len(ready))
	for _, target := range ready {
		keyboard = append(keyboard, []map[string]string{{"text": target.Name, "callback_data": shareTransferCallbackData(update.ID, target.ID)}})
	}
	state.Replies = append(state.Replies, &pendingReply{
		UpdateID: update.ID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Text:        fmt.Sprintf("识别到 %d 个 115 分享链接，请选择接收目录（10 分钟内有效）：", len(items)),
		ReplyMarkup: map[string]any{"inline_keyboard": keyboard}, CaptureMessageID: true, ShareTransferSelectionUpdateID: update.ID,
	})
}

func (b *Telegram) enqueueShareTransferBatch(state *telegramState, updateID int64, message *telegramMessage, items []messageShare, target TelegramTransferTarget, now time.Time) {
	batch := &messageBatch{
		UpdateID: updateID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Mode: "share_transfer", ReadyAt: now,
		ShareTarget: service.ShareTransferTarget{
			ID: target.ID, Name: target.Name, AccountID: target.AccountID, TargetCID: target.TargetCID,
			TargetPath: target.TargetPath, AssignedBotID: target.AssignedBotID,
		},
		Items: append([]messageShare(nil), items...),
	}
	for range items {
		batch.SourceMessageIDs = append(batch.SourceMessageIDs, message.ID)
	}
	state.Queue = append(state.Queue, batch)
}

func shareTransferCallbackData(updateID int64, targetID string) string {
	return "share:" + strconv.FormatInt(updateID, 10) + ":" + targetID
}

func (b *Telegram) enqueueShareTransferCallback(state *telegramState, callback *telegramCallbackQuery, now time.Time) {
	if callback == nil || callback.ID == "" {
		return
	}
	answer := func(text string) {
		state.CallbackAnswers = append(state.CallbackAnswers, &pendingCallbackAnswer{ID: callback.ID, Text: text})
	}
	if callback.Message == nil {
		answer("此转存选择已失效，请重新发送链接")
		return
	}
	if !b.AllowedChats[callback.Message.Chat.ID] {
		answer("当前聊天没有使用权限")
		return
	}
	parts := strings.Split(callback.Data, ":")
	if len(parts) != 3 || parts[0] != "share" {
		answer("无法识别此选择")
		return
	}
	updateID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		answer("此转存选择已失效，请重新发送链接")
		return
	}
	index := -1
	for i, selection := range state.ShareTransferSelections {
		if selection != nil && selection.UpdateID == updateID {
			index = i
			break
		}
	}
	if index < 0 {
		answer("此转存选择已失效，请重新发送链接")
		return
	}
	selection := state.ShareTransferSelections[index]
	if selection.ChatID != callback.Message.Chat.ID || selection.UserID != callback.From.ID || selection.PromptMessageID == 0 || selection.PromptMessageID != callback.Message.ID {
		answer("请由发送原链接的用户选择转存目标")
		return
	}
	if !now.Before(selection.Expires) {
		state.ShareTransferSelections = removeShareTransferSelection(state.ShareTransferSelections, index)
		answer("选择已超时，请重新发送链接")
		return
	}
	var selected TelegramTransferTarget
	found := false
	for _, target := range selection.Targets {
		if target.ID == parts[2] {
			selected, found = target, true
			break
		}
	}
	if !found {
		answer("此转存目标无效，请重新发送链接")
		return
	}
	current, ok := b.currentShareTransferTarget(selected.ID)
	if !ok || !current.Ready || !sameTransferTarget(current, selected) {
		state.ShareTransferSelections = removeShareTransferSelection(state.ShareTransferSelections, index)
		answer("转存目标已变化或不可用，请重新发送链接")
		return
	}
	if len(state.Queue) >= 100 {
		answer("Bot 任务队列已满，请稍后重新发送")
		return
	}
	message := &telegramMessage{ID: selection.MessageID, ThreadID: selection.ThreadID}
	message.Chat.ID, message.Chat.Type = selection.ChatID, "private"
	b.enqueueShareTransferBatch(state, selection.UpdateID, message, selection.Items, current, now)
	state.ShareTransferSelections = removeShareTransferSelection(state.ShareTransferSelections, index)
	answer("已选择接收目录：" + current.Name)
}

func (b *Telegram) currentShareTransferTarget(id string) (TelegramTransferTarget, bool) {
	if b.CurrentShareTransferTarget != nil {
		return b.CurrentShareTransferTarget(id)
	}
	for _, target := range b.ShareTransferTargets {
		if target.ID == id {
			return target, true
		}
	}
	return TelegramTransferTarget{}, false
}

func sameTransferTarget(a, b TelegramTransferTarget) bool {
	return a.ID == b.ID && a.Name == b.Name && a.AccountID == b.AccountID && a.TargetCID == b.TargetCID && a.TargetPath == b.TargetPath && a.AssignedBotID == b.AssignedBotID
}

func removeShareTransferSelection(selections []*pendingShareTransferSelection, index int) []*pendingShareTransferSelection {
	copy(selections[index:], selections[index+1:])
	return selections[:len(selections)-1]
}

func pruneShareTransferSelections(selections []*pendingShareTransferSelection, now time.Time) []*pendingShareTransferSelection {
	kept := selections[:0]
	for _, selection := range selections {
		if selection != nil && now.Before(selection.Expires) {
			kept = append(kept, selection)
		}
	}
	return kept
}
