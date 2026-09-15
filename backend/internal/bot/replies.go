package bot

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func shortReply(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// 展示有效提取码，保证正文中复制的原链接与实际处理的输入一致。
func replySource(input service.GenerateInput) string {
	link := strings.TrimSpace(input.URL)
	if input.Password != "" {
		if parsed, err := url.Parse(link); err == nil {
			query := parsed.Query()
			query.Set("password", strings.TrimSpace(input.Password))
			parsed.RawQuery = query.Encode()
			link = parsed.String()
		}
	}
	return link
}

// 每条结果独立统计，批次序号区分多链接任务，不把未处理文件记为失败。
func formatShareReply(input service.GenerateInput, settings service.AutomationSettings, result *service.GenerationResult, err error, index, total int) (string, string) {
	reshare := input.Mode == "reshare" || input.Mode == "" && settings.ReshareEnabled
	if result != nil && result.Mode == "direct" {
		reshare = false
	}
	if result != nil && result.ReshareURL != "" {
		reshare = true
	}
	operation := "生成 STRM"
	if reshare {
		operation = "转存再分享再生成 STRM"
	}
	if result == nil && err == nil {
		err = errors.New("生成器未返回处理结果")
	}
	source := replySource(input)
	preview := source
	var text strings.Builder
	if err == nil {
		fmt.Fprintf(&text, "✅ 【115网盘】%s 完成", operation)
	} else if result != nil && result.Written > 0 {
		fmt.Fprintf(&text, "⚠️ 【115网盘】%s 未完成（已有文件生成）", operation)
	} else {
		fmt.Fprintf(&text, "❌ 【115网盘】%s 失败", operation)
	}
	if total > 1 {
		fmt.Fprintf(&text, "【%d/%d】", index, total)
	}
	if err != nil {
		reason := err.Error()
		if errors.Is(err, p115.ErrShareUnderReview) {
			message := "分享链接审核中，无法生成 STRM"
			if result != nil && result.Written > 0 {
				message = "分享链接审核中，后续处理已停止"
			}
			reason = strings.ReplaceAll(reason, p115.ErrShareUnderReview.Error(), message)
		}
		reason = strings.Join(strings.Fields(reason), " ")
		fmt.Fprintf(&text, "\n\n❌ 错误：%s\n🔗 原链接：%s", shortReply(reason, 500), source)
	} else {
		fmt.Fprintf(&text, "\n\n📊 链接统计：成功 1 个，失败 0 个\n\n✅ 成功链接：\n• %s", source)
	}
	if result != nil {
		if result.ReshareURL != "" {
			if err == nil {
				text.WriteString("\n\n✅ 目录校验成功\n🔗 新链接：")
			} else {
				text.WriteString("\n\n🔗 已创建的分享：")
			}
			text.WriteString(result.ReshareURL)
			if err == nil {
				preview = result.ReshareURL
			}
		}
		fmt.Fprintf(&text, "\n\n📁 生成 STRM：成功 %d 个，失败 %d 个\n📦 元数据：成功 %d 个，失败 %d 个", result.Written, result.Failed, result.MetadataWritten, result.MetadataFailed)
		if remaining := len(result.Files) - result.Written - result.Failed; remaining > 0 {
			fmt.Fprintf(&text, "\n⏸ STRM 未处理：%d 个", remaining)
		}
		if remaining := len(result.MetadataFiles) - result.MetadataWritten - result.MetadataFailed; remaining > 0 {
			fmt.Fprintf(&text, "\n⏸ 元数据未处理：%d 个", remaining)
		}
		if result.Cleaned {
			text.WriteString("\n🗑 本次临时目录已移入回收站")
		} else if result.TempDirectoryID != "" {
			fmt.Fprintf(&text, "\n📂 临时目录 ID：%s，请按任务记录核对清理状态", result.TempDirectoryID)
		}
	}
	return shortReply(text.String(), 1800), preview
}
