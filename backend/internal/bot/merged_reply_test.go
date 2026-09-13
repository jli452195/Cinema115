package bot

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestMergedReplySummarizesSuccessReviewAndCounts(t *testing.T) {
	inputs := []service.GenerateInput{{URL: "https://115.com/s/a", Password: "abcd"}, {URL: "https://115.com/s/b"}, {URL: "https://115.com/s/c"}}
	out := []service.BatchOutcome{
		{Result: &service.GenerationResult{ReshareURL: "https://115.com/s/own", Cleaned: true, Written: 3, MetadataWritten: 2}},
		{Err: fmt.Errorf("%w；已安排审核重试", p115.ErrShareUnderReview)},
		{Result: &service.GenerationResult{ReshareURL: "https://115.com/s/own", Cleaned: true, Written: 1}},
	}
	text, preview := formatMergedReply(inputs, out)
	for _, want := range []string{"成功 2 个，失败 1 个", "https://115.com/s/a?password=abcd", "https://115.com/s/b", "已安排审核重试", "生成 STRM：成功 4 个，失败 0 个", "元数据：成功 2 个，失败 0 个"} {
		if !strings.Contains(text, want) {
			t.Fatal("汇总缺少内容", want, text)
		}
	}
	if strings.Count(text, "https://115.com/s/own") != 1 || preview != "https://115.com/s/own" || strings.Contains(text, "【1/3】") {
		t.Fatal("重复新链接或未合并回复", text)
	}
}

func TestMergedReplyBoundedWithoutLosingFooter(t *testing.T) {
	inputs := make([]service.GenerateInput, 200)
	out := make([]service.BatchOutcome, 200)
	for i := range inputs {
		inputs[i].URL = fmt.Sprintf("https://115.com/s/link%d?password=abcd", i)
		if i%2 == 0 {
			out[i].Result = &service.GenerationResult{Written: 1, ReshareURL: "https://115.com/s/own", Cleaned: true}
		} else {
			out[i].Err = errors.New(strings.Repeat("出错🎬", 1000))
		}
	}
	text, _ := formatMergedReply(inputs, out)
	if telegramTextUnits(text) > 4096 || !strings.Contains(text, "详见网页任务记录") || !strings.Contains(text, "新链接：https://115.com/s/own") || !strings.Contains(text, "成功 100 个，失败 100 个") || !strings.Contains(text, "生成 STRM：成功 100 个，失败 0 个") {
		t.Fatal("长批次超过 Telegram 限制或丢失汇总")
	}
}

func TestMergedReplyDoesNotClaimFailedSnapshotPassed(t *testing.T) {
	input := []service.GenerateInput{{URL: "https://115.com/s/a"}}
	out := []service.BatchOutcome{{Result: &service.GenerationResult{Written: 2, Cleaned: true, ReshareURL: "https://115.com/s/own"}, Err: errors.New("删除后快照复查失败")}}
	text, _ := formatMergedReply(input, out)
	if strings.Contains(text, "校验成功") || !strings.Contains(text, "已有文件生成") {
		t.Fatal("错误地宣称分享校验通过", text)
	}
	out[0] = service.BatchOutcome{Err: errors.New("转存失败"), Result: &service.GenerationResult{TempDirectoryID: "123"}}
	text, _ = formatMergedReply(input, out)
	if !strings.Contains(text, "临时目录保留") {
		t.Fatal("没有提示保留临时目录")
	}
}
