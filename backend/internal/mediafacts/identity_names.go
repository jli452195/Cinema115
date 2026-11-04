package mediafacts

import (
	_ "embed"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

//go:embed name_data/TSCharacters.txt
var nameTraditionalCharacters string

var nameSimplifications = loadNameSimplifications()
var identityContentLabel = regexp.MustCompile(`[\[【({（［｛][^\[\]【】()（）{}［］｛｝]{1,48}[\]】)}）］｝]`)

// NameKey 只规范化比较键；原始标题仍保留原文供查询、展示和修正。
// 繁简采用 OpenCC 完整单字表的第一候选，不执行依赖上下文的词组转换。
func NameKey(value string) string {
	return strings.Map(func(r rune) rune {
		if simplified, ok := nameSimplifications[r]; ok {
			r = simplified
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, norm.NFKC.String(value))
}

func loadNameSimplifications() map[rune]rune {
	lines := strings.Split(nameTraditionalCharacters, "\n")
	out := make(map[rune]rune, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		from, to := []rune(fields[0]), []rune(fields[1])
		if len(from) == 1 && len(to) == 1 {
			out[from[0]] = to[0]
		}
	}
	return out
}

func identityContentDeclaration(value, source string, out *Identity) string {
	apply := func(label string) {
		content := identityContentKind(label)
		if content == "" {
			return
		}
		if out.ContentType == "" {
			out.ContentType, out.ContentTypeSource = content, source
		} else if out.ContentType != content {
			identityAddConflict(out, "CONTENT_TYPE_CONFLICT")
		}
	}
	// 仅接受括号标签或独立类别目录，不根据字幕组、语言、片名或普通关键词猜测。
	if source != "filename" && identityContentKind(value) != "" {
		apply(value)
		return ""
	}
	return strings.TrimSpace(identityContentLabel.ReplaceAllStringFunc(value, func(label string) string {
		if identityContentKind(label) == "" {
			return label
		}
		apply(label)
		return " "
	}))
}

func identityContentKind(label string) string {
	switch NameKey(label) {
	case "anime", "animation", "动画", "动画版", "动漫", "动漫版":
		return "animation"
	case "liveaction", "真人", "真人版", "真人剧", "真人电影":
		return "live_action"
	default:
		return ""
	}
}

func identityAddConflict(out *Identity, code string) {
	if out.Conflict == "" {
		out.Conflict = code
		return
	}
	codes := strings.Split(out.Conflict, ",")
	for _, existing := range codes {
		if existing == code {
			return
		}
	}
	codes = append(codes, code)
	sort.Strings(codes)
	out.Conflict = strings.Join(codes, ",")
}
