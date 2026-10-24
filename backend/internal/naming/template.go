// Package naming renders media paths from caller-supplied facts. It performs no
// parsing, metadata lookup, cloud access, or episode-number mapping.
package naming

import (
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

type Kind string

const (
	Movie Kind = "movie"
	TV    Kind = "tv"
)

type Field string

const (
	Title         Field = "title"
	OriginalTitle Field = "originalTitle"
	EnglishTitle  Field = "englishTitle"
	OriginalName  Field = "originalName"
	Year          Field = "year"
	TMDBID        Field = "tmdbId"
	Season        Field = "season"
	Episode       Field = "episode"
	SeasonEpisode Field = "seasonEpisode"
	EpisodeTitle  Field = "episodeTitle"
	Resolution    Field = "resolution"
	Source        Field = "source"
	Platform      Field = "platform"
	VideoCodec    Field = "videoCodec"
	BitDepth      Field = "bitDepth"
	DynamicRange  Field = "dynamicRange"
	FrameRate     Field = "frameRate"
	AudioCodec    Field = "audioCodec"
	Channels      Field = "channels"
	AudioEffect   Field = "audioEffect"
	Languages     Field = "languages"
	SubtitleMode  Field = "subtitleMode"
	ReleaseGroup  Field = "releaseGroup"
	Edition       Field = "edition"
	Custom        Field = "custom"
)

// FieldDefinition is also the field library for the visual editor. A supported
// field does not imply that a parser or upstream source can supply its value.
type FieldDefinition struct {
	ID      Field  `json:"id"`
	Label   string `json:"label"`
	Group   string `json:"group"`
	TVOnly  bool   `json:"tvOnly,omitempty"`
	Numeric bool   `json:"numeric,omitempty"`
}

var fields = [...]FieldDefinition{
	{Title, "作品名称", "work", false, false},
	{OriginalTitle, "原语种名称", "work", false, false},
	{EnglishTitle, "英文名称", "work", false, false},
	{OriginalName, "原文件名", "work", false, false},
	{Year, "年份", "work", false, false},
	{TMDBID, "TMDB ID", "work", false, false},
	{Season, "季号", "episode", true, true},
	{Episode, "集号", "episode", true, true},
	{SeasonEpisode, "季集编号", "episode", true, false},
	{EpisodeTitle, "集名", "episode", true, false},
	{Resolution, "分辨率", "video", false, false},
	{Source, "资源来源", "video", false, false},
	{Platform, "发布平台", "video", false, false},
	{VideoCodec, "视频编码", "video", false, false},
	{BitDepth, "位深", "video", false, false},
	{DynamicRange, "动态范围", "video", false, false},
	{FrameRate, "帧率", "video", false, false},
	{AudioCodec, "音频编码", "audio", false, false},
	{Channels, "声道", "audio", false, false},
	{AudioEffect, "音频制式", "audio", false, false},
	{Languages, "字幕语言", "release", false, false},
	{SubtitleMode, "字幕形式", "release", false, false},
	{ReleaseGroup, "字幕组或发布组", "release", false, false},
	{Edition, "版本", "release", false, false},
	{Custom, "自定义标记", "release", false, false},
}

// Block's decorations belong to its value: an empty value omits the whole block.
// Separator is emitted only when another nonempty block precedes this one.
type Block struct {
	Field     Field  `json:"field"`
	Separator string `json:"separator,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	Suffix    string `json:"suffix,omitempty"`
	Pad       int    `json:"pad,omitempty"`
}

type Template struct {
	Kind        Kind      `json:"kind"`
	Directories [][]Block `json:"directories"`
	Filename    []Block   `json:"filename"`
}

// Snapshot is the saved rule pinned by a plan; it contains no storage target.
type Snapshot struct {
	Revision int64    `json:"revision"`
	Template Template `json:"template"`
}

func (t Template) Clone() Template {
	out := Template{Kind: t.Kind, Filename: slices.Clone(t.Filename), Directories: make([][]Block, len(t.Directories))}
	for i, layer := range t.Directories {
		out.Directories[i] = slices.Clone(layer)
	}
	return out
}

var (
	ErrTemplate = errors.New("无效命名模板")
	ErrInput    = errors.New("无效命名数据")
)

const (
	maxDirectories    = 4
	maxBlocks         = 32
	maxDecoration     = 64
	maxValueBytes     = 4096
	maxComponentBytes = 240
)

// Renderer owns a copy of the validated template. Compile once per task/batch;
// Render may then be called concurrently without retaining input data.
type Renderer struct{ template Template }

func Catalog(kind Kind) ([]FieldDefinition, error) {
	if kind != Movie && kind != TV {
		return nil, fmt.Errorf("%w：未知作品类型", ErrTemplate)
	}
	out := make([]FieldDefinition, 0, len(fields))
	for _, field := range fields {
		if kind == TV || !field.TVOnly {
			out = append(out, field)
		}
	}
	return out, nil
}

func definition(id Field) (FieldDefinition, bool) {
	for _, field := range fields {
		if field.ID == id {
			return field, true
		}
	}
	return FieldDefinition{}, false
}

func Compile(template Template) (*Renderer, error) {
	if template.Kind != Movie && template.Kind != TV {
		return nil, fmt.Errorf("%w：未知作品类型", ErrTemplate)
	}
	if len(template.Directories) > maxDirectories {
		return nil, fmt.Errorf("%w：最多支持 %d 层目录", ErrTemplate, maxDirectories)
	}
	for i, layer := range template.Directories {
		if err := validateLayer(template.Kind, layer); err != nil {
			return nil, fmt.Errorf("目录层 %d: %w", i+1, err)
		}
	}
	if err := validateLayer(template.Kind, template.Filename); err != nil {
		return nil, fmt.Errorf("文件名: %w", err)
	}
	hasEpisode := false
	for _, block := range template.Filename {
		hasEpisode = hasEpisode || block.Field == SeasonEpisode
	}
	if template.Kind == TV && !hasEpisode {
		return nil, fmt.Errorf("%w：剧集文件名须包含季集编号", ErrTemplate)
	}
	return &Renderer{template: template.Clone()}, nil
}

func validateLayer(kind Kind, layer []Block) error {
	if len(layer) == 0 || len(layer) > maxBlocks {
		return fmt.Errorf("%w：每层须有 1 至 %d 个字段", ErrTemplate, maxBlocks)
	}
	seen := make(map[Field]bool, len(layer))
	for _, block := range layer {
		field, ok := definition(block.Field)
		if !ok || kind == Movie && field.TVOnly {
			return fmt.Errorf("%w：不支持字段 %q", ErrTemplate, block.Field)
		}
		if seen[block.Field] {
			return fmt.Errorf("%w：同一层不能重复添加字段 %q", ErrTemplate, block.Field)
		}
		seen[block.Field] = true
		if block.Pad < 0 || block.Pad > 6 || block.Pad != 0 && !field.Numeric {
			return fmt.Errorf("%w：仅季号、集号支持 0 至 6 位补零", ErrTemplate)
		}
		for _, decoration := range []string{block.Separator, block.Prefix, block.Suffix} {
			if len(decoration) > maxDecoration || !utf8.ValidString(decoration) || unsafeText(decoration) {
				return fmt.Errorf("%w：字段格式包含非法字符或过长", ErrTemplate)
			}
		}
	}
	return nil
}

// Default returns fresh, independently editable movie/TV configuration.
func Default(kind Kind) (Template, error) {
	if kind != Movie && kind != TV {
		return Template{}, fmt.Errorf("%w：未知作品类型", ErrTemplate)
	}
	year := Block{Field: Year, Separator: " ", Prefix: "(", Suffix: ")"}
	template := Template{Kind: kind, Directories: [][]Block{{
		{Field: Title}, year, {Field: TMDBID, Separator: " ", Prefix: "{tmdb-", Suffix: "}"},
	}}, Filename: []Block{{Field: Title}}}
	if kind == TV {
		template.Directories = append(template.Directories, []Block{{Field: Season, Prefix: "Season ", Pad: 2}})
		template.Filename = append(template.Filename, Block{Field: SeasonEpisode, Separator: "."})
	} else {
		template.Filename = append(template.Filename, year)
	}
	for _, field := range []Field{Resolution, Source, Platform, VideoCodec, BitDepth, DynamicRange, FrameRate, AudioCodec, Channels, AudioEffect, Languages, SubtitleMode, Edition} {
		template.Filename = append(template.Filename, Block{Field: field, Separator: "."})
	}
	template.Filename = append(template.Filename, Block{Field: ReleaseGroup, Separator: "-"}, Block{Field: Custom, Separator: "-"})
	return template, nil
}
