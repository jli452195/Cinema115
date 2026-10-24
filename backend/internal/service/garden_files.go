package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/p115"
)

type GardenResourceInput struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"providerId"`
	Magnet     string `json:"magnet"`
}

type GardenFiles struct {
	Hash      string             `json:"hash"`
	Files     []OrganizeFile     `json:"files"`
	Subtitles []OrganizeSubtitle `json:"subtitles"`
	Reason    string             `json:"reason,omitempty"`
}

// Files is lazy: search never fetches resource details. Verified lists share the
// site's bounded cache by normalized BTIH, including base32/hex aliases.
func (s *GardenService) Files(ctx context.Context, in GardenResourceInput) (GardenFiles, error) {
	requestPath, err := garden.DetailPath(in.Provider, in.ProviderID)
	if err != nil {
		return GardenFiles{}, ErrOrganizeInput
	}
	resource, err := p115.ParseCloudResource(in.Magnet)
	if err != nil || resource.Protocol != "magnet" {
		return GardenFiles{}, ErrOrganizeInput
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
			return GardenFiles{}, err
		}
		s.mu.Lock()
		// Alias the validated response; normal cache pruning bounds both keys.
		delete(s.cache, requestPath)
		s.cache[key] = read
		s.mu.Unlock()
	}
	var d garden.ResourceDetail
	if err := json.Unmarshal(read.body, &d); err != nil {
		return GardenFiles{}, err
	}
	actual, err := p115.ParseCloudResource(d.Resource.Magnet)
	if err != nil || actual.Protocol != "magnet" || actual.Hash != resource.Hash {
		return GardenFiles{}, garden.ErrInvalid
	}
	out := GardenFiles{Hash: resource.Hash, Files: []OrganizeFile{}, Subtitles: []OrganizeSubtitle{}}
	if d.IsDeleted || d.Detail.HasMoreFiles {
		out.Reason = "Garden 没有提供完整文件清单，可使用普通离线"
		return out, nil
	}
	media := make([]OrganizeFile, 0, len(d.Detail.Files))
	for _, file := range d.Detail.Files {
		kind := organizeMedia(file.Name)
		if strings.EqualFold(path.Ext(file.Name), ".iso") || (kind != "video" && kind != "subtitle") {
			continue
		}
		if len(media) >= 5000 || kind == "video" && len(out.Files) >= 1000 {
			return GardenFiles{}, fmt.Errorf("%w：视频超过 1000 个或媒体文件超过 5000 个，请使用普通离线", ErrOrganizeInput)
		}
		f := OrganizeFile{ShareFile: p115.ShareFile{ID: file.Name, Name: path.Base(file.Name)}, ParentID: path.Dir(file.Name), SourcePath: "/" + file.Name, MediaType: kind, Season: 1}
		if kind == "video" {
			organizeFileNumbering(&f)
			out.Files = append(out.Files, f)
		}
		media = append(media, f)
	}
	out.Subtitles = organizeSubtitles(media)
	sort.Slice(out.Subtitles, func(i, j int) bool { return gardenNaturalLess(out.Subtitles[i].File.ID, out.Subtitles[j].File.ID) })
	sort.Slice(out.Files, func(i, j int) bool { return gardenNaturalLess(out.Files[i].ID, out.Files[j].ID) })
	if len(out.Files) == 0 {
		out.Reason = "清单中没有可整理的视频文件，可使用普通离线（不支持 ISO）"
	}
	return out, nil
}

var gardenNumbers = regexp.MustCompile(`[0-9]+|[^0-9]+`)

func gardenNaturalLess(a, b string) bool {
	x, y := gardenNumbers.FindAllString(strings.ToLower(a), -1), gardenNumbers.FindAllString(strings.ToLower(b), -1)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] == y[i] {
			continue
		}
		xn, yn := strings.TrimLeft(x[i], "0"), strings.TrimLeft(y[i], "0")
		if x[i][0] >= '0' && x[i][0] <= '9' && y[i][0] >= '0' && y[i][0] <= '9' {
			if len(xn) != len(yn) {
				return len(xn) < len(yn)
			}
			if xn != yn {
				return xn < yn
			}
			continue
		}
		return x[i] < y[i]
	}
	if len(x) != len(y) {
		return len(x) < len(y)
	}
	return a < b
}
