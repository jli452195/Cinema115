package bot

import (
	"context"
	"errors"
	"fmt"

	"strm115/backend/internal/service"
)

type shareTransferPreparer interface {
	PrepareTransfer(service.ShareTransferTarget) (service.ShareLinkTransferExecution, error)
}

var (
	errTransferBatchResumed    = errors.New("服务已恢复，但本批次启动时的凭据快照已丢失；未开始的链接已停止，请检查已提交结果后重新发送剩余链接")
	errCloudBatchTargetChanged = errors.New("转存目标已变化或不可用，请重新发送链接")
	errCloudBatchUnavailable   = errors.New("115 云下载服务尚未初始化")
)

// 执行闭包仅在内存中固定凭据；重启后不得为已开始批次重新读取凭据。
func (b *Telegram) cloudBatchExecution(batch *messageBatch) (service.CloudDownloadBatchExecution, error) {
	if batch.cloudExecution != nil {
		return batch.cloudExecution, nil
	}
	if batch.Next > 0 {
		return nil, errTransferBatchResumed
	}
	if b.CloudDownloads == nil {
		return nil, errCloudBatchUnavailable
	}
	if batch.CloudTargetID != "" {
		current, ok := b.currentCloudTarget(batch.CloudTargetID)
		expected := TelegramTransferTarget{ID: batch.CloudTargetID, Name: batch.CloudTargetName,
			AccountID: batch.CloudTarget.AccountID, TargetCID: batch.CloudTarget.TargetCID, TargetPath: batch.CloudTarget.TargetPath}
		if !ok || current.AssignedBotID != b.BotID || !current.Ready || !sameCloudTarget(current, expected) {
			return nil, errCloudBatchTargetChanged
		}
	}
	urls := make([]string, len(batch.Items))
	for i, item := range batch.Items {
		urls[i] = item.URL
	}
	execution, err := b.CloudDownloads.PrepareBatch(batch.CloudTarget, urls)
	if err != nil {
		return nil, fmt.Errorf("准备云下载批次: %w", err)
	}
	batch.cloudExecution = execution
	return execution, nil
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
	expected := TelegramTransferTarget{ID: batch.ShareTarget.ID, Name: batch.ShareTarget.Name, AccountID: batch.ShareTarget.AccountID,
		TargetCID: batch.ShareTarget.TargetCID, TargetPath: batch.ShareTarget.TargetPath, AssignedBotID: batch.ShareTarget.AssignedBotID}
	if hint := b.resourceChoiceHint(resourceChoice{Kind: "share_transfer", ShareTarget: &expected}, ""); hint != "" {
		return nil, errors.New(hint)
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
