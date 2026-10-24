package naming

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Data contains facts supplied by the caller, never guesses about media streams.
// Values contains text fields; numbers and the source name have dedicated fields.
// Nil numbers mean unknown; zero is a valid season/episode. TitleOnly prevents a
// release title's trailing tags from being mistaken for a real file extension.
type Data struct {
	Values       map[Field]string `json:"values"`
	Season       *int             `json:"season,omitempty"`
	Episode      *int             `json:"episode,omitempty"`
	EndEpisode   *int             `json:"endEpisode,omitempty"`
	OriginalName string           `json:"originalName,omitempty"`
	TitleOnly    bool             `json:"titleOnly,omitempty"`
}

type Result struct {
	Directory    string `json:"directory"`
	Stem         string `json:"stem"`
	Extension    string `json:"extension"`
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
}

// Companion keeps the complete rendered stem, including its dots. A title-only
// preview must never have its last release tag mistaken for an extension.
func Companion(video Result, tail, originalName string) (Result, error) {
	components := []string{video.Stem}
	if video.Directory != "" {
		components = append(components, strings.Split(video.Directory, "/")...)
	}
	for _, component := range components {
		if component == "" || component != cleanText(component) || unsafeText(component) || reservedName(component) || len(component) > maxComponentBytes || !utf8.ValidString(component) {
			return Result{}, fmt.Errorf("%w：视频命名结果无效", ErrInput)
		}
	}
	extension := strings.ToLower(path.Ext(originalName))
	if unsafeText(tail) || !utf8.ValidString(tail) || !utf8.ValidString(originalName) || strings.ContainsAny(originalName, `/\`) || !validExtension(extension) || extension == "" {
		return Result{}, fmt.Errorf("%w：字幕名称或后缀无效", ErrInput)
	}
	name := video.Stem + tail + extension
	if len(name) > maxComponentBytes {
		return Result{}, fmt.Errorf("%w：字幕文件名超过 %d 字节", ErrInput, maxComponentBytes)
	}
	return Result{Directory: video.Directory, Stem: video.Stem + tail, Extension: extension, Name: name, RelativePath: path.Join(video.Directory, name)}, nil
}

func (r *Renderer) Render(data Data) (Result, error) {
	if r == nil || r.template.Kind == "" {
		return Result{}, fmt.Errorf("%w：请先编译模板", ErrTemplate)
	}
	if err := validateData(r.template.Kind, data); err != nil {
		return Result{}, err
	}
	extension := ""
	if !data.TitleOnly {
		extension = strings.ToLower(path.Ext(data.OriginalName))
	}
	if !validExtension(extension) {
		return Result{}, fmt.Errorf("%w：原文件扩展名无效", ErrInput)
	}
	directories := make([]string, 0, len(r.template.Directories))
	for i, layer := range r.template.Directories {
		name, _ := renderLayer(layer, data)
		if name == "" { // An entirely unknown optional directory adds no path level.
			continue
		}
		if len(name) > maxComponentBytes {
			return Result{}, fmt.Errorf("%w：目录层 %d 超过 %d 字节，请缩短模板或字段", ErrInput, i+1, maxComponentBytes)
		}
		directories = append(directories, name)
	}
	stem, hasIdentity := renderLayer(r.template.Filename, data)
	if stem == "" || !hasIdentity {
		return Result{}, fmt.Errorf("%w：文件名缺少可用作品名称或原文件名", ErrInput)
	}
	if len(stem)+len(extension) > maxComponentBytes {
		return Result{}, fmt.Errorf("%w：文件名超过 %d 字节，请缩短模板或字段", ErrInput, maxComponentBytes)
	}
	out := Result{Directory: strings.Join(directories, "/"), Stem: stem, Extension: extension, Name: stem + extension}
	out.RelativePath = path.Join(out.Directory, out.Name)
	return out, nil
}

func validateData(kind Kind, data Data) error {
	if len(data.Values) > len(fields) {
		return fmt.Errorf("%w：字段过多", ErrInput)
	}
	for field, value := range data.Values {
		_, known := definition(field)
		if !known || field == Season || field == Episode || field == SeasonEpisode || field == OriginalName {
			return fmt.Errorf("%w：文本字段 %q 无效", ErrInput, field)
		}
		if len(value) > maxValueBytes || !utf8.ValidString(value) {
			return fmt.Errorf("%w：字段 %q 过长或编码无效", ErrInput, field)
		}
	}
	if len(data.OriginalName) > maxValueBytes || !utf8.ValidString(data.OriginalName) || !data.TitleOnly && strings.ContainsAny(data.OriginalName, `/\`) {
		return fmt.Errorf("%w：原文件名无效，请传入文件名而非路径", ErrInput)
	}
	for _, number := range []*int{data.Season, data.Episode, data.EndEpisode} {
		if number != nil && *number < 0 {
			return fmt.Errorf("%w：季集编号不能为负数", ErrInput)
		}
	}
	if kind == TV && (data.Season == nil || data.Episode == nil) {
		return fmt.Errorf("%w：剧集须提供目标季号与集号", ErrInput)
	}
	if data.EndEpisode != nil && (data.Episode == nil || *data.EndEpisode < *data.Episode) {
		return fmt.Errorf("%w：结束集不能小于起始集", ErrInput)
	}
	return nil
}

func renderLayer(layer []Block, data Data) (string, bool) {
	var out strings.Builder
	hasIdentity := false
	for _, block := range layer {
		value := cleanText(fieldValue(block, data))
		if value == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString(block.Separator)
		}
		out.WriteString(block.Prefix)
		out.WriteString(value)
		out.WriteString(block.Suffix)
		hasIdentity = hasIdentity || identity(block.Field)
	}
	name := strings.Trim(strings.TrimSpace(out.String()), ". ")
	if reservedName(name) {
		name = "_" + name
	}
	return name, hasIdentity
}

func fieldValue(block Block, data Data) string {
	switch block.Field {
	case Season:
		return number(*data.Season, block.Pad)
	case Episode:
		return number(*data.Episode, block.Pad)
	case SeasonEpisode:
		value := "S" + number(*data.Season, 2) + "E" + number(*data.Episode, 2)
		if data.EndEpisode != nil && *data.EndEpisode > *data.Episode {
			value += "-E" + number(*data.EndEpisode, 2)
		}
		return value
	case OriginalName:
		if data.TitleOnly {
			return data.OriginalName
		}
		return strings.TrimSuffix(data.OriginalName, path.Ext(data.OriginalName))
	default:
		return data.Values[block.Field]
	}
}

func number(value int, width int) string {
	text := strconv.Itoa(value)
	if len(text) < width {
		text = strings.Repeat("0", width-len(text)) + text
	}
	return text
}

func invalidRune(r rune) bool {
	return unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r)
}

func unsafeText(value string) bool { return strings.ContainsFunc(value, invalidRune) }

func cleanText(value string) string {
	value = strings.Map(func(r rune) rune {
		if invalidRune(r) {
			return '_'
		}
		return r
	}, value)
	return strings.Trim(strings.TrimSpace(value), ". ")
}

func validExtension(extension string) bool {
	if extension == "" {
		return true
	}
	if len(extension) < 2 || len(extension) > 16 || extension[0] != '.' {
		return false
	}
	for _, char := range extension[1:] {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func reservedName(name string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	base = strings.TrimRight(base, " ")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if strings.HasPrefix(base, prefix) {
			suffix := strings.TrimPrefix(base, prefix)
			if len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9' || suffix == "¹" || suffix == "²" || suffix == "³" {
				return true
			}
		}
	}
	return false
}
