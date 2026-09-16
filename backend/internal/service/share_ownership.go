package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"strm115/backend/internal/client/p115"
)

type generationFirstPageKey struct{}

type generationFirstPage struct {
	ShareCode string
	CID       string
	Page      *p115.SharePage
}

type generationSnapshotKey struct{}

type generationSnapshotPageKey struct {
	CID    string
	Offset int
}

// generationSnapshot 只在一次任务内复用已读取且媒体数量确认的分享目录，不持久化 Cookie 或远端响应。
type generationSnapshot struct {
	ShareCode string
	RootCID   string
	Roots     []p115.ShareFile
	Files     []p115.ShareFile
	Pages     map[generationSnapshotPageKey]*p115.SharePage
}

// 单条和批次使用同一次带 Cookie 的目录响应判断归属及审核，禁止用提取码猜测归属。
func (w *ShareWorkflow) inspectSource(ctx context.Context, share *p115.ShareInfo, cid, cookie string) (*p115.SharePage, bool, error) {
	uid := p115.CookieUserID(cookie)
	if uid == "" {
		return nil, false, generationInputError("所选 Cookie 缺少有效用户 ID，请重新登录 115")
	}
	page, err := w.Client.ListForTransfer(ctx, share, cid, 0, cookie)
	if err != nil {
		if errors.Is(err, p115.ErrShareUnderReview) {
			err = errors.Join(err, errSourceShareReview)
		}
		return nil, false, fmt.Errorf("读取原分享: %w", err)
	}
	if page == nil {
		return nil, false, errors.New("115 未返回原分享目录")
	}
	owner, err := strconv.ParseUint(page.OwnerID, 10, 64)
	if err != nil || owner == 0 {
		return nil, false, errors.New("115 未返回有效的分享者 user_id，无法判断归属，已停止本条任务")
	}
	return page, strconv.FormatUint(owner, 10) == uid, nil
}

// 首次归属检查的目录页交给后续扫描复用，避免重复请求原分享首页。
func (w *ShareWorkflow) sourcePages(share *p115.ShareInfo, cid, cookie string, first *p115.SharePage) func(context.Context, string, int) (*p115.SharePage, error) {
	if cid == "" {
		cid = "0"
	}
	return func(ctx context.Context, directory string, offset int) (*p115.SharePage, error) {
		if directory == cid && offset == 0 {
			return first, nil
		}
		return w.Client.ListForTransfer(ctx, share, directory, offset, cookie)
	}
}

func (w *ShareWorkflow) generateOriginal(ctx context.Context, input GenerateInput, cookie string, first *p115.SharePage) (*GenerationResult, error) {
	input.Mode = "direct"
	if first != nil {
		share, err := p115.ParseShareURL(input.URL)
		if err != nil {
			return nil, generationInputError(err.Error())
		}
		cid := strings.TrimSpace(input.CID)
		if cid == "" {
			cid = "0"
		}
		ctx = context.WithValue(ctx, generationFirstPageKey{}, generationFirstPage{ShareCode: share.Code, CID: cid, Page: first})
	}
	result, err := w.Generator.Generate(context.WithValue(ctx, generationCookieKey{}, cookie), input)
	if result == nil {
		result = &GenerationResult{}
		if err == nil {
			err = errors.New("生成器未返回处理结果")
		}
	}
	result.Mode = "direct"
	return result, err
}
