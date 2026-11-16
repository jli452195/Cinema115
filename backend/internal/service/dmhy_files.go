package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/dmhy"
	"strm115/backend/internal/utils/torrentmeta"
)

// 用户选择整理后才读取原帖及真实种子，不借用聚合站详情或列表中的大小作为文件身份。
func (s *DmhyService) Files(ctx context.Context, in DownloadResourceInput) (DownloadFiles, error) {
	if err := ctx.Err(); err != nil {
		return DownloadFiles{}, err
	}
	_, err := dmhy.DetailPath(in.ProviderID)
	if err != nil || in.Source != "dmhy" || in.Provider != "" || in.TorrentURL != "" {
		return DownloadFiles{}, ErrOrganizeInput
	}
	hash, err := btresource.MagnetHash(in.Magnet)
	if err != nil {
		return DownloadFiles{}, fmt.Errorf("%w：磁力身份无效", ErrOrganizeInput)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	detail, _, _, err := s.readDetail(ctx, in.ProviderID)
	if err != nil {
		return DownloadFiles{}, err
	}
	// 缓存命中也重新核对调用方选择，不能让同一原帖接受不同磁力声明。
	if err := validateDmhyDetail(detail, in.ProviderID, hash); err != nil {
		return DownloadFiles{}, err
	}
	if detail.TorrentURL == "" {
		return DownloadFiles{}, fmt.Errorf("%w：原帖未提供可读取的种子，可使用普通云下载", ErrOrganizeInput)
	}
	torrentURL, err := btresource.TorrentURL("dmhy", hash, detail.TorrentURL)
	if err != nil {
		return DownloadFiles{}, fmt.Errorf("%w：动漫花园种子地址无效", ErrOrganizeInput)
	}
	var meta torrentmeta.Metadata
	torrent, cached, err := s.torrents.readKey(ctx, hash, torrentURL, 30*time.Minute, false, nil, func(body []byte) error {
		var parseErr error
		meta, parseErr = parseDownloadTorrent(body, hash)
		return parseErr
	})
	if err != nil {
		return DownloadFiles{}, fmt.Errorf("读取动漫花园种子清单: %w", err)
	}
	// 首次读取已校验实际 info 内容；缓存或合并命中仍核验当前调用的 hash。
	if cached {
		meta, err = parseDownloadTorrent(torrent.body, hash)
		if err != nil {
			return DownloadFiles{}, err
		}
	}
	return downloadTorrentMetadata(meta)
}

func validateDmhyDetail(detail dmhy.Detail, id, hash string) error {
	actual, err := btresource.MagnetHash(detail.Magnet)
	if err != nil || detail.ID != id || actual != hash {
		return fmt.Errorf("%w：动漫花园原帖与所选磁力身份不一致，请核对原帖", ErrOrganizeInput)
	}
	return nil
}

type dmhyTorrentTransport struct{ site *DmhyService }

func (t dmhyTorrentTransport) Fetch(ctx context.Context, endpoint string, _ int64) ([]byte, error) {
	if t.site == nil || t.site.TorrentReader == nil {
		return nil, fmt.Errorf("%w：动漫花园种子读取尚未配置", ErrOrganizeInput)
	}
	// 种子文件名只约束读取目标，后续仍用实际 info 内容核对 BTIH。
	hash := strings.TrimSuffix(path.Base(endpoint), ".torrent")
	return t.site.TorrentReader.ReadTorrent(ctx, "dmhy", hash, endpoint)
}
