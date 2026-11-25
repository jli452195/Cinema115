package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

// ED2KSTRMImporter 只持久保存导入请求，云端缓存及 STRM 输出由后台任务处理。
type ED2KSTRMImporter interface {
	ImportToTarget(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error)
}

// ED2KSTRMModule 将 Bot 的目标分配与持久导入入口集中在同一模块。
type ED2KSTRMModule interface {
	ED2KSTRMImporter
	Targets() []model.ED2KSTRMTarget
	Target(string) (model.ED2KSTRMTarget, bool)
	AssignTarget(context.Context, string, string) error
}

func isED2KSTRMCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	command := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	return command == "/ed2k_strm" || command == "/ed2kstrm"
}

func (b *Telegram) currentED2KSTRMTarget(id string) (model.ED2KSTRMTarget, bool) {
	var target model.ED2KSTRMTarget
	ok := false
	if b.CurrentED2KSTRMTarget != nil {
		target, ok = b.CurrentED2KSTRMTarget(id)
	} else {
		for _, configured := range b.ED2KSTRMTargets {
			if configured.ID == id {
				target, ok = configured, true
				break
			}
		}
	}
	return target, ok && b.BotID != "" && target.AssignedBotID == b.BotID
}

func sameED2KSTRMTarget(a, b model.ED2KSTRMTarget) bool {
	return a.ID == b.ID && a.Name == b.Name && a.OutputDir == b.OutputDir && a.AssignedBotID == b.AssignedBotID
}

func (b *Telegram) enqueueED2KSTRMBatch(state *telegramState, updateID int64, message *telegramMessage, items []messageShare, target model.ED2KSTRMTarget, textDocument bool, now time.Time) {
	state.Queue = append(state.Queue, &messageBatch{
		UpdateID: updateID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Mode: "ed2k_strm", ReadyAt: now, TextDocument: textDocument, Items: items, ED2KSTRMTarget: target, ED2KRequestID: b.ed2kSTRMRequestID(updateID, target.ID),
	})
	b.Logs.Append("Telegram", fmt.Sprintf("消息-%d", message.ID), "ED2K STRM", "INFO", fmt.Sprintf("收到 ED2K STRM 导入请求，识别 %d 个链接并加入输出目录队列", len(items)))
}

// 令牌的数字前缀是 Bot 身份；更换同一 Bot 的令牌不改变消息的幂等请求编号。
func (b *Telegram) ed2kSTRMRequestID(updateID int64, targetID string) string {
	identity := strings.SplitN(b.Token, ":", 2)[0]
	sum := sha256.Sum256([]byte("cinema115-ed2k-strm-telegram\x00" + identity + "\x00" + strconv.FormatInt(updateID, 10) + "\x00" + targetID))
	return hex.EncodeToString(sum[:])
}

func (b *Telegram) processED2KSTRM(ctx context.Context, state *telegramState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	batch := state.Batch
	links := make([]string, 0, len(batch.Items))
	for _, item := range batch.Items {
		links = append(links, item.URL)
	}
	text := "ED2K STRM 模块当前不可用，请到管理页检查配置后重新发送。"
	current, assigned := b.currentED2KSTRMTarget(batch.ED2KSTRMTarget.ID)
	if !assigned || !sameED2KSTRMTarget(current, batch.ED2KSTRMTarget) || batch.ED2KRequestID == "" || batch.ED2KRequestID != b.ed2kSTRMRequestID(batch.UpdateID, current.ID) {
		text = "ED2K STRM 输出目录或 Bot 分配已变化，本批次未提交，请重新发送链接。"
	} else if b.ED2KSTRM != nil {
		// 固定目标与消息请求编号；取消或结果落盘前重启时复用原输出任务。
		result, err := b.ED2KSTRM.ImportToTarget(ctx, batch.ED2KSTRMTarget, links, batch.ED2KRequestID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			text = "ED2K STRM 导入未完成，请到管理页检查主账号、输出目录和任务记录后重新发送。"
			b.Logs.Append("Telegram", fmt.Sprintf("导入-%d", batch.UpdateID), "ED2K STRM", "ERROR", "ED2K STRM 导入未完成，请检查管理页资源记录及模块配置")
		} else {
			text = fmt.Sprintf("ED2K STRM 输出任务已接收：%d 个。\n目标：%s\n输出目录：%s\n处理进度请查看管理页的任务记录。", len(result.Tasks), current.Name, current.OutputDir)
			b.Logs.Append("Telegram", fmt.Sprintf("导入-%d", batch.UpdateID), "ED2K STRM", "INFO", fmt.Sprintf("ED2K STRM 已接收 %d 个输出任务", len(result.Tasks)))
		}
	}
	batch.Next = len(batch.Items)
	state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID, Text: text}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram ED2K STRM 导入结果: %w", err)
	}
	return nil
}
