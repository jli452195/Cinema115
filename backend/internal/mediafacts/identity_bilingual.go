package mediafacts

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

var identityTitleSeparators = strings.NewReplacer("_", " ", ".", " ")

// 明确的中英文双标题最多产生两个完整名称，纯空格混排或标题内嵌英文不拆分。
func identityBilingualTitles(raw string) (string, string) {
	raw = strings.Trim(raw, " ._\t\r\n-")
	// 年份截断点可能位于配对括号内部，去掉截断后残留的末尾左括号。
	raw = strings.TrimRight(raw, " ._\t\r\n-([【（{")
	if raw == "" || utf8.RuneCountInString(raw) > 400 {
		return "", ""
	}
	chinese, english := "", ""
	for _, pair := range [][2]string{{"[", "]"}, {"【", "】"}, {"(", ")"}, {"（", "）"}} {
		if !strings.HasPrefix(raw, pair[0]) {
			continue
		}
		end := strings.Index(raw, pair[1])
		if end < 0 {
			return "", ""
		}
		chinese = strings.TrimSpace(raw[len(pair[0]):end])
		english = strings.Trim(raw[end+len(pair[1]):], " ._\t\r\n-")
		break
	}
	if chinese == "" {
		boundary := strings.IndexFunc(raw, func(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' })
		if boundary <= 0 || raw[boundary-1] != '.' && raw[boundary-1] != '_' {
			return "", ""
		}
		chinese, english = strings.Trim(raw[:boundary], " ._\t\r\n-"), raw[boundary:]
	}
	chinese = strings.TrimSpace(identityTitleSeparators.Replace(chinese))
	english = strings.Join(strings.Fields(identityTitleSeparators.Replace(english)), " ")
	if !identityChineseTitle(chinese) || !identityEnglishTitle(english) {
		return "", ""
	}
	return chinese, english
}

func identityChineseTitle(title string) bool {
	count := 0
	for _, r := range title {
		switch {
		case unicode.Is(unicode.Han, r):
			count++
		case unicode.IsDigit(r), unicode.IsSpace(r), strings.ContainsRune("，。！？：；、·—-,'!?:;", r):
		default:
			return false
		}
	}
	return count >= 2 && utf8.RuneCountInString(title) <= 200
}

func identityEnglishTitle(title string) bool {
	letters, words := 0, 0
	inWord := false
	for _, r := range title {
		if r == ' ' {
			inWord = false
		}
		switch {
		case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
			letters++
			if !inWord {
				words++
			}
			inWord = true
		case r >= '0' && r <= '9', strings.ContainsRune(" ,'-!?:;&", r):
		default:
			return false
		}
	}
	// 单个短英文词可能属于正式混合片名，至少两个词才提供拆分证据。
	return words >= 2 && letters >= 5 && len(title) <= 200
}
