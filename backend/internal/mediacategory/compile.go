package mediacategory

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"strm115/backend/internal/naming"
)

// MaxYAMLBytes bounds configuration parsing; callers may use the same API limit.
const MaxYAMLBytes = 256 << 10

// ConfigError points at an invalid field or value for both editor modes.
// Syntax errors are wrapped separately with the YAML parser's own diagnostics.
type ConfigError struct {
	Field   string `json:"field"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("%s：%s（%s，第 %d 行，第 %d 列）", ErrConfig, e.Message, e.Field, e.Line, e.Column)
}

func (e *ConfigError) Unwrap() error { return ErrConfig }

func problem(node *yaml.Node, field, message string) error {
	return &ConfigError{Field: field, Line: max(1, node.Line), Column: max(1, node.Column), Message: message}
}

// Compile validates the complete document before constructing an immutable
// classifier. It never serializes YAML, so source formatting/comments stay with
// the caller's original document. No partial rules are returned on error.
func Compile(source string) (*Classifier, error) {
	if len(source) > MaxYAMLBytes {
		return nil, fmt.Errorf("%w：YAML 不能超过 %d KiB", ErrConfig, MaxYAMLBytes/1024)
	}
	decoder := yaml.NewDecoder(strings.NewReader(source))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w：读取 YAML: %w", ErrConfig, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w：读取 YAML: %w", ErrConfig, err)
		}
		return nil, problem(&extra, "$", "仅支持一份 YAML 文档")
	}
	if len(document.Content) != 1 {
		return nil, problem(&document, "$", "须提供完整的分类配置")
	}
	root, err := object(document.Content[0], "$", []string{"movie", "tv"}, "movie", "tv")
	if err != nil {
		return nil, err
	}
	movie, err := compileSection(root["movie"], "movie")
	if err != nil {
		return nil, err
	}
	tv, err := compileSection(root["tv"], "tv")
	if err != nil {
		return nil, err
	}
	return &Classifier{movie: movie, tv: tv}, nil
}

func expect(node *yaml.Node, field string, kind yaml.Kind, tag string) error {
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return problem(node, field, "不支持 YAML 锚点或别名")
	}
	if node.Kind != kind || node.Tag != tag {
		return problem(node, field, "字段类型不符或使用了不支持的 YAML 标签")
	}
	return nil
}

func object(node *yaml.Node, field string, allowed []string, required ...string) (map[string]*yaml.Node, error) {
	if err := expect(node, field, yaml.MappingNode, "!!map"); err != nil {
		return nil, err
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if err := expect(key, field, yaml.ScalarNode, "!!str"); err != nil {
			return nil, err
		}
		if !slices.Contains(allowed, key.Value) {
			return nil, problem(key, field+"."+key.Value, "不支持此字段")
		}
		if fields[key.Value] != nil {
			return nil, problem(key, field+"."+key.Value, "字段重复")
		}
		fields[key.Value] = value
	}
	for _, key := range required {
		if fields[key] == nil {
			return nil, problem(node, field+"."+key, "缺少必填字段")
		}
	}
	return fields, nil
}

func text(node *yaml.Node, field string) (string, error) {
	if err := expect(node, field, yaml.ScalarNode, "!!str"); err != nil {
		return "", err
	}
	if strings.TrimSpace(node.Value) == "" {
		return "", problem(node, field, "文本不能为空")
	}
	return node.Value, nil
}

func directory(node *yaml.Node, field string) (string, error) {
	value, err := text(node, field)
	if err != nil {
		return "", err
	}
	if err := naming.ValidateDirectory(value); err != nil {
		return "", fmt.Errorf("%w: %w", problem(node, field, "分类目录无效"), err)
	}
	return value, nil
}

func positiveInteger(node *yaml.Node, field string) (int, error) {
	if err := expect(node, field, yaml.ScalarNode, "!!int"); err != nil {
		return 0, err
	}
	var value int
	if err := node.Decode(&value); err != nil || value <= 0 {
		return 0, problem(node, field, "须为有效正整数")
	}
	return value, nil
}

func compileSection(node *yaml.Node, field string) (section, error) {
	fields, err := object(node, field, []string{"fallback", "rules"}, "fallback", "rules")
	if err != nil {
		return section{}, err
	}
	fallback, err := directory(fields["fallback"], field+".fallback")
	if err != nil {
		return section{}, err
	}
	list := fields["rules"]
	if err := expect(list, field+".rules", yaml.SequenceNode, "!!seq"); err != nil {
		return section{}, err
	}
	rules := make([]rule, 0, len(list.Content))
	names := make(map[string]bool, len(list.Content))
	for i, item := range list.Content {
		itemField := field + ".rules[" + strconv.Itoa(i) + "]"
		fields, err := object(item, itemField, []string{"name", "match"}, "name", "match")
		if err != nil {
			return section{}, err
		}
		name, err := text(fields["name"], itemField+".name")
		if err != nil {
			return section{}, err
		}
		name = strings.TrimSpace(name)
		if err := naming.ValidateDirectory(name); err != nil {
			return section{}, fmt.Errorf("%w: %w", problem(fields["name"], itemField+".name", "分类名称无效"), err)
		}
		if names[name] {
			return section{}, problem(fields["name"], itemField+".name", "当前作品类型内规则名称重复")
		}
		names[name] = true
		match, err := compileConditions(fields["match"], itemField+".match")
		if err != nil {
			return section{}, err
		}
		rules = append(rules, rule{name: name, match: match})
	}
	return section{fallback: fallback, rules: rules}, nil
}

func compileConditions(node *yaml.Node, field string) (conditions, error) {
	allowed := []string{"genres", "countries", "languages"}
	fields, err := object(node, field, allowed)
	if err != nil {
		return conditions{}, err
	}
	if len(fields) == 0 {
		return conditions{}, problem(node, field, "至少填写一个匹配条件")
	}
	var match conditions
	for _, key := range allowed {
		list := fields[key]
		if list == nil {
			continue
		}
		listField := field + "." + key
		if err := expect(list, listField, yaml.SequenceNode, "!!seq"); err != nil {
			return conditions{}, err
		}
		if len(list.Content) == 0 {
			return conditions{}, problem(list, listField, "条件列表不能为空")
		}
		var codes []string
		var genreSeen map[int]bool
		var codeSeen map[string]bool
		if key == "genres" {
			match.genres = make([]int, 0, len(list.Content))
			genreSeen = make(map[int]bool, len(list.Content))
		} else {
			codes = make([]string, 0, len(list.Content))
			codeSeen = make(map[string]bool, len(list.Content))
		}
		for i, item := range list.Content {
			itemField := listField + "[" + strconv.Itoa(i) + "]"
			if key == "genres" {
				value, err := positiveInteger(item, itemField)
				if err != nil {
					return conditions{}, err
				}
				if genreSeen[value] {
					return conditions{}, problem(item, itemField, "类型 ID 重复")
				}
				genreSeen[value] = true
				match.genres = append(match.genres, value)
				continue
			}
			value, err := text(item, itemField)
			if err != nil {
				return conditions{}, err
			}
			value = strings.TrimSpace(value)
			if !validCode(value) {
				return conditions{}, problem(item, itemField, "代码须为两位英文字母")
			}
			if key == "languages" {
				value = strings.ToLower(value)
			} else {
				value = strings.ToUpper(value)
			}
			if codeSeen[value] {
				return conditions{}, problem(item, itemField, "代码重复")
			}
			codeSeen[value] = true
			codes = append(codes, value)
		}
		switch key {
		case "countries":
			match.countries = codes
		case "languages":
			match.languages = codes
		}
	}
	return match, nil
}
