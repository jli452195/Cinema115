package mediafacts

import (
	"regexp"
	"slices"
	"strings"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
	"strm115/backend/internal/utils/subtitle"
)

const videoSubtitleCode = `(?:zh(?:-(?:cn|tw|hk|sg))?|ja|en)`
const videoSubtitleCodeList = videoSubtitleCode + `(?:\+` + videoSubtitleCode + `)*`

var (
	// 点号分隔的独立括号组是命名模板生成的字幕字段，例如 AAC.[zh-CN]。
	videoAudioLanguage   = regexp.MustCompile(`(?i)(^|[^a-z0-9])(?:AAC|FLAC|ALAC|AC-?3|E-?AC-?3|DDP?|DTS(?:-HD)?|TRUEHD|OPUS|AUDIO)[ _-]*[\[【(（]([^\[\]【】()（）]*)[\]】)）]`)
	videoSubtitleMarker  = regexp.MustCompile(`(?i)内封|內封|内嵌|內嵌|嵌入|外挂|外掛|字幕|softsub|hardsub|external`)
	videoSubtitleField   = regexp.MustCompile(`(?i)\.((?:` + videoSubtitleCodeList + `)|softsub|hardsub|external)`)
	videoSubtitleCodeTag = regexp.MustCompile(`(?i)^` + videoSubtitleCodeList + `$`)
	videoSubtitleContext = regexp.MustCompile(`(?i)(?:^|[. (])(?:s[0-9]{1,3}e[0-9]{1,4}|(?:19|20)[0-9]{2}|[0-9]{3,4}[pi]|[48]k|web[.-]?dl|bluray|remux|h26[45]|hevc|aac|flac|opus|ddp|truehd|dts)(?:$|[. )])`)
	videoSubtitleSidecar = regexp.MustCompile(`(?i)\.(?:ass|ssa|srt|sup|vtt|sub|idx)\.(?:mkv|mp4|m2ts|ts|webm|mov|strm)$`)
)

var videoSubtitleLanguages = [...]struct{ code, legacy, short, label string }{
	{"zh-CN", "zh-Hans", "简", "简体"}, {"zh-TW", "zh-Hant", "繁", "繁体"}, {"zh-HK", "", "港", "香港中文"},
	{"zh-SG", "", "新", "新加坡中文"}, {"zh", "", "中", "中文"}, {"ja", "", "日", "日语"}, {"en", "", "英", "英语"},
}

// VideoSubtitleFields 提取原始发布标题中可用于命名的字幕字段。
func VideoSubtitleFields(name string) (language, mode string) {
	language, mode, _ = videoSubtitleValues(name, releaseparse.Spec{})
	return language, mode
}

func videoSubtitleLanguageValue(languages []string) string {
	if len(languages) == 0 {
		return ""
	}
	var value strings.Builder
	for _, language := range videoSubtitleLanguages {
		if slices.Contains(languages, language.code) || language.legacy != "" && slices.Contains(languages, language.legacy) {
			if value.Len() > 0 {
				value.WriteByte('+')
			}
			value.WriteString(language.code)
		}
	}
	return value.String()
}

func videoSubtitleModeValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hard", "hardsub", "内嵌", "內嵌":
		return "hardsub"
	case "soft", "softsub", "内封", "內封":
		return "softsub"
	case "external", "外挂", "外掛":
		return "external"
	default:
		return ""
	}
}

func videoSubtitleValues(name string, hint releaseparse.Spec) (language, mode string, conflict bool) {
	name = videoAudioLanguage.ReplaceAllStringFunc(name, func(annotation string) string {
		match := videoAudioLanguage.FindStringSubmatch(annotation)
		if videoSubtitleMarker.MatchString(match[2]) {
			return annotation
		}
		return match[1] + " "
	})
	detected, conflict := specificationWithSubtitleModeConflict(name)
	languages := detected.Languages
	var modes []string
	modes = appendUnique(modes, videoSubtitleModeValue(detected.SubtitleMode))
	for _, match := range subtitleBrackets.FindAllStringSubmatch(name, -1) {
		languages = append(languages, subtitle.LanguageCodes(match[1])...)
		modes = appendUnique(modes, videoSubtitleModeValue(match[1]))
	}
	if fields := videoSubtitleField.FindAllStringSubmatchIndex(name, -1); len(fields) > 0 && !videoSubtitleSidecar.MatchString(name) {
		lastLanguageEnd := -1
		for _, match := range fields {
			start, end := match[0], match[1]
			value := name[match[2]:match[3]]
			if end < len(name) && !strings.ContainsRune(".-", rune(name[end])) {
				continue
			}
			prefix := name[:start]
			// 独立点号字段需要集号、年份、规格或前一个字幕字段作为依据，防止误读片名中的 External。
			bracket := strings.LastIndexByte(prefix, '[')
			hasLanguage := bracket >= 0 && strings.HasSuffix(prefix, "]") && videoSubtitleCodeTag.MatchString(prefix[bracket+1:len(prefix)-1])
			if lastLanguageEnd != start && !hasLanguage && !videoSubtitleContext.MatchString(prefix) {
				continue
			}
			if code := subtitle.LanguageCodes(value); len(code) > 0 {
				if end < len(name) && name[end] == '-' && !strings.ContainsAny(value, "-+") {
					continue
				}
				languages = append(languages, code...)
				lastLanguageEnd = end
			} else {
				modes = appendUnique(modes, videoSubtitleModeValue(value))
			}
		}
	}
	if len(languages) == 0 {
		languages = hint.Languages
	}
	if conflict || len(modes) > 1 {
		return videoSubtitleLanguageValue(languages), "", true
	}
	switch len(modes) {
	case 0:
		mode = videoSubtitleModeValue(hint.SubtitleMode)
	case 1:
		mode = modes[0]
	}
	return videoSubtitleLanguageValue(languages), mode, false
}

// NormalizeVideoSubtitleValues 将旧清单中的字幕字段转换为当前命名格式。
func NormalizeVideoSubtitleValues(values map[naming.Field]string) {
	if language := values[naming.Languages]; language != "" {
		codes := subtitle.LanguageCodes(language)
		if len(codes) == 0 {
			codes = releaseparse.Specification("[" + language + "]").Languages
		}
		values[naming.Languages] = videoSubtitleLanguageValue(codes)
	}
	if mode := values[naming.SubtitleMode]; mode != "" {
		values[naming.SubtitleMode] = videoSubtitleModeValue(mode)
	}
}

// VideoSubtitleDisplayLabels 为资源卡片生成中文摘要。
func VideoSubtitleDisplayLabels(language, mode string) (string, string) {
	var compact strings.Builder
	var label string
	count := 0
	for _, code := range strings.Split(language, "+") {
		for _, candidate := range videoSubtitleLanguages {
			if candidate.code == code {
				compact.WriteString(candidate.short)
				label = candidate.label
				count++
				break
			}
		}
	}
	if count != 1 {
		label = compact.String()
	}
	return label, map[string]string{"hardsub": "内嵌", "softsub": "内封", "external": "外挂"}[mode]
}
