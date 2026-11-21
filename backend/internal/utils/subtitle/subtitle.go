// Package subtitle 提供与存储无关的字幕命名规则。
package subtitle

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

func Supported(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".ass", ".ssa", ".srt", ".vtt", ".idx", ".sub":
		return true
	}
	return false
}

// PairStem 返回 VobSub 配对主干，保留目标文件名的大小写。
func PairStem(name string) string {
	ext := path.Ext(name)
	if strings.EqualFold(ext, ".idx") || strings.EqualFold(ext, ".sub") {
		return strings.TrimSuffix(name, ext)
	}
	return ""
}

type Move struct {
	SourceScope, SourceName, Target string
}

// PairConflicts 找出会拼成不同来源 IDX/SUB 的计划项。单独的 SUB 也可能是文本字幕。
func PairConflicts(moves []Move) []int {
	groups := make(map[string][]int)
	for i, move := range moves {
		if stem := PairStem(move.Target); stem != "" {
			key := strings.ToLower(stem)
			groups[key] = append(groups[key], i)
		}
	}
	var conflicts []int
	for _, indices := range groups {
		first := moves[indices[0]]
		ext := path.Ext(first.Target)
		hasPartner := false
		mismatch := len(indices) > 2
		for _, index := range indices[1:] {
			member := moves[index]
			hasPartner = hasPartner || !strings.EqualFold(ext, path.Ext(member.Target))
			mismatch = mismatch || PairStem(first.Target) != PairStem(member.Target) ||
				first.SourceScope != member.SourceScope || !strings.EqualFold(PairStem(first.SourceName), PairStem(member.SourceName))
		}
		if hasPartner && mismatch {
			conflicts = append(conflicts, indices...)
		}
	}
	return conflicts
}

// OutputTail 生成任务使用的字幕后缀和扩展名，保留显式指定的语言及版本。
func OutputTail(original, language, version string) (string, error) {
	if !Supported(original) || strings.ContainsAny(version, `/\:*?"<>|`) || strings.IndexFunc(version, func(r rune) bool { return r < 32 }) >= 0 || len([]rune(version)) > 30 || strings.TrimSpace(version) != version || strings.Trim(version, ".") != version {
		return "", fmt.Errorf("字幕文件名或版本无效")
	}
	if strings.EqualFold(language, "zh-en") {
		language = "zh-en"
	} else if language != "" {
		tokens := metadataTokens(language)
		if len(tokens) != 1 || rules.aliases[tokens[0]].language == "" || rules.aliases[tokens[0]].tag != "" || strings.IndexFunc(language, unicode.IsSpace) >= 0 {
			return "", fmt.Errorf("字幕语言无效")
		}
		language = rules.aliases[tokens[0]].language
	}
	return inspectSource(original, language).tail(version) + strings.ToLower(path.Ext(original)), nil
}

// AppendTail 使用视频完整主干生成实际文件名，不改变字幕正文。
func AppendTail(stem, tail string) (string, error) {
	if stem == "" || strings.ContainsAny(stem+tail, "/\\\x00\r\n") {
		return "", fmt.Errorf("字幕文件名无效")
	}
	name := stem + tail
	if len(name) > 255 {
		return "", fmt.Errorf("字幕文件名超过 255 字节，请缩短视频名或版本")
	}
	return name, nil
}
