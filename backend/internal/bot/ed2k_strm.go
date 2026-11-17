package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

// ED2KSTRMImporter 只持久保存导入请求，云端缓存及 STRM 输出由后台任务处理。
type ED2KSTRMImporter interface {
	Import(context.Context, []string) ([]model.ED2KSTRMResource, error)
}

func isED2KSTRMCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	command := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	return command == "/ed2k_strm" || command == "/ed2kstrm"
}

func parseED2KSTRMMessage(message *telegramMessage) []string {
	text, entities := message.Text, message.Entities
	if text == "" {
		text, entities = message.Caption, message.CaptionEntities
	}
	links := ed2kLinkPattern.FindAllString(text, -1)
	// 隐藏链接只读取明确的 ED2K 目标，不跟随跳转或使用显示名称猜测目标。
	for _, entity := range entities {
		if entity.Type == "text_link" && strings.HasPrefix(strings.ToLower(entity.URL), "ed2k://") {
			links = append(links, entity.URL)
		}
	}
	result := make([]string, 0, len(links))
	seen := make(map[string]bool, len(links))
	for _, link := range links {
		normalized, err := p115.ValidateCloudURL(link)
		if err != nil || seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	return result
}

func (b *Telegram) enqueueED2KSTRMMessage(state *telegramState, update telegramUpdate, message *telegramMessage, now time.Time) {
	if b.ED2KSTRM == nil {
		b.queueTextReply(state, update, message, "ED2K STRM 模块尚未启用，请先在管理页配置。")
		return
	}
	links := parseED2KSTRMMessage(message)
	if len(links) == 0 {
		b.queueTextReply(state, update, message, "请使用 /ed2k_strm 或 /ed2kstrm，后面附上 ED2K 文件链接。每条消息最多导入 50 个链接。")
		return
	}
	if len(links) > 50 {
		b.queueTextReply(state, update, message, "每条消息最多导入 50 个 ED2K 链接，请拆分后发送。")
		return
	}
	if len(state.Queue) >= 100 {
		b.queueTextReply(state, update, message, "ED2K STRM 接收队列已满，请稍后重试。")
		return
	}
	items := make([]messageShare, 0, len(links))
	for _, link := range links {
		items = append(items, messageShare{URL: link})
	}
	state.Queue = append(state.Queue, &messageBatch{
		UpdateID: update.ID, ChatID: message.Chat.ID, MessageID: message.ID, ThreadID: message.ThreadID,
		Mode: "ed2k_strm", ReadyAt: now, Items: items,
	})
	b.Logs.Append("Telegram", fmt.Sprintf("消息-%d", message.ID), "ED2K STRM", "INFO", fmt.Sprintf("收到 ED2K STRM 导入请求，识别 %d 个链接并加入队列", len(items)))
}

func (b *Telegram) processED2KSTRM(ctx context.Context, state *telegramState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	batch := state.Batch
	links := make([]string, 0, len(batch.Items)-batch.Next)
	for _, item := range batch.Items[batch.Next:] {
		links = append(links, item.URL)
	}
	text := "ED2K STRM 模块当前不可用，请到管理页检查配置后重新发送。"
	if b.ED2KSTRM != nil {
		// Import 按内容身份幂等入队；取消或保存结果前重启时可以安全重试。
		resources, err := b.ED2KSTRM.Import(ctx, links)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			text = "ED2K STRM 导入未完成，请到管理页检查主账号、输出设置和资源记录后重新发送。"
			b.Logs.Append("Telegram", fmt.Sprintf("导入-%d", batch.UpdateID), "ED2K STRM", "ERROR", "ED2K STRM 导入未完成，请检查管理页资源记录及模块配置")
		} else {
			ready, pending, attention := 0, 0, 0
			type resourceKey struct {
				hash string
				size int64
			}
			seen := make(map[resourceKey]bool, len(resources))
			for _, resource := range resources {
				key := resourceKey{hash: strings.ToLower(resource.Hash), size: resource.Size}
				if seen[key] {
					continue
				}
				seen[key] = true
				switch resource.Status {
				case "ready":
					ready++
				case "queued", "downloading", "recovering":
					pending++
				default:
					attention++
				}
			}
			text = fmt.Sprintf("ED2K STRM 导入已接收：%d 个资源。\n已生成：%d 个；后台处理：%d 个。", len(seen), ready, pending)
			if attention > 0 {
				text += fmt.Sprintf("\n另有 %d 个资源需要检查状态。", attention)
			}
			text += "\n生成状态和输出文件请查看管理页。"
			b.Logs.Append("Telegram", fmt.Sprintf("导入-%d", batch.UpdateID), "ED2K STRM", "INFO", fmt.Sprintf("ED2K STRM 导入已接收，%d 个资源，%d 个已生成", len(seen), ready))
		}
	}
	batch.Next = len(batch.Items)
	state.Pending = &pendingReply{UpdateID: batch.UpdateID, ChatID: batch.ChatID, MessageID: batch.MessageID, ThreadID: batch.ThreadID, Text: text}
	if err := b.save(*state); err != nil {
		return fmt.Errorf("保存 Telegram ED2K STRM 导入结果: %w", err)
	}
	return nil
}
