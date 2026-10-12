package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"strm115/backend/internal/client/p115"
)

const CloudDownloadBatchLimit = 100

type CloudDownloadBatchEvent struct {
	Type             string `json:"type"`
	Index            int    `json:"index"`
	Total            int    `json:"total"`
	Accepted         bool   `json:"accepted,omitempty"`
	RemovedOldTask   bool   `json:"removedOldTask,omitempty"`
	OutcomeUncertain bool   `json:"outcomeUncertain,omitempty"`
	Error            string `json:"error,omitempty"`
	Stopped          bool   `json:"stopped,omitempty"`
}

// Credentials remain private and in memory for the lifetime of one request.
type CloudDownloadBatch struct {
	service *CloudDownloadService
	config  CloudDownloadConfig
	account P115Account
	urls    []string
	indexes []int
}

func (s *CloudDownloadService) PrepareBatch(config CloudDownloadConfig, urls []string) (*CloudDownloadBatch, error) {
	if len(urls) == 0 || len(urls) > CloudDownloadBatchLimit {
		return nil, generationInputError("每批请选择 1 至 100 条云下载链接")
	}
	account, err := s.snapshot(config)
	if err != nil {
		return nil, err
	}
	batch := &CloudDownloadBatch{service: s, config: config, account: account, urls: make([]string, 0, len(urls)), indexes: make([]int, 0, len(urls))}
	seen := make(map[string]bool, len(urls))
	// Validate every link before the first write. The UI deduplicates selection;
	// reject duplicate API input instead of silently changing receipt indexes.
	for i, raw := range urls {
		resource, err := p115.ParseCloudResource(raw)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条云下载链接: %w", i+1, err)
		}
		key := resource.Protocol + ":" + resource.Hash
		if seen[key] {
			return nil, generationInputError("批量云下载包含重复资源，请去重后提交")
		}
		seen[key] = true
		batch.urls = append(batch.urls, raw)
		batch.indexes = append(batch.indexes, i)
	}
	return batch, nil
}

// Run is sequential and request-scoped. A broken stream or uncertain upstream
// result stops unsent work; accepted tasks are never automatically retried.
func (b *CloudDownloadBatch) Run(ctx context.Context, emit func(CloudDownloadBatchEvent) error) error {
	total := len(b.urls)
	if err := emit(CloudDownloadBatchEvent{Type: "ready", Total: total}); err != nil {
		return err
	}
	stopped := false
	for i, raw := range b.urls {
		if err := ctx.Err(); err != nil {
			return err
		}
		index := b.indexes[i]
		if err := emit(CloudDownloadBatchEvent{Type: "start", Index: index, Total: total}); err != nil {
			return err
		}
		receipt, submitErr := b.service.submitSnapshot(ctx, b.config, b.account, raw)
		event := CloudDownloadBatchEvent{Type: "result", Index: index, Total: total, Accepted: submitErr == nil, RemovedOldTask: receipt.RemovedOldTask}
		if submitErr != nil {
			event.Error = CloudDownloadErrorMessage(submitErr)
			event.OutcomeUncertain = CloudDownloadUncertain(submitErr)
			if receipt.RemovedOldTask {
				event.Error = "旧任务记录已移除，原有网盘文件已保留。" + event.Error
			}
			var upstream *p115.HTTPError
			stopped = event.OutcomeUncertain || (errors.As(submitErr, &upstream) && (upstream.Status == http.StatusUnauthorized || upstream.Status == http.StatusForbidden || upstream.Status == http.StatusTooManyRequests))
		}
		if err := emit(event); err != nil {
			return err
		}
		if stopped {
			break
		}
	}
	return emit(CloudDownloadBatchEvent{Type: "done", Total: total, Stopped: stopped})
}
