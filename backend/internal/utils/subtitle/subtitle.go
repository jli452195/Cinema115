// Package subtitle contains storage-independent subtitle naming rules.
package subtitle

import (
	"fmt"
	"path"
	"strings"
)

func Supported(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".ass", ".ssa", ".srt", ".vtt", ".idx", ".sub":
		return true
	}
	return false
}

// Name follows the final video stem, without changing subtitle contents.
func Name(video, original, language, version string) (string, error) {
	if !Supported(original) || strings.ContainsAny(video+version, "/\\\x00\r\n") || len([]rune(version)) > 30 {
		return "", fmt.Errorf("字幕文件名或版本无效")
	}
	switch language {
	case "", "zh-CN", "zh-TW", "zh", "zh-en", "ja", "en":
	default:
		return "", fmt.Errorf("字幕语言无效")
	}
	name := strings.TrimSuffix(video, path.Ext(video))
	if version != "" {
		name += "." + version
	}
	if language != "" {
		name += "." + language
	}
	name += strings.ToLower(path.Ext(original))
	if len(name) > 255 {
		return "", fmt.Errorf("字幕文件名超过 255 字节，请缩短视频名或版本")
	}
	return name, nil
}
