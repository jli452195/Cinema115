package service

import (
	"context"
	"encoding/json"
	"time"

	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/utils/torrentmeta"
)

// Files is lazy: search never fetches resource details. Verified lists share the
// site's bounded cache by normalized BTIH, including base32/hex aliases.
func (s *GardenService) Files(ctx context.Context, in DownloadResourceInput) (DownloadFiles, error) {
	requestPath, err := garden.DetailPath(in.Provider, in.ProviderID)
	if err != nil {
		return DownloadFiles{}, ErrOrganizeInput
	}
	resource, err := p115.ParseCloudResource(in.Magnet)
	if err != nil || resource.Protocol != "magnet" {
		return DownloadFiles{}, ErrOrganizeInput
	}
	key := "files:" + resource.Hash
	s.mu.Lock()
	read, exists := s.cache[key]
	s.mu.Unlock()
	if !exists || time.Now().After(read.expires) {
		read, _, err = s.read(ctx, requestPath, 30*time.Minute, false, nil, func(body []byte) error {
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
		s.mu.Lock()
		// Alias the validated response; normal cache pruning bounds both keys.
		delete(s.cache, requestPath)
		s.cache[key] = read
		s.mu.Unlock()
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
	if d.IsDeleted || d.Detail.HasMoreFiles {
		out.Reason = "Garden 没有提供完整文件清单，可使用普通离线"
		return out, nil
	}
	files := make([]torrentmeta.File, 0, len(d.Detail.Files))
	for _, file := range d.Detail.Files {
		files = append(files, torrentmeta.File{Path: file.Name})
	}
	return organizeResourceFiles(resource.Hash, files)
}
