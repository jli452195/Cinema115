package bot

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

// 每个合并批次仅显示一次结果，正文预留统计和新链接，超长列表留在任务记录中。
func formatMergedReply(inputs []service.GenerateInput, outcomes []service.BatchOutcome) (string, string) {
	success, failure := make([]string, 0, len(inputs)), make([]string, 0, len(inputs))
	written, failed, metadata, metadataFailed, conflictRenamed, remaining := 0, 0, 0, 0, 0, 0
	newURL, tempID := "", ""
	cleaned := true
	ownFailed := false
	direct := 0
	cached := 0
	for i, out := range outcomes {
		link := replySource(inputs[i])
		if out.Result != nil && out.Result.Mode == "direct" {
			direct++
			link += "（原分享直接生成）"
		}
		if out.Result != nil && out.Result.Cached {
			cached++
			link += "（已复用快照）"
		}
		if out.Err == nil && out.Result != nil {
			success = append(success, "• "+link)
		} else {
			reason := "生成器未返回结果"
			if out.Err != nil {
				reason = strings.Join(strings.Fields(out.Err.Error()), " ")
				if errors.Is(out.Err, p115.ErrShareUnderReview) {
					reason = "分享链接审核中，无法生成 STRM"
					if strings.Contains(out.Err.Error(), "已安排审核重试") {
						reason += "；已安排审核重试"
					}
				}
			}
			failure = append(failure, "• "+link+"\n  原因："+shortReply(reason, 180))
		}
		if r := out.Result; r != nil {
			written += r.Written
			failed += r.Failed
			metadata += r.MetadataWritten
			metadataFailed += r.MetadataFailed
			conflictRenamed += r.ConflictRenamed
			remaining += max(0, len(r.Files)-r.Written-r.Failed)
			if r.ReshareURL != "" {
				newURL = r.ReshareURL
			}
			if r.TempDirectoryID != "" {
				tempID = r.TempDirectoryID
			}
			if (r.ReshareURL != "" || r.TempDirectoryID != "") && !r.Cleaned {
				cleaned = false
			}
			if r.ReshareURL != "" && out.Err != nil {
				ownFailed = true
			}
		}
	}
	action := "转存再分享再生成 STRM"
	if direct > 0 {
		action = "分享生成 STRM"
	}
	if cached == len(inputs) && len(inputs) > 0 {
		action = "复用已生成 STRM"
	}
	title := "✅ 【115网盘】" + action + " 完成"
	if len(failure) > 0 {
		title = "⚠️ 【115网盘】" + action + " 部分完成"
	} else if metadataFailed > 0 {
		title = "⚠️ 【115网盘】" + action + " 完成（元数据部分失败，可再次提交重试）"
	}
	if len(success) == 0 {
		title = "❌ 【115网盘】" + action + " 失败"
		if written > 0 {
			title = "⚠️ 【115网盘】" + action + " 未完成（已有文件生成）"
		}
	}
	header := fmt.Sprintf("%s\n\n📊 链接统计：成功 %d 个，失败 %d 个", title, len(success), len(failure))
	foot := ""
	if newURL != "" {
		if cached == len(inputs) && len(inputs) > 0 {
			foot = "\n\n✅ 已复用本地成功快照\n🔗 原成功分享：" + newURL
		} else if cleaned && !ownFailed {
			foot = "\n\n✅ 校验成功\n🔗 新链接：" + newURL
		} else {
			foot = "\n\n🔗 已创建的分享：" + newURL
		}
	}
	foot += fmt.Sprintf("\n\n📁 生成 STRM：成功 %d 个，失败 %d 个\n📦 元数据：成功 %d 个，失败 %d 个", written, failed, metadata, metadataFailed)
	if conflictRenamed > 0 {
		foot += fmt.Sprintf("\n🛡 同名内容不同：已保留旧文件，另存 %d 个", conflictRenamed)
	}
	if remaining > 0 {
		foot += fmt.Sprintf("\n⏸ STRM 未处理：%d 个", remaining)
	}
	if tempID != "" && !cleaned {
		foot += "\n📂 临时目录保留，请查看任务记录"
	}
	budget := 3900 - telegramTextUnits(header+foot) - 100
	sections := make([]string, 0, 2)
	groups := [][]string{success, failure}
	labels := []string{"✅ 成功链接：", "❌ 失败链接："}
	for i, group := range groups {
		if len(group) == 0 {
			continue
		}
		limit := budget
		if i == 0 && len(failure) > 0 {
			limit = budget / 2
		}
		section := "\n\n" + labels[i]
		shown := 0
		for _, line := range group {
			if telegramTextUnits(section+"\n"+line) > limit-60 {
				break
			}
			section += "\n" + line
			shown++
		}
		if shown < len(group) {
			section += fmt.Sprintf("\n…另有 %d 条，详见网页任务记录", len(group)-shown)
		}
		sections = append(sections, section)
		budget -= telegramTextUnits(section)
	}
	preview := newURL
	if preview == "" && len(inputs) > 0 {
		preview = replySource(inputs[0])
	}
	return header + strings.Join(sections, "") + foot, preview
}

func telegramTextUnits(text string) int { return len(utf16.Encode([]rune(text))) }
