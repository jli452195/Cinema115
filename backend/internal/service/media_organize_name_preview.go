package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
)

type MediaNamePreview struct {
	Source         string                  `json:"source"`
	Title          string                  `json:"title,omitempty"`
	Year           string                  `json:"year,omitempty"`
	Season         *int                    `json:"season,omitempty"`
	Episode        *int                    `json:"episode,omitempty"`
	EndEpisode     *int                    `json:"endEpisode,omitempty"`
	Specifications map[naming.Field]string `json:"specifications,omitempty"`
	Work           *mediaNamePreviewWork   `json:"work,omitempty"`
	RelativePath   string                  `json:"relativePath,omitempty"`
	Message        string                  `json:"message,omitempty"`
}

type mediaNamePreviewWork struct {
	ID        int    `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Date      string `json:"date,omitempty"`
	PosterURL string `json:"posterUrl,omitempty"`
	Overview  string `json:"overview,omitempty"`
}

// PreviewName 使用正式整理的解析、TMDB 匹配与当前命名模板，只计算相对路径。
func (s *MediaOrganizer) PreviewName(ctx context.Context, raw string) (MediaNamePreview, error) {
	out := MediaNamePreview{Source: strings.TrimSpace(raw)}
	source, valid := mediaNamePreviewSource(out.Source)
	if !valid {
		return out, fmt.Errorf("%w：请输入一个作品名、文件名或有效的相对路径", ErrMediaOrganizeInput)
	}
	name := path.Base(source)
	mediaType := mediaFileType(name)
	titleOnly := mediaType == "" && !mediaKeepFile(name)
	if !titleOnly && mediaType != "video" {
		out.Message = "请检查视频或 STRM 的名称，字幕与其他附件由对应视频确定名称"
		return out, nil
	}

	rules := namingRecognition(ctx)
	ctx, cancel := context.WithTimeout(pinNamingRecognition(ctx, rules), 20*time.Second)
	defer cancel()
	specifications, factsErr := mediafacts.ValuesWithRecognition(name, releaseparse.Spec{}, rules)
	out.Specifications = specifications
	file := OrganizeFile{
		ShareFile: p115.ShareFile{ID: "preview", Name: name},
		ParentID:  path.Dir(source), SourcePath: source, MediaType: "video", Season: 1,
	}
	if titleOnly {
		// 解析裸标题时保留末尾的点号分隔标签，渲染仍然使用原名。
		file.Name += ".mediafacts"
	}
	mediaOrganizeFileNumbering(&file)
	var run MediaOrganizeRun
	mediaAppendScanEntries(&run, []MediaOrganizeEntry{{File: file}})
	identity := run.Groups[0].Identity
	file = run.Entries[0].File
	out.Title, out.Year = identity.Name, identity.Year
	if identity.SeasonExplicit {
		out.Season = mediaOptionalNumber(&identity.Season)
	}
	if mediaOrganizeHasNumbering(file) {
		out.Season, out.Episode = mediaOptionalNumber(&file.Season), &file.Episode
		if file.EndEpisode > 0 {
			out.EndEpisode = &file.EndEpisode
		}
	} else if identity.Kind == "tv" && organizeSpecialFile.MatchString(file.Name) {
		out.Message = "特殊内容缺少季集，请补充季集后查询 TMDB"
		return out, nil
	}

	match, decision, err := recognizeMediaWithDecision(ctx, s.metadata, identity)
	if match == nil {
		out.Message = decision.Message
		switch {
		case errors.Is(err, tmdb.ErrNotConfigured):
			out.Message = "后端尚未配置 TMDB"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			out.Message = "TMDB 查询超时，请重试"
		}
		return out, nil
	}
	detail := match.Detail
	out.Work = &mediaNamePreviewWork{
		ID: detail.ID, Kind: detail.Kind, Title: detail.Title, Date: detail.Date,
		PosterURL: detail.PosterURL, Overview: detail.Overview,
	}
	if detail.Kind == "tv" {
		file.Season = match.Season
		if code, message := mediaOrganizeNumberingError(file); code != "" {
			reason, _, _ := strings.Cut(message, "，")
			out.Message = reason + "，请在输入中补充或修正季集"
			return out, nil
		}
		out.Season = &file.Season
	}
	template, err := (MediaNaming{Templates: s.templates, Metadata: s.metadata}).prepareWithDetail(ctx, detail.Kind, detail.ID, nil, detail)
	if err != nil {
		out.Message = "命名模板预览暂未完成，请检查模板配置或稍后重试"
		return out, nil
	}
	if factsErr != nil {
		out.Message = factsErr.Error()
		return out, nil
	}
	result, err := template.render(MediaNamingFile{
		OrganizeSelection: OrganizeSelection{ID: file.ID, Season: file.Season, Episode: file.Episode},
		Name:              name,
		TitleOnly:         titleOnly,
		NamingValues:      specifications,
	})
	if err != nil {
		out.Message = err.Error()
		return out, nil
	}
	out.RelativePath = result.RelativePath
	return out, nil
}

func mediaNamePreviewSource(raw string) (string, bool) {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	source := strings.ReplaceAll(raw, "\\", "/")
	if source == "" || len(source) > 4096 || !utf8.ValidString(source) || strings.IndexFunc(source, unicode.IsControl) >= 0 ||
		strings.HasPrefix(source, "/") || strings.HasSuffix(source, "/") || strings.Contains(source, "://") ||
		len(source) >= 2 && source[1] == ':' {
		return "", false
	}
	for _, part := range strings.Split(source, "/") {
		if part == ".." {
			return "", false
		}
	}
	source = path.Clean(source)
	return source, source != "."
}
