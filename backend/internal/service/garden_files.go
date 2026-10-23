package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/p115"
)

type GardenResourceInput struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"providerId"`
	Magnet     string `json:"magnet"`
}

type GardenFiles struct {
	Hash        string         `json:"hash"`
	Files       []OrganizeFile `json:"files"`
	EndEpisodes map[string]int `json:"endEpisodes"`
	Reason      string         `json:"reason,omitempty"`
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
	out := GardenFiles{Hash: resource.Hash, Files: []OrganizeFile{}, EndEpisodes: map[string]int{}}
	if d.IsDeleted || d.Detail.HasMoreFiles {
		out.Reason = "Garden 没有提供完整文件清单，可使用普通离线"
		return out, nil
	}
	for _, file := range d.Detail.Files {
		if strings.EqualFold(path.Ext(file.Name), ".iso") || organizeMedia(file.Name) != "video" {
			continue
		}
		if len(out.Files) >= 1000 {
			return GardenFiles{}, fmt.Errorf("%w：视频超过 1000 个，请使用普通离线", ErrOrganizeInput)
		}
		f := OrganizeFile{ShareFile: p115.ShareFile{ID: file.Name, Name: path.Base(file.Name)}, SourcePath: "/" + file.Name, MediaType: "video", Season: 1}
		parsed, _ := animeparse.Title(strings.TrimSuffix(f.Name, path.Ext(f.Name)))
		special := gardenSpecialFile.MatchString(f.Name) || gardenSpecialFile.MatchString(path.Base(path.Dir(file.Name)))
		if parsed.Kind == "episode" && parsed.Episode != nil && *parsed.Episode > 0 && *parsed.Episode <= 10000 {
			f.Episode = *parsed.Episode
		}
		// A clearly labelled E027-E28 is one file, not two download entries.
		if !special {
			if m := gardenFileRange.FindStringSubmatch(f.Name); m != nil {
				start, _ := strconv.Atoi(m[1])
				end, _ := strconv.Atoi(m[2])
				if start > 0 && end > start && end <= 10000 {
					f.Episode = start
					out.EndEpisodes[f.ID] = end
				}
			}
		}
		if special {
			f.Episode = 0
			delete(out.EndEpisodes, f.ID)
		}
		out.Files = append(out.Files, f)
	}
	sort.Slice(out.Files, func(i, j int) bool { return gardenNaturalLess(out.Files[i].ID, out.Files[j].ID) })
	if len(out.Files) == 0 {
		out.Reason = "清单中没有可整理的视频文件，可使用普通离线（不支持 ISO）"
	}
	return out, nil
}

var gardenFileRange = regexp.MustCompile(`(?i)(?:\b|[._])E(\d{1,4})\s*[-+]\s*E?(\d{1,4})(?:\D|$)`)
var gardenSpecialFile = regexp.MustCompile(`(?i)(?:^|[\[ ._(\-])(?:NCOP|NCED|OP|ED|OVA|OAD|SP|PV|CM|SPECIALS?|BONUS|EXTRAS?|FILM|MOVIE)(?:[0-9]|$|[\] ._)\-])|特典|番外|特别篇|特別篇|劇場版|剧场版`)
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
