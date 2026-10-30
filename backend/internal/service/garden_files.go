package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/utils/torrentmeta"
)

// Files is lazy: search never fetches resource details. Verified lists share the
// site's bounded cache by normalized BTIH, including base32/hex aliases.
func (s *GardenService) Files(ctx context.Context, in DownloadResourceInput) (DownloadFiles, error) {
	if err := ctx.Err(); err != nil {
		return DownloadFiles{}, err
	}
	requestPath, err := garden.DetailPath(in.Provider, in.ProviderID)
	if err != nil {
		return DownloadFiles{}, ErrOrganizeInput
	}
	resource, err := p115.ParseCloudResource(in.Magnet)
	if err != nil || resource.Protocol != "magnet" {
		return DownloadFiles{}, ErrOrganizeInput
	}
	key := "files:" + resource.Hash
	read, _, err := s.readKey(ctx, key, requestPath, 30*time.Minute, false, nil, func(body []byte) error {
		d, e := garden.ParseResourceDetail(body, in.Provider, in.ProviderID)
		if e != nil {
			return e
		}
		r, e := p115.ParseCloudResource(d.Resource.Magnet)
		if e != nil || r.Protocol != "magnet" || r.Hash != resource.Hash {
			return garden.ErrInvalid
		}
		return nil
	})
	if err != nil {
		return DownloadFiles{}, err
	}
	var d garden.ResourceDetail
	if err := json.Unmarshal(read.body, &d); err != nil {
		return DownloadFiles{}, err
	}
	actual, err := p115.ParseCloudResource(d.Resource.Magnet)
	if err != nil || actual.Protocol != "magnet" || actual.Hash != resource.Hash {
		return DownloadFiles{}, garden.ErrInvalid
	}
	out := DownloadFiles{Hash: resource.Hash, Files: []OrganizeFile{}, Subtitles: []OrganizeSubtitle{}}
	if d.IsDeleted {
		out.Reason = "Garden 资源已被删除"
		return out, nil
	}
	if d.Detail.HasMoreFiles || len(d.Detail.Files) == 0 {
		postKey := resource.Hash
		if d.Resource.Provider == "mikan" {
			postKey = d.Resource.ProviderID
		}
		for _, link := range d.Detail.Magnets {
			endpoint, err := btresource.TorrentURL(d.Resource.Provider, postKey, link.URL)
			if err != nil {
				continue
			}
			read, _, err := s.torrents.readKey(ctx, resource.Hash, d.Resource.Provider+":"+endpoint, 30*time.Minute, false, nil, func(body []byte) error {
				_, err := parseDownloadTorrent(body, resource.Hash)
				return err
			})
			if err != nil {
				return DownloadFiles{}, fmt.Errorf("读取原站种子完整清单失败: %w", err)
			}
			return downloadTorrentFiles(read.body, resource.Hash)
		}
		out.Reason = "Garden 清单缺失或不完整，且未提供可用的原站种子"
		return out, nil
	}
	files := make([]torrentmeta.File, 0, len(d.Detail.Files))
	for _, file := range d.Detail.Files {
		files = append(files, torrentmeta.File{Path: file.Name})
	}
	return organizeResourceFiles(resource.Hash, files)
}

type gardenTorrentTransport struct{ reader btresource.TorrentReader }

func (t gardenTorrentTransport) Fetch(ctx context.Context, key string, _ int64) ([]byte, error) {
	provider, endpoint, _ := strings.Cut(key, ":")
	postKey := strings.TrimSuffix(path.Base(endpoint), ".torrent")
	return t.reader.ReadTorrent(ctx, provider, postKey, endpoint)
}
