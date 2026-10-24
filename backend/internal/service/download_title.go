package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/naming"
)

var errDownloadTitleReview = errors.New("单集内容需要确认")
var downloadTitleQuality = regexp.MustCompile(`(?i)[0-9]{3,4}[pi]|[48]k`)

// Clear single episodes need a naming intent, not a remote file manifest.
// The title remains display/numbering metadata, never evidence of a file path.
func (s *DownloadOrganize) resourceFiles(ctx context.Context, in DownloadResourceInput) (DownloadFiles, error) {
	if err := ctx.Err(); err != nil {
		return DownloadFiles{}, err
	}
	if in.Source == "gying" {
		return s.Resources.Files(ctx, in)
	}
	parsed, err := animeparse.Title(in.Title)
	mixedQuality := parsed.Resolution != nil && *parsed.Resolution == "unknown" && len(downloadTitleQuality.FindAllString(in.Title, 2)) > 1
	if err != nil || parsed.Kind != "episode" || parsed.Episode == nil || *parsed.Episode < 1 || *parsed.Episode > 10000 || parsed.SpecAmbiguous || mixedQuality || organizeSpecialFile.MatchString(in.Title) {
		return s.Resources.Files(ctx, in)
	}
	// Keep source and magnet validation even when no upstream read is needed.
	switch in.Source {
	case "mikan":
		_, err = animeresource.TorrentURL("mikan", in.ProviderID, in.TorrentURL)
		if in.Provider != "" {
			err = ErrOrganizeInput
		}
	case "garden":
		_, err = garden.DetailPath(in.Provider, in.ProviderID)
		if in.TorrentURL != "" {
			err = ErrOrganizeInput
		}
	default:
		err = ErrOrganizeInput
	}
	hash, hashErr := animeresource.MagnetHash(in.Magnet)
	if err != nil || hashErr != nil {
		return DownloadFiles{}, fmt.Errorf("%w：资源来源或磁力无效", ErrOrganizeInput)
	}
	f := OrganizeFile{ShareFile: p115.ShareFile{ID: "title", Name: in.Title}, TitleOnly: true, ParentID: ".", SourcePath: "/title", MediaType: "video", Season: 1, Episode: *parsed.Episode}
	if m := organizeFileSeason.FindStringSubmatch(in.Title); m != nil {
		f.Season, _ = strconv.Atoi(m[1])
	}
	return DownloadFiles{Hash: hash, Files: []OrganizeFile{f}, Subtitles: []OrganizeSubtitle{}, Specs: map[string]DownloadFileSpec{f.ID: {AnimeSpec: parsed.Spec, Fansub: downloadFansub(in.Title)}}}, nil
}

func downloadTargetKey(item OrganizeItem) string {
	name := strings.ToLower(item.TargetPath)
	if item.File.MediaType == "video" && !item.File.TitleOnly {
		name = strings.TrimSuffix(name, strings.ToLower(path.Ext(item.Name)))
	}
	return name
}

// Bind only the one actual video in this task's already-read directory. No
// torrent read, global search, guessed extension or SHA1 request is required.
func bindTitleDownload(job *OrganizeJob, files []OrganizeFile) error {
	intent := job.Items[0]
	var video OrganizeFile
	count := 0
	for _, f := range files {
		if strings.EqualFold(path.Ext(f.Name), ".iso") {
			return fmt.Errorf("%w：资源包含 ISO，请核对下载目录", errDownloadTitleReview)
		}
		if f.MediaType == "video" {
			video, count = f, count+1
		}
	}
	if count != 1 {
		if count > 1 {
			return fmt.Errorf("%w：下载目录有 %d 个视频，请核对后手动整理", errDownloadTitleReview, count)
		}
		return fmt.Errorf("按标题识别为单集，但下载目录有 %d 个视频，请核对后手动整理", count)
	}
	video.Season = 1
	organizeFileNumbering(&video)
	parsed, err := animeparse.Title(strings.TrimSuffix(video.Name, path.Ext(video.Name)))
	unmapped := video.Episode == 0 && (parsed.Kind != "unknown" || parsed.RawEpisode != "")
	wrongEpisode := video.Episode > 0 && video.Episode != intent.File.Episode
	if err != nil || organizeSpecialFile.MatchString(video.Name) || organizeSpecialFile.MatchString(path.Base(path.Dir(video.SourcePath))) || video.EndEpisode != 0 || unmapped || wrongEpisode {
		return fmt.Errorf("%w：实际视频编号与标题不一致，请核对后手动整理：%s", errDownloadTitleReview, video.Name)
	}
	if organizeFileSeason.MatchString(intent.File.Name) && organizeFileSeason.MatchString(video.Name) && video.Season != intent.File.Season {
		return fmt.Errorf("%w：实际视频季号与标题不一致，请核对后手动整理：%s", errDownloadTitleReview, video.Name)
	}
	intent.Name += strings.ToLower(path.Ext(video.Name))
	intent.TargetPath = path.Join(job.Config.TargetPath, intent.Directory, intent.Name)
	video.SourcePath = path.Join(job.Config.SourcePath, video.SourcePath)
	intent.File = video
	items := []OrganizeItem{intent}
	for _, sub := range organizeSubtitles(files) {
		if sub.VideoID != video.ID {
			continue
		}
		result, err := naming.Companion(naming.Result{Directory: intent.Directory, Stem: intent.Stem}, sub.Tail, sub.File.Name)
		if err != nil {
			return fmt.Errorf("%w：%w", errDownloadTitleReview, err)
		}
		sub.File.SourcePath = path.Join(job.Config.SourcePath, sub.File.SourcePath)
		items = append(items, OrganizeItem{File: sub.File, Directory: result.Directory, Stem: result.Stem, Name: result.Name, TargetPath: path.Join(job.Config.TargetPath, result.RelativePath), Status: "pending"})
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		key := strings.ToLower(item.TargetPath)
		if seen[key] || len(item.Name) > 240 {
			return fmt.Errorf("%w：实际文件整理后重名或名称过长，请核对后手动整理", errDownloadTitleReview)
		}
		seen[key] = true
	}
	job.Items = items
	return nil
}
