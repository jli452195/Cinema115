package notification

import (
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxTemplateLength = 2800
const MaxMessageLength = 4096
const MaxCaptionLength = 1024

const DefaultTemplate = `{icon} <b>动漫订阅 · {event}</b>

<b>{title}</b>
🎞 集数：<b>{episode}</b>
🏷 字幕组：{group}
📡 来源：{provider}

<blockquote>{resource}</blockquote>

📁 目录：<code>{path}</code>
💬 状态：{reason}
🕒 {time}

<i>Cinema115 · 追番动态</i>`

type Variable struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

var Variables = []Variable{
	{Name: "icon", Label: "状态图标"}, {Name: "event", Label: "事件名称"},
	{Name: "title", Label: "动漫名称"}, {Name: "episode", Label: "发布集数"},
	{Name: "group", Label: "字幕组"}, {Name: "provider", Label: "资源站点"},
	{Name: "resource", Label: "资源名称"}, {Name: "path", Label: "下载或整理目录"},
	{Name: "reason", Label: "状态说明"}, {Name: "time", Label: "发生时间（北京时间）"},
	{Name: "job_id", Label: "任务编号"},
}

var ErrTemplate = errors.New("通知模板格式错误")
var placeholder = regexp.MustCompile(`\{([a-z_]+)\}`)
var tag = regexp.MustCompile(`<[^>]*>`)
var allowedTag = regexp.MustCompile(`^</?(b|i|u|s|code|pre|blockquote)>$`)

func utf16Length(value string) int {
	length := 0
	for _, r := range value {
		length++
		if r > 0xffff {
			length++
		}
	}
	return length
}

func FitsPhotoCaption(text, parseMode string) bool {
	if parseMode == "HTML" {
		length, err := validateHTML(text)
		return err == nil && length <= MaxCaptionLength
	}
	return utf16Length(text) <= MaxCaptionLength
}

func effective(template string) string {
	if strings.TrimSpace(template) == "" {
		return DefaultTemplate
	}
	return template
}

// 仅接受 Telegram 和网页预览共同支持的无属性标签。
func validateHTML(value string) (int, error) {
	decoder := xml.NewDecoder(strings.NewReader("<root>" + value + "</root>"))
	stack := make([]string, 0, 12)
	length := 0
	rootSeen := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return length, nil
		}
		if err != nil {
			return 0, fmt.Errorf("%w：HTML 标签或转义字符不完整", ErrTemplate)
		}
		switch node := token.(type) {
		case xml.StartElement:
			name := node.Name.Local
			if len(stack) == 0 && name == "root" && !rootSeen {
				rootSeen = true
				stack = append(stack, name)
				continue
			}
			if node.Name.Space != "" || len(node.Attr) != 0 || len(stack) > 12 {
				return 0, fmt.Errorf("%w：标签不能包含属性或过深嵌套", ErrTemplate)
			}
			switch name {
			case "b", "i", "u", "s", "code", "pre", "blockquote":
			default:
				return 0, fmt.Errorf("%w：仅支持 b、i、u、s、code、pre、blockquote 标签", ErrTemplate)
			}
			for _, parent := range stack {
				if parent == "code" || parent == "pre" || (name == "code" || name == "pre") && parent != "root" || name == "blockquote" && parent == "blockquote" {
					return 0, fmt.Errorf("%w：代码和引用标签不能这样嵌套", ErrTemplate)
				}
			}
			stack = append(stack, name)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			length += utf16Length(string(node))
		default:
			return 0, fmt.Errorf("%w：不支持注释或其他 HTML 指令", ErrTemplate)
		}
	}
}

func Validate(template string) error {
	template = effective(template)
	if !utf8.ValidString(template) || utf16Length(template) > MaxTemplateLength || strings.ContainsFunc(template, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' }) {
		return fmt.Errorf("%w：模板最多 2800 字符，不能包含控制字符", ErrTemplate)
	}
	known := make(map[string]bool, len(Variables))
	for _, variable := range Variables {
		known[variable.Name] = true
	}
	for _, match := range placeholder.FindAllStringSubmatch(template, -1) {
		if !known[match[1]] {
			return fmt.Errorf("%w：未知变量 %s", ErrTemplate, match[0])
		}
	}
	static := placeholder.ReplaceAllString(template, "")
	if strings.ContainsAny(static, "{}") {
		return fmt.Errorf("%w：变量请使用 {title} 形式", ErrTemplate)
	}
	for _, match := range tag.FindAllString(template, -1) {
		if !allowedTag.MatchString(match) {
			return fmt.Errorf("%w：请使用完整且无属性的受支持标签", ErrTemplate)
		}
	}
	length, err := validateHTML(template)
	if err == nil && (length == 0 || strings.TrimSpace(html.UnescapeString(tag.ReplaceAllString(template, ""))) == "") {
		return fmt.Errorf("%w：消息正文不能为空", ErrTemplate)
	}
	return err
}

func truncate(value string, budget int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, ""))
	if utf16Length(value) <= budget {
		return value
	}
	if budget <= 0 {
		return ""
	}
	length := 0
	for i, r := range value {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if length+width > budget-1 {
			return value[:i] + "…"
		}
		length += width
	}
	return value
}

// 按可见文本预算截断每次变量替换，保留成对 HTML 标签与完整 Unicode 字符。
func Render(template string, values map[string]string) (string, error) {
	template = effective(template)
	if err := Validate(template); err != nil {
		return "", err
	}
	staticLength, err := validateHTML(placeholder.ReplaceAllString(template, ""))
	if err != nil {
		return "", err
	}
	remaining := len(placeholder.FindAllStringIndex(template, -1))
	budget := MaxMessageLength - staticLength
	result := placeholder.ReplaceAllStringFunc(template, func(token string) string {
		limit := min(budget/remaining, 700)
		value := truncate(values[token[1:len(token)-1]], limit)
		remaining--
		budget -= utf16Length(value)
		return html.EscapeString(value)
	})
	if strings.TrimSpace(html.UnescapeString(tag.ReplaceAllString(result, ""))) == "" {
		return "", fmt.Errorf("%w：消息正文不能为空", ErrTemplate)
	}
	return strings.ReplaceAll(result, "&apos;", "&#39;"), nil
}
