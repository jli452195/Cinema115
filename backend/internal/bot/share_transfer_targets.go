package bot

import (
	"strm115/backend/internal/service"
	"time"
)

func (b *Telegram) enqueueShareTransferBatch(state *telegramState, updateID int64, message *telegramMessage, items []messageShare, target TelegramTransferTarget, textDocument bool, now time.Time) {
	batch := &messageBatch{
		UpdateID: updateID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Mode: "share_transfer", ReadyAt: now, TextDocument: textDocument,
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
