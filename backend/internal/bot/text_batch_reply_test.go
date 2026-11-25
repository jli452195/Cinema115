package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/service"
)

func TestTextShareBatchSendsOnlyFinalSummary(t *testing.T) {
	const source = "https://115.com/s/private?password=secr"
	const secret = "sensitive-extra"
	var b *Telegram
	b = newBatchTestBot(t, batchGeneratorFunc(func(_ context.Context, input service.GenerateInput) (*service.GenerationResult, error) {
		disk := readTextBatchCheckpoint(t, b)
		if disk.Pending == nil || !strings.Contains(disk.Pending.Text, "待核对") || strings.Contains(disk.Pending.Text, source) || disk.Batch.Items[disk.Batch.Next-1].URL != "" {
			t.Fatal("调用前需要保存脱敏的中断进度")
		}
		if input.URL == source {
			return nil, fmt.Errorf("上游错误：%s %s", source, secret)
		}
		return &service.GenerationResult{Written: 1}, nil
	}))
	b.Logs = &service.GenerationLogs{}
	state := telegramState{Batch: &messageBatch{
		Mode: "direct", TextDocument: true, ChatID: 42, UpdateID: 61, MessageID: 71, StatusMessageID: 81,
		ShareSettings: shareTestSnapshot(t, b), Items: []messageShare{{URL: source}, {URL: "https://115.com/s/ok"}},
	}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	disk := readTextBatchCheckpoint(t, b)
	if state.Pending != nil || disk.Pending != nil || disk.Batch.Next != 1 || disk.Batch.TextResult.Failed != 1 {
		t.Fatalf("首条结果没有只保存计数：%+v", disk)
	}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || state.Pending.EditMessageID != 81 || !strings.Contains(state.Pending.Text, "成功 1 条，失败 1 条") || strings.Contains(state.Pending.Text, secret) {
		t.Fatalf("附件结果没有输出一次脱敏汇总：%+v", state.Pending)
	}
	logs := b.Logs.Snapshot("ERROR", "Telegram")
	if len(logs) != 1 || strings.Contains(logs[0].Message, source) || strings.Contains(logs[0].Message, secret) {
		t.Fatalf("日志缺少失败条目或泄露附件内容：%+v", logs)
	}
}

type textMergedTestGenerator struct {
	mergedTestGenerator
	prepared int
}

func (g *textMergedTestGenerator) PrepareTelegramGeneration(service.AutomationSettings) (func(context.Context) context.Context, error) {
	g.prepared++
	return func(ctx context.Context) context.Context { return ctx }, nil
}

func TestTextMergedBatchPartitionsAndPersistsCheckpoints(t *testing.T) {
	var b *Telegram
	var sizes []int
	gen := &textMergedTestGenerator{mergedTestGenerator: mergedTestGenerator{
		single: func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
			t.Fatal("合并分享不应回退逐条生成")
			return nil, nil
		},
		merged: func(_ context.Context, inputs []service.GenerateInput) ([]service.BatchOutcome, error) {
			disk := readTextBatchCheckpoint(t, b)
			processed := len(sizes) * telegramTextBatchSize
			if disk.Batch.Next != processed+len(inputs) || disk.Pending == nil || disk.Batch.TextResult.Succeeded != processed {
				t.Fatalf("新一段调用前未保存完整游标与之前的结果：%+v", disk)
			}
			if inputs[0].URL != fmt.Sprintf("https://115.com/s/share%d", processed) || inputs[0].Mode != "reshare" {
				t.Fatalf("分段输入顺序变化：%+v", inputs[0])
			}
			sizes = append(sizes, len(inputs))
			outcomes := make([]service.BatchOutcome, len(inputs))
			for i := range outcomes {
				outcomes[i].Result = &service.GenerationResult{Written: 1, ReshareURL: fmt.Sprintf("https://115.com/s/new%d", len(sizes))}
			}
			return outcomes, nil
		},
	}}
	b = newBatchTestBot(t, gen)
	settings := b.Settings.Value()
	settings.ReshareEnabled = true
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	items := make([]messageShare, 245)
	for i := range items {
		items[i].URL = fmt.Sprintf("https://115.com/s/share%d", i)
	}
	state := telegramState{Batch: &messageBatch{Mode: "reshare", TextDocument: true, ChatID: 42, ShareSettings: shareTestSnapshot(t, b), Items: items}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{100, 100, 45}) || gen.prepared != 1 || state.Pending == nil || !strings.Contains(state.Pending.Text, "成功 245 条，失败 0 条") {
		t.Fatalf("分段、固定凭据或最终汇总错误：%v，准备次数 %d，回执 %+v", sizes, gen.prepared, state.Pending)
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(state.Pending.Text, fmt.Sprintf("https://115.com/s/new%d", i)) {
			t.Fatalf("第 %d 段的新链接遗漏", i)
		}
	}
}

func TestCloudBatchReplyLimitsLongFailureDetails(t *testing.T) {
	b := newBatchTestBot(t, nil)
	batch := &messageBatch{Mode: "cloud", Items: make([]messageShare, 100)}
	for i := range batch.Items {
		b.recordCloudBatchResult(batch, "115 云下载", service.CloudDownloadBatchEvent{Index: i, Error: strings.Repeat("🎬", 500)})
	}
	reply := batch.formatSummary("115 云下载提交结果", 0)
	if u := telegramTextUnits(reply); u > 3900 || !strings.Contains(reply, "已提交 0 条，失败 100 条") || !strings.Contains(reply, "另有 95 条") {
		t.Fatalf("失败回执长度或总数错误：%d 单元", u)
	}
}

func readTextBatchCheckpoint(t *testing.T, b *Telegram) telegramState {
	t.Helper()
	data, err := os.ReadFile(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var state telegramState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
