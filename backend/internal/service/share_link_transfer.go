package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/strm"
)

type ShareLinkTransferClient interface {
	ListForTransfer(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error)
	ReceiveShare(context.Context, *p115.ShareInfo, []string, string, string) error
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
}

type ShareLinkTransferReceipt struct {
	TargetPath    string `json:"targetPath"`
	ItemCount     int    `json:"itemCount"`
	AcceptedItems int    `json:"acceptedItems"`
	BatchCount    int    `json:"batchCount"`
}

// 转存写请求失败时，上游仍可能已接收，调用方应先核对目录再决定是否重试。
var ErrShareTransferUncertain = errors.New("转存结果待核对")

// ShareLinkTransferExecution keeps task credentials only in process memory.
type ShareLinkTransferExecution func(context.Context, string, string) (ShareLinkTransferReceipt, error)

type ShareLinkTransferService struct {
	Accounts *P115Accounts
	Settings *ShareTransferSettings
	Client   ShareLinkTransferClient
}

func (s *ShareLinkTransferService) Ready() error {
	if s == nil || s.Accounts == nil || s.Settings == nil || s.Client == nil {
		return generationInputError("115 分享转存服务尚未初始化")
	}
	return nil
}

func (s *ShareLinkTransferService) Transfer(ctx context.Context, target ShareTransferTarget, rawURL, password string) (ShareLinkTransferReceipt, error) {
	transfer, err := s.PrepareTransfer(target)
	if err != nil {
		return ShareLinkTransferReceipt{TargetPath: target.TargetPath}, err
	}
	return transfer(ctx, rawURL, password)
}

// PrepareTransfer fixes the configured destination and credentials before the
// first link of a task. Later configuration edits cannot switch its account.
func (s *ShareLinkTransferService) PrepareTransfer(target ShareTransferTarget) (ShareLinkTransferExecution, error) {
	if err := s.Ready(); err != nil {
		return nil, err
	}
	if err := s.Settings.ValidateSnapshot(target); err != nil {
		return nil, err
	}
	account, err := s.Accounts.Primary(target.AccountID)
	if err != nil {
		return nil, err
	}
	uid := p115.CookieUserID(account.Cookie)
	if uid == "" || uid != p115.CookieUserID("UID="+account.UID) {
		return nil, generationInputError("无法确认主账号 UID，请重新登录 115")
	}
	return func(ctx context.Context, rawURL, password string) (ShareLinkTransferReceipt, error) {
		return s.transferSnapshot(ctx, target, account, rawURL, password)
	}, nil
}

func (s *ShareLinkTransferService) transferSnapshot(ctx context.Context, target ShareTransferTarget, account P115Account, rawURL, password string) (ShareLinkTransferReceipt, error) {
	receipt := ShareLinkTransferReceipt{TargetPath: target.TargetPath}
	share, err := p115.ParseShareURL(rawURL)
	if err != nil {
		return receipt, generationInputError(err.Error())
	}
	if strings.TrimSpace(password) != "" {
		share.Password = strings.TrimSpace(password)
	}
	if err := strm.ValidatePassword(share.Password); err != nil {
		return receipt, generationInputError(err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, ShareGenerationTimeout)
	defer cancel()

	if err := s.Accounts.ValidatePrimary(account.ID); err != nil {
		return receipt, err
	}
	targetPage, err := s.Client.DriveFiles(ctx, target.TargetCID, 0, account.Cookie)
	if err != nil {
		return receipt, fmt.Errorf("核验目标目录 %s 失败: %w", target.TargetPath, err)
	}
	if targetPage == nil {
		return receipt, errors.New("115 未返回目标目录内容，已停止转存")
	}

	var sourceRoot *p115.SharePage
	pages, err := readTransferDirectory(ctx, "0", func(ctx context.Context, cid string, offset int) (*p115.SharePage, error) {
		page, err := s.Client.ListForTransfer(ctx, share, cid, offset, account.Cookie)
		if offset == 0 {
			sourceRoot = page
		}
		return page, err
	})
	if err != nil {
		return receipt, fmt.Errorf("读取分享条目失败: %w", err)
	}
	if sourceRoot == nil {
		return receipt, errors.New("115 未返回分享目录信息，已停止转存")
	}
	owner, err := strconv.ParseUint(sourceRoot.OwnerID, 10, 64)
	if err != nil || owner == 0 {
		return receipt, errors.New("115 未返回有效分享者信息，已停止转存")
	}
	rootPages := make([]int, 0, len(pages))
	for offset := range pages {
		rootPages = append(rootPages, offset)
	}
	for i := 1; i < len(rootPages); i++ {
		for j := i; j > 0 && rootPages[j] < rootPages[j-1]; j-- {
			rootPages[j], rootPages[j-1] = rootPages[j-1], rootPages[j]
		}
	}
	roots := make([]p115.ShareFile, 0, sourceRoot.Count)
	for _, offset := range rootPages {
		roots = append(roots, pages[offset].Files...)
	}
	if len(roots) == 0 {
		return receipt, generationInputError("分享中没有可转存的条目")
	}
	receipt.ItemCount = len(roots)

	for start := 0; start < len(roots); start += 100 {
		if err := s.Accounts.ValidatePrimary(account.ID); err != nil {
			return receipt, err
		}
		end := min(start+100, len(roots))
		ids := make([]string, 0, end-start)
		for _, item := range roots[start:end] {
			ids = append(ids, item.ID)
		}
		if err := s.Client.ReceiveShare(ctx, share, ids, target.TargetCID, account.Cookie); err != nil {
			return receipt, fmt.Errorf("提交到 %s 的第 %d 批转存失败；已确认提交 %d/%d 个条目，本批结果可能不确定，请先检查目标目录再重试: %w", target.TargetPath, start/100+1, receipt.AcceptedItems, receipt.ItemCount, errors.Join(ErrShareTransferUncertain, err))
		}
		receipt.AcceptedItems += len(ids)
		receipt.BatchCount++
	}
	return receipt, nil
}
