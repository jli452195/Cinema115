package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/recognition"
	"strm115/backend/internal/releaseparse"
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

type namingSeasonSource interface {
	Season(context.Context, int, int) (*tmdb.SeasonDetail, error)
}

type preparedMediaNaming struct {
	recognition  recognition.Snapshot
	snapshot     naming.Snapshot
	detail       *tmdb.Detail
	renderer     *naming.Renderer
	english      string
	seasonSource namingSeasonSource
	numbering    *numberingSelection
	episodeNames map[int]map[int]string
}

func (s MediaNaming) prepare(ctx context.Context, kind string, id int, expected *int64) (preparedMediaNaming, error) {
	return s.prepareWithDetail(ctx, kind, id, expected, nil)
}

// 已完成识别的预览复用作品详情，避免再查一次同一作品。
func (s MediaNaming) prepareWithDetail(ctx context.Context, kind string, id int, expected *int64, detail *tmdb.Detail) (preparedMediaNaming, error) {
	rules := namingRecognition(ctx)
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
	if detail == nil {
		if kind == "tv" {
			ctx, err = pinNumbering(ctx, s.Metadata, id)
			if err != nil {
				return preparedMediaNaming{}, err
			}
		}
		detail, err = s.Metadata.Details(ctx, kind, id)
		if err != nil {
			return preparedMediaNaming{}, fmt.Errorf("读取 TMDB 作品: %w", err)
		}
	}
	if detail == nil || detail.ID != id || detail.Kind != kind || detail.Title == "" || snapshot.Template.Kind != naming.Kind(kind) {
		return preparedMediaNaming{}, fmt.Errorf("命名模板或作品信息无效")
	}
	p := preparedMediaNaming{snapshot: snapshot, detail: detail, renderer: renderer, recognition: rules}
	if kind == "tv" && namingTemplateUsesField(snapshot.Template, naming.EpisodeTitle) {
		p.seasonSource, _ = s.Metadata.(namingSeasonSource)
		if p.seasonSource != nil {
			p.numbering, _ = ctx.Value(numberingKey{id}).(*numberingSelection)
			p.episodeNames = make(map[int]map[int]string)
		}
	}
	p.english, err = optionalNamingEnglish(ctx, s.Metadata, snapshot.Template, kind, id)
	if err != nil {
		return preparedMediaNaming{}, err
	}
	return p, nil
}

func namingTemplateUsesField(template naming.Template, field naming.Field) bool {
	layers := append([][]naming.Block{template.Filename}, template.Directories...)
	for _, layer := range layers {
		for _, block := range layer {
			if block.Field == field {
				return true
			}
		}
	}
	return false
}

func optionalNamingEnglish(ctx context.Context, metadata any, template naming.Template, kind string, id int) (string, error) {
	source, ok := metadata.(interface {
		EnglishTitle(context.Context, string, int) (string, error)
	})
	if !ok || !namingTemplateUsesField(template, naming.EnglishTitle) {
		return "", nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	english, err := source.EnglishTitle(lookupCtx, kind, id)
	cancel()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", nil
	}
	return english, nil
}

// 同一作品的命名计划按季复用一次可选查询，缺失的集名与查询失败均可省略。
func (p preparedMediaNaming) episodeTitle(ctx context.Context, season, episode int) string {
	if p.seasonSource == nil {
		return ""
	}
	if names, exists := p.episodeNames[season]; exists {
		return names[episode]
	}
	p.episodeNames[season] = nil
	if p.numbering != nil {
		ctx = context.WithValue(ctx, numberingKey{p.detail.ID}, p.numbering)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	detail, err := p.seasonSource.Season(lookupCtx, p.detail.ID, season)
	if err != nil || detail == nil || detail.Number != season || p.detail.Numbering != nil && detail.NumberingRevision != p.detail.Numbering.Revision {
		return ""
	}
	names := make(map[int]string, len(detail.Episodes))
	for _, item := range detail.Episodes {
		names[item.Number] = item.Name
	}
	p.episodeNames[season] = names
	return names[episode]
}

func (p preparedMediaNaming) render(ctx context.Context, file MediaNamingFile) (naming.Result, error) {
	if p.snapshot.Template.Kind == naming.TV && (file.Season < 0 || file.Season > 1000 || file.Episode < 0 || file.Episode > 10000) {
		return naming.Result{}, fmt.Errorf("%w：请填写有效目标季集", ErrOrganizeInput)
	}
	values := make(map[naming.Field]string, len(file.NamingValues)+5)
	if file.NamingValues == nil {
		var err error
		values, err = mediafacts.ValuesWithRecognition(file.Name, releaseparse.Spec{}, p.recognition)
		if err != nil {
			return naming.Result{}, err
		}
	}
	for field, value := range file.NamingValues {
		// 旧整理记录可能仍然保存已退役的字段。
		if field != "container" && field != "edition" {
			values[field] = value
		}
	}
	if file.NamingValues != nil {
		mediafacts.NormalizeVideoSubtitleValues(values)
	}
	values[naming.Title], values[naming.OriginalTitle] = p.detail.Title, p.detail.OriginalTitle
	values[naming.EnglishTitle] = p.english
	values[naming.TMDBID] = strconv.Itoa(p.detail.ID)
	values[naming.Year] = ""
	if len(p.detail.Date) >= 4 {
		values[naming.Year] = p.detail.Date[:4]
	}
	// 集名和作品字段一样由实际目标元数据决定。
	delete(values, naming.EpisodeTitle)
	if p.seasonSource != nil {
		values[naming.EpisodeTitle] = p.episodeTitle(ctx, file.Season, file.Episode)
		if err := ctx.Err(); err != nil {
			return naming.Result{}, err
		}
	}
	data := naming.Data{Values: values, OriginalName: file.Name, TitleOnly: file.TitleOnly}
	if p.snapshot.Template.Kind == naming.TV {
		// 连集保留原范围用于编号，文件名仍沿用起始集。
		data.Season, data.Episode = &file.Season, &file.Episode
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
		result, err := p.render(ctx, file)
		if ctx.Err() != nil {
			return MediaNamingPreview{}, ctx.Err()
		}
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
