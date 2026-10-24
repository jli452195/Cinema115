package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/naming"
)

type NamingTemplates interface {
	NamingTemplate(context.Context, naming.Kind) (naming.Snapshot, error)
}

// MediaNaming joins caller file facts with saved templates and cached metadata.
// It never reads a drive, parses a torrent or executes a file operation.
type MediaNaming struct {
	Templates NamingTemplates
	Metadata  OrganizeMetadata
}

type MediaNamingFile struct {
	OrganizeSelection
	Name         string                  `json:"name"`
	TitleOnly    bool                    `json:"titleOnly,omitempty"`
	NamingValues map[naming.Field]string `json:"namingValues,omitempty"`
}

type MediaNamingInput struct {
	Kind      string             `json:"kind"`
	TMDBID    int                `json:"tmdbId"`
	Files     []MediaNamingFile  `json:"files"`
	Subtitles []OrganizeSubtitle `json:"subtitles,omitempty"`
}

type MediaNamingItem struct {
	ID     string         `json:"id"`
	Result *naming.Result `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type MediaNamingPreview struct {
	Naming naming.Snapshot   `json:"naming"`
	Items  []MediaNamingItem `json:"items"`
}

type preparedMediaNaming struct {
	snapshot naming.Snapshot
	detail   *tmdb.Detail
	renderer *naming.Renderer
}

func (s MediaNaming) prepare(ctx context.Context, kind string, id int, expected *int64) (preparedMediaNaming, error) {
	if (kind != "movie" && kind != "tv") || id < 1 {
		return preparedMediaNaming{}, ErrOrganizeInput
	}
	if s.Templates == nil || s.Metadata == nil {
		return preparedMediaNaming{}, fmt.Errorf("公共命名服务未配置")
	}
	snapshot, err := s.Templates.NamingTemplate(ctx, naming.Kind(kind))
	if err != nil {
		return preparedMediaNaming{}, fmt.Errorf("读取公共命名模板: %w", err)
	}
	if expected != nil && *expected != snapshot.Revision {
		return preparedMediaNaming{}, fmt.Errorf("%w：命名模板已更新，请刷新命名预览并重新确认", ErrOrganizeConflict)
	}
	snapshot.Template = snapshot.Template.Clone()
	renderer, err := naming.Compile(snapshot.Template)
	if err != nil {
		return preparedMediaNaming{}, fmt.Errorf("编译公共命名模板: %w", err)
	}
	if kind == "tv" {
		ctx, err = pinNumbering(ctx, s.Metadata, id)
		if err != nil {
			return preparedMediaNaming{}, err
		}
	}
	detail, err := s.Metadata.Details(ctx, kind, id)
	if err != nil {
		return preparedMediaNaming{}, fmt.Errorf("读取 TMDB 作品: %w", err)
	}
	if detail == nil || detail.ID != id || detail.Kind != kind || detail.Title == "" || snapshot.Template.Kind != naming.Kind(kind) {
		return preparedMediaNaming{}, fmt.Errorf("命名模板或作品信息无效")
	}
	return preparedMediaNaming{snapshot: snapshot, detail: detail, renderer: renderer}, nil
}

func (p preparedMediaNaming) render(file MediaNamingFile) (naming.Result, error) {
	if p.snapshot.Template.Kind == naming.TV && (file.Season < 0 || file.Season > 1000 || file.Episode < 1 || file.Episode > 10000 || file.EndEpisode != 0 && (file.EndEpisode < file.Episode || file.EndEpisode > 10000)) {
		return naming.Result{}, fmt.Errorf("%w：请填写有效目标季集", ErrOrganizeInput)
	}
	values := make(map[naming.Field]string, len(file.NamingValues)+5)
	if file.NamingValues == nil {
		values = mediaFileValues(file.Name, DownloadFileSpec{})
	}
	for field, value := range file.NamingValues {
		values[field] = value
	}
	values[naming.Title], values[naming.OriginalTitle] = p.detail.Title, p.detail.OriginalTitle
	values[naming.TMDBID] = strconv.Itoa(p.detail.ID)
	values[naming.Year] = ""
	if len(p.detail.Date) >= 4 {
		values[naming.Year] = p.detail.Date[:4]
	}
	values[naming.Custom] = strings.TrimSpace(file.Suffix)
	data := naming.Data{Values: values, OriginalName: file.Name, TitleOnly: file.TitleOnly}
	if p.snapshot.Template.Kind == naming.TV {
		data.Season, data.Episode = &file.Season, &file.Episode
		if file.EndEpisode != 0 {
			data.EndEpisode = &file.EndEpisode
		}
	}
	return p.renderer.Render(data)
}

func (s MediaNaming) Preview(ctx context.Context, input MediaNamingInput) (MediaNamingPreview, error) {
	if len(input.Files) == 0 || len(input.Files) > 1000 || len(input.Subtitles) > 4000 {
		return MediaNamingPreview{}, ErrOrganizeInput
	}
	p, err := s.prepare(ctx, input.Kind, input.TMDBID, nil)
	if err != nil {
		return MediaNamingPreview{}, err
	}
	out := MediaNamingPreview{Naming: p.snapshot, Items: make([]MediaNamingItem, 0, len(input.Files)+len(input.Subtitles))}
	videos := make(map[string]naming.Result, len(input.Files))
	seen := make(map[string]bool, len(input.Files)+len(input.Subtitles))
	for _, file := range input.Files {
		if err := ctx.Err(); err != nil {
			return MediaNamingPreview{}, err
		}
		if file.ID == "" || seen[file.ID] {
			return MediaNamingPreview{}, ErrOrganizeInput
		}
		seen[file.ID] = true
		result, err := p.render(file)
		item := MediaNamingItem{ID: file.ID}
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Result = &result
			videos[file.ID] = result
		}
		out.Items = append(out.Items, item)
	}
	for _, sub := range input.Subtitles {
		video, ok := videos[sub.VideoID]
		if !ok {
			continue
		}
		if sub.File.ID == "" || seen[sub.File.ID] {
			return MediaNamingPreview{}, ErrOrganizeInput
		}
		seen[sub.File.ID] = true
		result, err := naming.Companion(video, sub.Tail, sub.File.Name)
		item := MediaNamingItem{ID: sub.File.ID}
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Result = &result
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
