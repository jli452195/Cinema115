package bot

import (
	"context"
	"errors"
	"fmt"

	"strm115/backend/internal/service"
)

type cloudSubmissionPreparer interface {
	PrepareSubmission(service.CloudDownloadConfig) (service.CloudDownloadSubmission, error)
}

type shareTransferPreparer interface {
	PrepareTransfer(service.ShareTransferTarget) (service.ShareLinkTransferExecution, error)
}

var errTransferBatchResumed = errors.New("服务已恢复，但本批次启动时的凭据快照已丢失；未开始的链接已停止，请检查已提交结果后重新发送剩余链接")

// Prepared executions hold credentials only in memory, never in the JSON state.
// After a restart, started batches must not continue with newly read credentials.
func (b *Telegram) cloudBatchSubmission(batch *messageBatch) (service.CloudDownloadSubmission, error) {
	if batch.cloudSubmission != nil {
		return batch.cloudSubmission, nil
	}
	if batch.Next > 0 {
		return nil, errTransferBatchResumed
	}
	if b.CloudDownloads == nil {
		return nil, errors.New("115 云下载服务尚未初始化")
	}
	if batch.CloudTargetID != "" {
		current, ok := b.currentCloudTarget(batch.CloudTargetID)
		expected := TelegramTransferTarget{ID: batch.CloudTargetID, Name: batch.CloudTargetName,
			AccountID: batch.CloudTarget.AccountID, TargetCID: batch.CloudTarget.TargetCID, TargetPath: batch.CloudTarget.TargetPath}
		if !ok || !current.Ready || !sameCloudTarget(current, expected) {
			return nil, errors.New("转存目标已变化或不可用，请重新发送链接")
		}
	}
	var submission service.CloudDownloadSubmission
	if preparer, ok := b.CloudDownloads.(cloudSubmissionPreparer); ok {
		prepared, err := preparer.PrepareSubmission(batch.CloudTarget)
		if err != nil {
			return nil, fmt.Errorf("准备云下载批次: %w", err)
		}
		submission = prepared
	} else {
		submitter, config := b.CloudDownloads, batch.CloudTarget
		submission = func(ctx context.Context, rawURL string) (service.CloudDownloadReceipt, error) {
			return submitter.Submit(ctx, config, rawURL)
		}
	}
	batch.cloudSubmission = submission
	return submission, nil
}

func (b *Telegram) shareBatchExecution(batch *messageBatch) (service.ShareLinkTransferExecution, error) {
	if batch.shareExecution != nil {
		return batch.shareExecution, nil
	}
	if batch.Next > 0 {
		return nil, errTransferBatchResumed
	}
	if b.ShareTransfers == nil {
		return nil, errors.New("115 分享转存服务尚未初始化")
	}
	var execution service.ShareLinkTransferExecution
	if preparer, ok := b.ShareTransfers.(shareTransferPreparer); ok {
		prepared, err := preparer.PrepareTransfer(batch.ShareTarget)
		if err != nil {
			return nil, fmt.Errorf("准备分享转存批次: %w", err)
		}
		execution = prepared
	} else {
		transferer, target := b.ShareTransfers, batch.ShareTarget
		execution = func(ctx context.Context, rawURL, password string) (service.ShareLinkTransferReceipt, error) {
			return transferer.Transfer(ctx, target, rawURL, password)
		}
	}
	batch.shareExecution = execution
	return execution, nil
}
