package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"strm115/backend/internal/client/p115"
)

// CloudDownloadBatchSize 是本项目单次提交分组上限。
const CloudDownloadBatchSize = 100

type CloudDownloadBatchEvent struct {
	Type             string `json:"type"`
	Index            int    `json:"index"`
	Total            int    `json:"total"`
	Accepted         bool   `json:"accepted,omitempty"`
	OutcomeUncertain bool   `json:"outcomeUncertain,omitempty"`
	Error            string `json:"error,omitempty"`
	Stopped          bool   `json:"stopped,omitempty"`
}

// CloudDownloadBatchExecution 由网页和 Bot 共用，凭据仅固定在内存中。
type CloudDownloadBatchExecution interface {
	Run(context.Context, func(CloudDownloadBatchEvent) error) error
	SubmitChunk(context.Context, int) ([]CloudDownloadBatchEvent, bool)
}

type cloudDownloadBatch struct {
	service   *CloudDownloadService
	targetCID string
	account   P115Account
	urls      []string
	resources []p115.CloudResource
}

func (s *CloudDownloadService) PrepareBatch(config CloudDownloadConfig, urls []string) (CloudDownloadBatchExecution, error) {
	if len(urls) == 0 {
		return nil, generationInputError("请至少选择一条云下载链接")
	}
	account, err := s.snapshot(config)
	if err != nil {
		return nil, err
	}
	batch := &cloudDownloadBatch{service: s, targetCID: strings.TrimSpace(config.TargetCID), account: account, urls: make([]string, 0, len(urls)), resources: make([]p115.CloudResource, 0, len(urls))}
	seen := make(map[p115.CloudResource]bool, len(urls))
	// 整批在写入前验证。拒绝重复输入以保留原链接与回执索引的对应关系。
	for i, raw := range urls {
		resource, err := p115.ParseCloudResource(raw)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条云下载链接: %w", i+1, err)
		}
		if seen[resource] {
			return nil, generationInputError("批量云下载包含重复资源，请去重后提交")
		}
		seen[resource] = true
		batch.urls = append(batch.urls, strings.TrimSpace(raw))
		batch.resources = append(batch.resources, resource)
	}
	return batch, nil
}

// Run 每次发送一个分组，开始事件全部发出后才向 115 写入该组。
func (b *cloudDownloadBatch) Run(ctx context.Context, emit func(CloudDownloadBatchEvent) error) error {
	total := len(b.urls)
	if err := emit(CloudDownloadBatchEvent{Type: "ready", Total: total}); err != nil {
		return err
	}
	stopped := false
	for start := 0; start < total; start += CloudDownloadBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		for index := start; index < min(start+CloudDownloadBatchSize, total); index++ {
			if err := emit(CloudDownloadBatchEvent{Type: "start", Index: index, Total: total}); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		events, stop := b.SubmitChunk(ctx, start)
		for _, event := range events {
			if err := emit(event); err != nil {
				return err
			}
		}
		if stop {
			stopped = true
			break
		}
	}
	return emit(CloudDownloadBatchEvent{Type: "done", Total: total, Stopped: stopped})
}

func (b *cloudDownloadBatch) SubmitChunk(ctx context.Context, start int) ([]CloudDownloadBatchEvent, bool) {
	if start < 0 || start >= len(b.urls) {
		return nil, true
	}
	ctx, cancel := context.WithTimeout(ctx, CloudDownloadTimeout)
	defer cancel()
	end := min(start+CloudDownloadBatchSize, len(b.urls))
	resources := b.resources[start:end]
	events := make([]CloudDownloadBatchEvent, len(resources))
	for i := range events {
		events[i] = CloudDownloadBatchEvent{Type: "result", Index: start + i, Total: len(b.urls)}
	}
	release, err := b.service.acquireDirectResources(ctx, b.account, resources)
	if err != nil {
		return reportCloudChunkError(events, err)
	}
	defer release()
	if len(resources) == 1 {
		if err := b.service.addDirectURL(ctx, b.account.Cookie, b.urls[start], b.targetCID); err != nil {
			return reportCloudChunkError(events, err)
		}
		events[0].Accepted = true
		return events, false
	}
	receipts, err := b.service.Client.AddURLs(ctx, b.account.Cookie, b.urls[start:end], b.targetCID)
	if err != nil {
		return reportCloudChunkError(events, directCloudSubmitError(err))
	}
	return events, applyCloudChunkReceipts(events, resources, receipts)
}

func reportCloudChunkError(events []CloudDownloadBatchEvent, err error) ([]CloudDownloadBatchEvent, bool) {
	uncertain := CloudDownloadUncertain(err)
	for i := range events {
		events[i].OutcomeUncertain = uncertain
		events[i].Error = CloudDownloadErrorMessage(err)
	}
	var upstream *p115.HTTPError
	var deferred *p115.CloudRequestDeferredError
	if uncertain || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrGenerationInput) ||
		errors.Is(err, p115.ErrCloudSubmissionRejected) || errors.As(err, &deferred) {
		return events, true
	}
	if errors.As(err, &upstream) {
		switch upstream.Status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusTooManyRequests:
			return events, true
		}
	}
	return events, false
}

func applyCloudChunkReceipts(events []CloudDownloadBatchEvent, resources []p115.CloudResource, receipts []p115.CloudURLAddResult) bool {
	const rejected = "115 拒绝提交该链接"
	seen := make([]bool, len(events))
	for _, receipt := range receipts {
		i := cloudChunkReceiptIndex(resources, receipt)
		if i < 0 {
			continue
		}
		if seen[i] && events[i].Accepted == receipt.Rejected {
			events[i].OutcomeUncertain = true
		}
		seen[i] = true
		events[i].Accepted = !receipt.Rejected
		if receipt.Rejected {
			events[i].Error = rejected
		} else {
			events[i].Error = ""
		}
	}
	stopped := false
	for i := range events {
		if !seen[i] || events[i].OutcomeUncertain {
			events[i].Accepted = false
			events[i].OutcomeUncertain = true
			events[i].Error = "115 未确认该链接的提交结果"
			stopped = true
		}
	}
	return stopped
}

// 回执按实际资源匹配，返回顺序和请求数量不能作为提交成功依据。
func cloudChunkReceiptIndex(resources []p115.CloudResource, receipt p115.CloudURLAddResult) int {
	fromURL := strings.TrimSpace(receipt.URL) != ""
	fromHash := strings.TrimSpace(receipt.InfoHash) != ""
	if !fromHash && (!fromURL || !receipt.Rejected && !receipt.Confirmed) {
		return -1
	}
	var resource p115.CloudResource
	if fromURL {
		var err error
		resource, err = p115.ParseCloudResource(receipt.URL)
		if err != nil {
			return -1
		}
	}
	match := -1
	for i, submitted := range resources {
		if fromURL && resource != submitted || fromHash && !submitted.MatchesTaskHash(receipt.InfoHash) {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = i
	}
	return match
}
