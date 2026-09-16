package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestShareReplyFormats(t *testing.T) {
	input := service.GenerateInput{URL: "https://115.com/s/source", Password: "ayss"}
	settings := service.AutomationSettings{ReshareEnabled: true}
	result := &service.GenerationResult{Written: 10, ReshareURL: "https://115cdn.com/s/own?password=fwgl", Cleaned: true, MetadataWritten: 2}
	text, preview := formatShareReply(input, settings, result, nil, 1, 1)
	for _, want := range []string{"✅ 【115网盘】转存再分享再生成 STRM 完成", "链接统计：成功 1 个，失败 0 个", "https://115.com/s/source?password=ayss", "目录校验成功", "生成 STRM：成功 10 个，失败 0 个", "元数据：成功 2 个，失败 0 个", "回收站"} {
		if !strings.Contains(text, want) {
			t.Error("成功回复缺少字段", want)
		}
	}
	if preview != result.ReshareURL {
		t.Error("成功消息应预览新分享")
	}
	err := fmt.Errorf("读取原分享: %w；已安排审核重试，30 分钟后执行", p115.ErrShareUnderReview)
	text, preview = formatShareReply(input, settings, nil, err, 2, 3)
	for _, want := range []string{"❌ 【115网盘】", "失败【2/3】", "分享链接审核中，无法生成 STRM", "已安排审核重试", "https://115.com/s/source?password=ayss"} {
		if !strings.Contains(text, want) {
			t.Error("审核失败提示不完整", want)
		}
	}
	if strings.Contains(text, "校验成功") || preview != "https://115.com/s/source?password=ayss" {
		t.Error("失败消息误报成功或预览错误")
	}
	result.Cleaned = false
	result.TempDirectoryID = "123"
	result.MetadataFailed = 1
	text, _ = formatShareReply(input, settings, result, errors.New("字幕保存失败"), 1, 1)
	if !strings.HasPrefix(text, "⚠️") || !strings.Contains(text, "元数据：成功 2 个，失败 1 个") || !strings.Contains(text, "已创建的分享") || !strings.Contains(text, "临时目录 ID：123") || strings.Contains(text, "校验成功") {
		t.Fatal("部分成功的回复不准确", text)
	}
	settings.FixedSharePassword = "ayss"
	text, _ = formatShareReply(input, settings, &service.GenerationResult{Written: 1, Mode: "direct"}, nil, 1, 1)
	if !strings.Contains(text, "【115网盘】生成 STRM 完成") || strings.Contains(text, "转存再分享") {
		t.Fatal("本人原分享直接生成时错误显示转存模式")
	}
	text, _ = formatShareReply(input, settings, &service.GenerationResult{Written: 1}, nil, 1, 1)
	if !strings.Contains(text, "转存再分享") {
		t.Fatal("不能按密码匹配推断实际处理模式")
	}
}

func TestShareReplyLimitsAndIncompleteCounts(t *testing.T) {
	input := service.GenerateInput{URL: "https://115.com/s/a"}
	result := &service.GenerationResult{Written: 1, Failed: 1, Files: make([]service.GeneratedSTRM, 5)}
	text, _ := formatShareReply(input, service.AutomationSettings{}, result, errors.New(strings.Repeat("❌ 具体失败原因", 2000)), 1, 1)
	if len(utf16.Encode([]rune(text))) > 4096 || !strings.Contains(text, "STRM 未处理：3 个") || !strings.Contains(text, input.URL) {
		t.Fatal("长错误丢失关键内容或超过 Telegram 限制")
	}
	text, _ = formatShareReply(input, service.AutomationSettings{}, nil, nil, 1, 1)
	if !strings.Contains(text, "生成器未返回处理结果") {
		t.Fatal("无结果不能报成功")
	}
}

func TestShareReplyDescribesCacheMetadataWarningAndConflict(t *testing.T) {
	input := service.GenerateInput{URL: "https://115.com/s/source"}
	cached, _ := formatShareReply(input, service.AutomationSettings{}, &service.GenerationResult{Cached: true, Written: 2, ReshareURL: "https://115.com/s/own", Cleaned: true}, nil, 1, 1)
	if !strings.Contains(cached, "复用已生成 STRM") || !strings.Contains(cached, "已复用本地成功快照") || strings.Contains(cached, "目录校验成功") || strings.Contains(cached, "本次临时目录") || strings.Contains(cached, "新增") {
		t.Fatal("缓存结果文案不准确", cached)
	}
	warning, _ := formatShareReply(input, service.AutomationSettings{}, &service.GenerationResult{Written: 2, MetadataWritten: 1, MetadataFailed: 1, ConflictRenamed: 2}, nil, 1, 1)
	for _, want := range []string{"⚠️", "元数据部分失败", "再次提交重试", "已保留旧文件，另存 2 个"} {
		if !strings.Contains(warning, want) {
			t.Fatal("部分完成文案缺失", want, warning)
		}
	}
}

func TestPendingReplyQuotePreviewAndLegacyCompatibility(t *testing.T) {
	pending := &pendingReply{ChatID: 42, MessageID: 700, ThreadID: 99, PreviewURL: "https://115.com/s/a", Text: "结果"}
	encoded, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	var restored pendingReply
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	request := restored.request()
	reply := request["reply_parameters"].(map[string]any)
	preview := request["link_preview_options"].(map[string]any)
	if reply["message_id"] != int64(700) || reply["allow_sending_without_reply"] != true || request["message_thread_id"] != int64(99) || preview["url"] != pending.PreviewURL || preview["is_disabled"] != false {
		t.Fatal("持久化后引用或预览丢失")
	}
	legacy := (&pendingReply{ChatID: 42, Text: "旧结果"}).request()
	if _, ok := legacy["reply_parameters"]; ok {
		t.Fatal("旧任务不能伪造引用编号")
	}
	if legacy["link_preview_options"].(map[string]any)["is_disabled"] != true {
		t.Fatal("开始提示或旧消息不应强制预览")
	}
}

func TestBatchResultKeepsOriginalMessageReference(t *testing.T) {
	b := newBatchTestBot(t, batchGeneratorFunc(func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
		return &service.GenerationResult{Written: 1}, nil
	}))
	state := telegramState{Batch: &messageBatch{UpdateID: 10, ChatID: 42, MessageID: 700, ThreadID: 99, Items: []messageShare{{URL: "https://115.com/s/a"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Pending.MessageID != 700 || state.Pending.ThreadID != 99 {
		t.Fatal("结果引用丢失")
	}
}
