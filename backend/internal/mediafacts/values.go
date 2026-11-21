// Package mediafacts extracts explicit filename tags without network or stream reads.
package mediafacts

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/recognition"
	"strm115/backend/internal/releaseparse"
)

// ValuesWithRecognition 用固定词表识别普通整理的发布组和平台；RSS 继续使用原有 Values 接口。
func ValuesWithRecognition(name string, hint releaseparse.Spec, snapshot recognition.Snapshot) (map[naming.Field]string, error) {
	values := valuesWithPlatform(name, hint, "", false)
	result, err := snapshot.Recognize(name)
	if err != nil {
		return nil, fmt.Errorf("识别文件发布信息: %w", err)
	}
	if len(result.Conflicts) > 0 {
		return nil, fmt.Errorf("文件发布信息命中冲突的词表映射，请在识别词表中测试并修正规则")
	}
	values[naming.ReleaseGroup] = strings.Join(result.ReleaseGroups, "&")
	if len(result.Platforms) == 1 {
		values[naming.Platform] = result.Platforms[0]
	}
	return values, nil
}

// ValuesWithRecognizedRelease 补充规格时复用同名文件、同一词表快照已确认的发布组和平台。
// 仅重算普通规格，不继承其他字段，也不把当前全局词表带入原任务。
func ValuesWithRecognizedRelease(name string, hint releaseparse.Spec, recognized map[naming.Field]string) map[naming.Field]string {
	values := valuesWithPlatform(name, hint, recognized[naming.ReleaseGroup], false)
	if platform, ok := recognized[naming.Platform]; ok {
		values[naming.Platform] = platform
	}
	return values
}

var mediaFactTokens = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(web[ .-]?dl|webrip|blu[ .-]?ray|bdrip|remux|hdtv|dvdrip|nf|netflix|amzn|dsnp|atvp|hmax|bilibili|h[ ._-]?26[45]|x26[45]|avc|hevc|av1|vp9|(?:8|10|12)[ ._-]?(?:bit|b)|profile[ ._-]?[578](?:[ ._-]?1)?|p(?:5|7|8(?:[ ._-]?1)?)|fel|mel|el|rpu|hdr10\+|hdr10plus|hdr10|dolby[ ._-]?vision|dovi|dv|hdr|hlg|sdr|progressive|interlaced|cfr|vfr|truehd|dts[ .-]?hd[ .-]?ma|dts[ .-]?x|dts|ddp|dd|e[ ._-]?ac[ ._-]?3|ac[ ._-]?3|aac|flac|opus|atmos|mkv|mp4|m2ts|ts|webm|mov|iso|[12567]\.1|2\.0|(?:[0-9]{1,2}(?:\.[0-9]+)?(?:fps|[pi])|[0-9]{2,3}(?:\.[0-9]+)?fps))`)
var mediaFactCanonical = strings.NewReplacer(" ", "", ".", "", "-", "", "_", "")
var mediaJoinedChannels = regexp.MustCompile(`(?i)(truehd|dts|ddp|dd|eac3|ac3|aac|flac|opus)([12567]\.1|2\.0)`)

// Only explicit filename/source tags are used. Unknown and conflicting scalar
// tags stay empty; this is unrelated to RSS matching and never reads streams.
func Values(name string, hint releaseparse.Spec, group string) map[naming.Field]string {
	return valuesWithPlatform(name, hint, group, true)
}

func valuesWithPlatform(name string, hint releaseparse.Spec, group string, includePlatform bool) map[naming.Field]string {
	search := Search(name)
	if search.Resolution != "" || resourceQuality.MatchString(name) {
		hint.Resolution = search.Resolution
	}
	if len(search.Languages) > 0 {
		hint.Languages = search.Languages
	}
	if search.SubtitleMode != "" {
		hint.SubtitleMode = search.SubtitleMode
	}
	values := map[naming.Field]string{naming.Resolution: hint.Resolution, naming.ReleaseGroup: group}
	values[naming.Languages] = subtitleLanguages(hint.Languages)
	values[naming.SubtitleMode] = map[string]string{"hard": "内嵌", "soft": "内封", "external": "外挂"}[hint.SubtitleMode]
	tags := make(map[naming.Field][]string, 16)
	// 下载资源的标题规格属于已解析的输入事实。与文件名标签一起参与冲突检查，
	// 只有唯一值才会进入命名模板，避免把不确定的规格写进目标名称。
	switch strings.ToLower(hint.Codec) {
	case "h264":
		tags[naming.VideoCodec] = appendUnique(tags[naming.VideoCodec], "H264")
	case "h265":
		tags[naming.VideoCodec] = appendUnique(tags[naming.VideoCodec], "HEVC")
	case "av1", "vp9":
		tags[naming.VideoCodec] = appendUnique(tags[naming.VideoCodec], strings.ToUpper(hint.Codec))
	}
	if hint.BitDepth != "" {
		tags[naming.BitDepth] = appendUnique(tags[naming.BitDepth], strings.TrimSuffix(strings.ToLower(hint.BitDepth), "bit")+"bit")
	}
	if hint.Container != "" {
		tags[naming.Container] = appendUnique(tags[naming.Container], strings.ToUpper(hint.Container))
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".strm", ".mkv", ".mp4", ".m2ts", ".ts", ".webm", ".mov", ".iso":
		name = strings.TrimSuffix(name, path.Ext(name))
	}
	name = mediaJoinedChannels.ReplaceAllString(name, "$1 $2")
	for _, index := range mediaFactTokens.FindAllStringSubmatchIndex(name, -1) {
		if end := index[1]; end < len(name) && (name[end] >= 'a' && name[end] <= 'z' || name[end] >= 'A' && name[end] <= 'Z' || name[end] >= '0' && name[end] <= '9') {
			continue
		}
		raw := strings.ToLower(name[index[2]:index[3]])
		key := mediaFactCanonical.Replace(raw)
		var field naming.Field
		value := ""
		switch key {
		case "webdl", "webrip", "bluray", "bdrip", "remux", "hdtv", "dvdrip":
			field = naming.Source
			value = map[string]string{"webdl": "WEB-DL", "webrip": "WEBRip", "bluray": "BluRay", "bdrip": "BDRip", "remux": "REMUX", "hdtv": "HDTV", "dvdrip": "DVDRip"}[key]
			if key == "remux" {
				tags[naming.SourceModifier] = appendUnique(tags[naming.SourceModifier], value)
			} else {
				tags[naming.SourceMedium] = appendUnique(tags[naming.SourceMedium], value)
			}
		case "nf", "netflix", "amzn", "dsnp", "atvp", "hmax", "bilibili":
			if !includePlatform {
				continue
			}
			field = naming.Platform
			value = strings.ToUpper(key)
			if key == "netflix" {
				value = "NF"
			}
		case "h264", "x264", "avc":
			field, value = naming.VideoCodec, "H264"
			if key == "x264" {
				tags[naming.Encoder] = appendUnique(tags[naming.Encoder], "x264")
			}
		case "h265", "x265", "hevc":
			field, value = naming.VideoCodec, "HEVC"
			if key == "x265" {
				tags[naming.Encoder] = appendUnique(tags[naming.Encoder], "x265")
			}
		case "av1", "vp9":
			field, value = naming.VideoCodec, strings.ToUpper(key)
		case "8bit", "10bit", "12bit", "8b", "10b", "12b":
			field, value = naming.BitDepth, map[string]string{"8bit": "8bit", "10bit": "10bit", "12bit": "12bit", "8b": "8bit", "10b": "10bit", "12b": "12bit"}[key]
		case "profile5", "profile7", "profile8", "profile81", "p5", "p7", "p8", "p81":
			field, value = naming.DVProfile, map[string]string{"profile5": "P5", "profile7": "P7", "profile8": "P8", "profile81": "P8.1", "p5": "P5", "p7": "P7", "p8": "P8", "p81": "P8.1"}[key]
			tags[naming.DynamicRange] = appendUnique(tags[naming.DynamicRange], "DV")
		case "fel", "mel":
			field, value = naming.DVLayer, strings.ToUpper(key)
			tags[naming.DynamicRange] = appendUnique(tags[naming.DynamicRange], "DV")
		case "el":
			field, value = naming.DVLayer, "EL"
		case "rpu":
			field, value = naming.DVMetadata, "RPU"
			tags[naming.DynamicRange] = appendUnique(tags[naming.DynamicRange], "DV")
		case "dv", "dovi", "dolbyvision":
			field, value = naming.DynamicRange, "DV"
		case "hdr10+", "hdr10plus", "hdr10", "hdr", "hlg", "sdr":
			field, value = naming.DynamicRange, strings.ToUpper(key)
			if key == "hdr10plus" {
				value = "HDR10+"
			}
			if key == "hdr10" || key == "sdr" {
				tags[naming.HDRBaseLayer] = appendUnique(tags[naming.HDRBaseLayer], strings.ToUpper(key))
			}
		case "aac", "flac", "opus", "dts", "dtshdma", "truehd", "ddp", "eac3", "dd", "ac3":
			field = naming.AudioCodec
			value = strings.ToUpper(key)
			if key == "eac3" {
				value = "DDP"
			}
			if key == "ac3" {
				value = "DD"
			}
		case "atmos", "dtsx":
			field, value = naming.AudioEffect, map[string]string{"atmos": "Atmos", "dtsx": "DTS-X"}[key]
		case "20", "11", "21", "51", "61", "71":
			field, value = naming.Channels, raw
		case "progressive", "interlaced":
			field, value = naming.ScanType, map[string]string{"progressive": "Progressive", "interlaced": "Interlaced"}[key]
		case "cfr", "vfr":
			field, value = naming.FrameRateMode, strings.ToUpper(key)
		case "mkv", "mp4", "m2ts", "ts", "webm", "mov", "iso":
			field, value = naming.Container, strings.ToUpper(key)
		default:
			if strings.HasSuffix(key, "fps") {
				field, value = naming.FrameRate, raw
			} else if last := key[len(key)-1:]; last == "p" || last == "i" {
				field, value = naming.FrameRate, strings.TrimSuffix(raw, last)+"fps"
				if last == "p" {
					tags[naming.ScanType] = appendUnique(tags[naming.ScanType], "Progressive")
				} else {
					tags[naming.ScanType] = appendUnique(tags[naming.ScanType], "Interlaced")
				}
			}
		}
		if field == "" {
			continue
		}
		tags[field] = appendUnique(tags[field], value)
	}
	hasDV := contains(tags[naming.DynamicRange], "DV")
	for field, list := range tags {
		if field == naming.DVProfile || field == naming.DVLayer || field == naming.DVMetadata {
			if !hasDV {
				continue
			}
		}
		if field == naming.HDRBaseLayer && !hasDV {
			continue
		}
		if field == naming.DynamicRange && hasDV && contains(list, "SDR") {
			values[field] = "DV"
			continue
		}
		if field == naming.Source {
			values[field] = strings.Join(list, " ")
			continue
		}
		if len(list) == 1 || field == naming.DynamicRange && !contains(list, "SDR") {
			values[field] = strings.Join(list, " ")
		}
	}
	return values
}

func appendUnique(values []string, value string) []string {
	if value == "" || contains(values, value) {
		return values
	}
	return append(values, value)
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func subtitleLanguages(languages []string) string {
	var label strings.Builder
	single := ""
	count := 0
	for _, language := range [][3]string{{"zh-Hans", "简", "简体"}, {"zh-Hant", "繁", "繁体"}, {"ja", "日", "日语"}, {"en", "英", "英语"}} {
		if slices.Contains(languages, language[0]) {
			label.WriteString(language[1])
			single = language[2]
			count++
		}
	}
	if count == 1 {
		return single
	}
	return label.String()
}

// InheritSubtitles is only for a known immediate directory with one video.
func InheritSubtitles(values map[naming.Field]string, directory string) {
	if values[naming.Languages] != "" {
		return
	}
	parent := Values(directory, releaseparse.Spec{}, "")
	if parent[naming.Languages] == "" {
		return
	}
	values[naming.Languages] = parent[naming.Languages]
	if values[naming.SubtitleMode] == "" {
		values[naming.SubtitleMode] = parent[naming.SubtitleMode]
	}
}
