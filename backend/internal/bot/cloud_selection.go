package bot

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/service"
)

const cloudSelectionLifetime = 10 * time.Minute
const maxPendingCloudSelections = 50

type pendingCloudSelection struct {
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

type pendingCallbackAnswer struct {
	ID   string `json:"id"`
	Text string `json:"text,omitempty"`
}

func (b *Telegram) enqueueCloudMessage(state *telegramState, update telegramUpdate, message *telegramMessage, links []string, now time.Time) {
	if message.Chat.Type != "private" {
		b.queueTextReply(state, update, message, "⚠️ 磁力转存只允许在 Bot 私聊中使用，请私聊发送 /cloud。")
		return
	}
	if b.CloudDownloads == nil || len(b.CloudDownloadTargets) == 0 {
		b.queueTextReply(state, update, message, "⚠️ 此 Bot 尚未分配磁力转存配置，请到 Telegram Bot 页面分配转存目标。")
		return
	}
	if len(links) == 0 {
		b.queueTextReply(state, update, message, "请发送 magnet 磁力链接或 ED2K 链接。\n\n直接发送或使用 /cloud <链接>，都会默认执行智能删除重试。")
		return
	}
	ready := make([]TelegramTransferTarget, 0, len(b.CloudDownloadTargets))
	for _, target := range b.CloudDownloadTargets {
		if target.Ready {
			ready = append(ready, target)
		}
	}
	if len(ready) == 0 {
		hint := b.CloudDownloadTargets[0].SetupHint
		if hint == "" {
			hint = "请到磁力/ED2K 转存页面检查转存目标配置"
		}
		b.queueTextReply(state, update, message, "⚠️ 没有可用的磁力转存目标。"+hint)
		return
	}
	if len(state.Queue) >= 100 {
		b.queueTextReply(state, update, message, "Bot 任务队列已满，请稍后重新发送。")
		return
	}
	if len(ready) == 1 {
		b.enqueueCloudBatch(state, update.ID, message, links, ready[0], now)
		return
	}
	if len(state.CloudSelections) >= maxPendingCloudSelections {
		b.queueTextReply(state, update, message, "⚠️ 当前待选择的转存消息较多，请稍后重新发送。")
		return
	}
	selection := &pendingCloudSelection{UpdateID: update.ID, ChatID: message.Chat.ID, UserID: message.From.ID, MessageID: message.ID, ThreadID: message.ThreadID, Expires: now.Add(cloudSelectionLifetime), Targets: ready}
	selection.Items = make([]messageShare, 0, len(links))
	for _, link := range links {
		selection.Items = append(selection.Items, messageShare{URL: link})
	}
	state.CloudSelections = append(state.CloudSelections, selection)
	keyboard := make([][]map[string]string, 0, len(ready))
	for _, target := range ready {
		keyboard = append(keyboard, []map[string]string{{"text": target.Name, "callback_data": cloudTargetCallbackData(update.ID, target.ID)}})
	}
	state.Replies = append(state.Replies, &pendingReply{
		UpdateID: update.ID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Text:        fmt.Sprintf("识别到 %d 个磁力或 ED2K 链接，请选择转存目标（10 分钟内有效）：", len(links)),
		ReplyMarkup: map[string]any{"inline_keyboard": keyboard}, CaptureMessageID: true, CloudSelectionUpdateID: update.ID,
	})
}

func (b *Telegram) enqueueCloudBatch(state *telegramState, updateID int64, message *telegramMessage, links []string, target TelegramTransferTarget, now time.Time) {
	batch := &messageBatch{
		UpdateID: updateID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Mode: "cloud", CloudRedo: true, ReadyAt: now, CloudTargetID: target.ID, CloudTargetName: target.Name,
		CloudTarget: service.CloudDownloadConfig{AccountID: target.AccountID, TargetCID: target.TargetCID, TargetPath: target.TargetPath},
	}
	for _, link := range links {
		batch.Items = append(batch.Items, messageShare{URL: link})
		batch.SourceMessageIDs = append(batch.SourceMessageIDs, message.ID)
	}
	state.Queue = append(state.Queue, batch)
}

func cloudTargetCallbackData(updateID int64, targetID string) string {
	return "cloud:" + strconv.FormatInt(updateID, 10) + ":" + targetID
}

func (b *Telegram) enqueueCloudCallback(state *telegramState, update telegramUpdate, callback *telegramCallbackQuery, now time.Time) {
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
	if len(parts) != 3 || parts[0] != "cloud" {
		answer("无法识别此选择")
		return
	}
	updateID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		answer("此转存选择已失效，请重新发送链接")
		return
	}
	index := -1
	for i, selection := range state.CloudSelections {
		if selection != nil && selection.UpdateID == updateID {
			index = i
			break
		}
	}
	if index < 0 {
		answer("此转存选择已失效，请重新发送链接")
		return
	}
	selection := state.CloudSelections[index]
	if selection.ChatID != callback.Message.Chat.ID || selection.UserID != callback.From.ID || selection.PromptMessageID == 0 || selection.PromptMessageID != callback.Message.ID {
		answer("请由发送原链接的用户选择转存目标")
		return
	}
	if !now.Before(selection.Expires) {
		state.CloudSelections = removeCloudSelection(state.CloudSelections, index)
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
	current, ok := b.currentCloudTarget(selected.ID)
	if !ok || !current.Ready || !sameCloudTarget(current, selected) {
		state.CloudSelections = removeCloudSelection(state.CloudSelections, index)
		answer("转存目标已变化或不可用，请重新发送链接")
		return
	}
	if len(state.Queue) >= 100 {
		answer("Bot 任务队列已满，请稍后重新发送")
		return
	}
	message := &telegramMessage{ID: selection.MessageID, ThreadID: selection.ThreadID}
	message.Chat.ID = selection.ChatID
	message.Chat.Type = "private"
	links := make([]string, 0, len(selection.Items))
	for _, item := range selection.Items {
		links = append(links, item.URL)
	}
	b.enqueueCloudBatch(state, selection.UpdateID, message, links, current, now)
	state.CloudSelections = removeCloudSelection(state.CloudSelections, index)
	answer("已选择：" + current.Name)
}

func sameCloudTarget(a, b TelegramTransferTarget) bool {
	return a.ID == b.ID && a.Name == b.Name && a.AccountID == b.AccountID && a.TargetCID == b.TargetCID && a.TargetPath == b.TargetPath
}

func (b *Telegram) currentCloudTarget(id string) (TelegramTransferTarget, bool) {
	if b.CurrentCloudTarget != nil {
		return b.CurrentCloudTarget(id)
	}
	for _, target := range b.CloudDownloadTargets {
		if target.ID == id {
			return target, true
		}
	}
	return TelegramTransferTarget{}, false
}

func removeCloudSelection(selections []*pendingCloudSelection, index int) []*pendingCloudSelection {
	copy(selections[index:], selections[index+1:])
	return selections[:len(selections)-1]
}

func pruneCloudSelections(selections []*pendingCloudSelection, now time.Time) []*pendingCloudSelection {
	kept := selections[:0]
	for _, selection := range selections {
		if selection != nil && now.Before(selection.Expires) {
			kept = append(kept, selection)
		}
	}
	return kept
}
