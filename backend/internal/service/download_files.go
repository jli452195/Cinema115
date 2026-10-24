package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/utils/torrentmeta"
)

type DownloadResourceInput struct {
	Title      string `json:"title,omitempty"` // Display metadata only; never a file identity.
	Source     string `json:"source"`
	Provider   string `json:"provider"`
	ProviderID string `json:"providerId"`
	Magnet     string `json:"magnet"`
	TorrentURL string `json:"torrentUrl"`
}

type DownloadFileSpec struct {
	model.AnimeSpec
	Fansub string `json:"fansub,omitempty"`
}

type DownloadFiles struct {
	ManifestID string                      `json:"manifestId,omitempty"`
	Specs      map[string]DownloadFileSpec `json:"specs,omitempty"`
	Hash       string                      `json:"hash"`
	Files      []OrganizeFile              `json:"files"`
	Subtitles  []OrganizeSubtitle          `json:"subtitles"`
	Reason     string                      `json:"reason,omitempty"`
}

type downloadResourceReader interface {
	Files(context.Context, DownloadResourceInput) (DownloadFiles, error)
}

// DownloadResources isolates site reads from the download/organize worker.
// Sites retain separate sessions/caches/gates; only file normalization is shared.
type DownloadResources struct {
	Garden *GardenService
	Gying  interface {
		ReadTorrent(context.Context, string, string, string) ([]byte, error)
	}
	mikan *animeSiteCache
}

func NewDownloadResources(g *GardenService, m *MikanService, reader animeresource.TorrentReader) *DownloadResources {
	return &DownloadResources{Garden: g, mikan: newAnimeSiteCache(mikanTorrentTransport{site: m, reader: reader}, "mikan")}
}

func (s *DownloadResources) Files(ctx context.Context, in DownloadResourceInput) (DownloadFiles, error) {
	if in.Source == "garden" && s.Garden != nil && in.TorrentURL == "" {
		return s.Garden.Files(ctx, in)
	}
	if in.Source == "gying" {
		hash, err := animeresource.MagnetHash(in.Magnet)
		if err != nil || in.TorrentURL != "" || s.Gying == nil {
			return DownloadFiles{}, ErrOrganizeInput
		}
		body, err := s.Gying.ReadTorrent(ctx, in.Provider, in.ProviderID, hash)
		if err != nil {
			return DownloadFiles{}, err
		}
		return downloadTorrentFiles(body, hash)
	}
	if in.Source != "mikan" || in.Provider != "" {
		return DownloadFiles{}, ErrOrganizeInput
	}
	endpoint, err := animeresource.TorrentURL("mikan", in.ProviderID, in.TorrentURL)
	if err != nil {
		return DownloadFiles{}, fmt.Errorf("%w：蜜柑种子地址无效", ErrOrganizeInput)
	}
	hash, err := animeresource.MagnetHash(in.Magnet)
	if err != nil {
		return DownloadFiles{}, fmt.Errorf("%w：磁力身份无效", ErrOrganizeInput)
	}
	read, _, err := s.mikan.read(ctx, endpoint, 30*time.Minute, false, nil, func(body []byte) error {
		_, err := torrentmeta.Parse(body)
		return err
	})
	if err != nil {
		if errors.Is(err, torrentmeta.ErrInvalid) || errors.Is(err, torrentmeta.ErrUnsupported) {
			return DownloadFiles{}, fmt.Errorf("%w：种子没有可用的 BT v1 清单，可使用普通离线", ErrOrganizeInput)
		}
		return DownloadFiles{}, fmt.Errorf("读取蜜柑种子清单: %w", err)
	}
	return downloadTorrentFiles(read.body, hash)
}

func parseDownloadTorrent(body []byte, hash string) (torrentmeta.Metadata, error) {
	meta, err := torrentmeta.Parse(body)
	if err != nil {
		return meta, fmt.Errorf("%w：种子没有可用的 BT v1 清单，可使用普通离线", ErrOrganizeInput)
	}
	// Recheck each caller, including cache hits and coalesced requests. A filename
	// or claimed magnet never replaces the identity of the actual info bytes.
	if meta.InfoHash != hash {
		return meta, fmt.Errorf("%w：种子与磁力身份不一致，请核对原帖", ErrOrganizeInput)
	}
	return meta, nil
}

func downloadTorrentFiles(body []byte, hash string) (DownloadFiles, error) {
	meta, err := parseDownloadTorrent(body, hash)
	if err != nil {
		return DownloadFiles{}, err
	}
	if meta.MultiFile {
		for i := range meta.Files {
			meta.Files[i].Path = path.Join(meta.Name, meta.Files[i].Path)
		}
	}
	return organizeResourceFiles(meta.InfoHash, meta.Files)
}

type mikanTorrentTransport struct {
	site   *MikanService
	reader animeresource.TorrentReader
}

func (t mikanTorrentTransport) Fetch(ctx context.Context, endpoint string, _ int64) (body []byte, err error) {
	if t.site == nil || t.reader == nil {
		return nil, fmt.Errorf("%w：蜜柑种子读取尚未配置", ErrOrganizeInput)
	}
	postKey := strings.TrimSuffix(path.Base(endpoint), ".torrent")
	err = t.site.withMikanAccess(ctx, func() error {
		body, err = t.reader.ReadTorrent(ctx, "mikan", postKey, endpoint)
		return err
	})
	return body, err
}

func organizeResourceFiles(hash string, files []torrentmeta.File) (DownloadFiles, error) {
	out := DownloadFiles{Hash: hash, Files: []OrganizeFile{}, Subtitles: []OrganizeSubtitle{}, Specs: make(map[string]DownloadFileSpec)}
	media := make([]OrganizeFile, 0, len(files))
	for _, file := range files {
		kind := organizeMedia(file.Path)
		if strings.EqualFold(path.Ext(file.Path), ".iso") || kind != "video" && kind != "subtitle" {
			continue
		}
		if len(media) >= 5000 || kind == "video" && len(out.Files) >= 1000 {
			return DownloadFiles{}, fmt.Errorf("%w：视频超过 1000 个或媒体文件超过 5000 个，请使用普通离线", ErrOrganizeInput)
		}
		f := OrganizeFile{ShareFile: p115.ShareFile{ID: file.Path, Name: path.Base(file.Path), Size: file.Size}, ParentID: path.Dir(file.Path), SourcePath: "/" + file.Path, MediaType: kind, Season: 1}
		if kind == "video" {
			organizeFileNumbering(&f)
			out.Specs[f.ID] = DownloadFileSpec{AnimeSpec: animeparse.Specification(f.Name), Fansub: downloadFansub(f.Name)}
			out.Files = append(out.Files, f)
		}
		media = append(media, f)
	}
	out.Subtitles = organizeSubtitles(media)
	sort.Slice(out.Subtitles, func(i, j int) bool { return resourceNaturalLess(out.Subtitles[i].File.ID, out.Subtitles[j].File.ID) })
	sort.Slice(out.Files, func(i, j int) bool { return resourceNaturalLess(out.Files[i].ID, out.Files[j].ID) })
	if len(out.Files) == 0 {
		out.Reason = "清单中没有可整理的视频文件，可使用普通离线（不支持 ISO）"
	}
	return out, nil
}

func downloadFansub(title string) string {
	_, tags := resourceTitleTags(title)
	for _, tag := range tags {
		if strings.Contains(tag, "字幕组") || strings.Contains(tag, "字幕組") || strings.Contains(tag, "字幕社") {
			return tag
		}
	}
	return ""
}

var resourceNumbers = regexp.MustCompile(`[0-9]+|[^0-9]+`)

func resourceNaturalLess(a, b string) bool {
	x, y := resourceNumbers.FindAllString(strings.ToLower(a), -1), resourceNumbers.FindAllString(strings.ToLower(b), -1)
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
