package service

import (
	"context"
	"fmt"
	"strconv"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/strm"
)

type ed2kPlaybackSourceKey struct{}
type ed2kSourceAccountKey struct{}

// WithED2KSourceAccount 固定本次授权请求的主来源凭据，后续配置变化不能替换该快照。
func WithED2KSourceAccount(ctx context.Context, account P115Account) context.Context {
	return context.WithValue(ctx, ed2kSourceAccountKey{}, account)
}

func ed2kSourceAccount(ctx context.Context) (P115Account, bool) {
	account, ok := ctx.Value(ed2kSourceAccountKey{}).(P115Account)
	return account, ok
}

type ed2kPlaybackSource struct {
	claim strm.ED2K
	load  func(context.Context) (*p115.DownloadInfo, error)
}

// playbackDeclaredSource 将两种完整内容声明交给同一秒传流程，不伪造云端文件位置。
func playbackDeclaredSource(ctx context.Context) (Play115Source, bool) {
	if declared, ok := ctx.Value(play115SourceKey{}).(Play115Source); ok {
		return declared, true
	}
	if source, ok := ctx.Value(ed2kPlaybackSourceKey{}).(ed2kPlaybackSource); ok {
		return Play115Source{SHA1: source.claim.SHA1, Size: source.claim.Size, Name: source.claim.Name}, true
	}
	return Play115Source{}, false
}

// ResolveED2KWithReport 优先复用固定目标的文件，仅在缺校验片段时调用缓存恢复来源。
func (s *PlaybackCache) ResolveED2KWithReport(ctx context.Context, selection *PlaybackCacheSelection, claim strm.ED2K, ua string, loadSource func(context.Context) (*p115.DownloadInfo, error)) (string, PlaybackResolution, error) {
	var report PlaybackResolution
	path, err := strm.ED2KPath(claim)
	if err != nil {
		return "", report, err
	}
	claim, _ = strm.ParseED2KPath(path)
	if selection == nil || selection.cookie == "" || selection.err != nil {
		return "", report, fmt.Errorf("未选择有效的播放目标账号")
	}
	report.AccountLabel = PlaybackAccountLabel(selection.accountName, selection.uid, selection.binding.AccountID)
	ctx = WithRedirect115SHA1(ctx, claim.SHA1)
	ctx = context.WithValue(ctx, ed2kPlaybackSourceKey{}, ed2kPlaybackSource{claim: claim, load: loadSource})
	ctx = context.WithValue(ctx, playbackSourceKey{}, DirectLinkKey("ed2k-copy", selection.DirectLinkScope(), claim.SHA1, strconv.FormatInt(claim.Size, 10)))
	// 外部 ED2K 声明不建立已验证的来源关联；账号副本按实际 UID 和文件 ID 核验。
	ctx = context.WithValue(ctx, playbackMaterialOrigin{}, "")
	s.logs.Add(ctx, "INFO", "ED2K STRM 已取得内容声明，先复用目标文件或秒传，未触发离线；目标账号="+report.AccountLabel)
	link, err := s.resolveInstant(ctx, selection, "", ua, &report, func() (*p115.DownloadInfo, error) {
		if loadSource == nil {
			return nil, p115.ErrPlaybackRangeUnavailable
		}
		return loadSource(ctx)
	})
	if err != nil {
		return "", PlaybackResolution{}, err
	}
	return link, report, nil
}

// PrepareED2KRestore 构造恢复主缓存所需资料，只使用已登记的管理员共享小号作为只读来源。
// 调用者必须固定写入账号与目录，并在 115 明确接受秒传且核验目标后调用返回的提交函数。
func (s *PlaybackCache) PrepareED2KRestore(ctx context.Context, claim strm.ED2K) (*p115.DownloadInfo, func() error, error) {
	path, err := strm.ED2KPath(claim)
	if err != nil {
		return nil, nil, err
	}
	claim, _ = strm.ParseED2KPath(path)
	snapshot := s.Snapshot()
	donors := make([]P115Account, 0, len(snapshot.donors))
	for _, account := range snapshot.donors {
		if account.OwnerUserID == "" && account.Ownership != "member" && !IsMemberAccount(account.ID) {
			donors = append(donors, account)
		}
	}
	selected := &PlaybackCacheSelection{donors: donors}
	ctx = context.WithValue(ctx, ed2kPlaybackSourceKey{}, ed2kPlaybackSource{claim: claim})
	ctx = context.WithValue(ctx, playbackMaterialOrigin{}, "")
	ctx = WithPlaybackSHA1Hint(ctx, claim.SHA1)
	info, _, commit, err := s.preparePlaybackMaterial(ctx, selected, &PlaybackResolution{}, func() (*p115.DownloadInfo, error) {
		return nil, p115.ErrPlaybackRangeUnavailable
	})
	return info, commit, err
}
