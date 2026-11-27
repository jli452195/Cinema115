// Package mediafacts extracts explicit filename tags without network or stream reads.
package mediafacts

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

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
	if len(result.ReleaseGroups) > 0 {
		values[naming.ReleaseGroup] = strings.Join(result.ReleaseGroups, "&")
	}
	if len(result.Platforms) == 1 {
		values[naming.Platform] = result.Platforms[0]
	} else if len(result.Platforms) > 1 {
		values[naming.Platform] = ""
	}
	return values, nil
}

const mediaAudioTokens = `true[ ._-]?hd|dts[ ._-]?hd[ ._-]?(?:ma|hra?)|dts[ :._-]?x|dts|dd\+|ddp|dd|e[ ._-]?ac[ ._-]?3|ac[ ._-]?3|aac|flac|opus|atmos`
const mediaChannelTokens = `[12567]\.1|2\.0`

var mediaFactTokens = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(web[ ._-]?dl|web[ ._-]?rip|blu[ ._-]?ray|bdrip|remux|hdtv|dvdrip|nf|netflix|amzn|dsnp|atvp|hmax|bilibili|h[ ._-]?26[45]|x26[45]|avc|hevc|av1|vp9|(?:8|10|12)[ ._-]?(?:bit|b)|(?:profile[ ._-]?|p)(?:[57]|8(?:[ ._-]?[14])?)|fel|mel|el|rpu|hdr10(?:\+|[ ._-]?plus)?|dolby[ ._-]?vision|dovi|dv|hdr|hlg|sdr|progressive|interlaced|cfr|vfr|(?:` + mediaAudioTokens + `)(` + mediaChannelTokens + `)?|` + mediaChannelTokens + `|(?:[0-9]{1,2}(?:\.[0-9]+)?(?:fps|[pi])|[0-9]{2,3}(?:\.[0-9]+)?fps))`)
var mediaFactCanonical = strings.NewReplacer(" ", "", ".", "", "-", "", "_", "", ":", "")

// Values 仅提取明确的文件名与发布规格。缺失字段省略，冲突字段保留空值以阻止补缺。
func Values(name string, hint releaseparse.Spec, group string) map[naming.Field]string {
	return valuesWithPlatform(name, hint, group, true)
}

func valuesWithPlatform(name string, hint releaseparse.Spec, group string, includePlatform bool) map[naming.Field]string {
	values := make(map[naming.Field]string, 16)
	if resolution, conflict := resolutionFact(name); resolution != "" || conflict {
		values[naming.Resolution] = resolution
	} else if hint.Resolution != "" {
		values[naming.Resolution] = hint.Resolution
	}
	if group != "" {
		values[naming.ReleaseGroup] = group
	}
	language, mode, subtitleConflict := videoSubtitleValues(name, hint)
	if language != "" {
		values[naming.Languages] = language
	}
	if mode != "" || subtitleConflict {
		values[naming.SubtitleMode] = mode
	}
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
	switch strings.ToLower(path.Ext(name)) {
	case ".strm", ".mkv", ".mp4", ".m2ts", ".ts", ".webm", ".mov", ".iso":
		name = strings.TrimSuffix(name, path.Ext(name))
	}
	lastAudioEnd := -1
	for _, index := range mediaFactTokens.FindAllStringSubmatchIndex(name, -1) {
		if end := index[1]; end < len(name) && (name[end] >= 'a' && name[end] <= 'z' || name[end] >= 'A' && name[end] <= 'Z' || name[end] >= '0' && name[end] <= '9') {
			continue
		}
		tokenEnd := index[3]
		if index[4] >= 0 {
			tokenEnd = index[4]
			tags[naming.Channels] = appendUnique(tags[naming.Channels], name[index[4]:index[5]])
		}
		raw := strings.ToLower(name[index[2]:tokenEnd])
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
		case "profile5", "profile7", "profile8", "p5", "p7", "p8":
			field, value = naming.DVProfile, "P"+key[len(key)-1:]
		case "profile81", "profile84", "p81", "p84":
			field, value = naming.DVProfile, "P8."+key[len(key)-1:]
		case "el", "fel", "mel":
			field, value = naming.DVLayer, strings.ToUpper(key)
		case "rpu":
			field, value = naming.DVMetadata, "RPU"
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
		case "aac", "flac", "opus", "dts", "dtshdma", "dtshdhr", "dtshdhra", "truehd", "dd+", "ddp", "eac3", "dd", "ac3":
			field = naming.AudioCodec
			value = strings.ToUpper(key)
			if key == "eac3" || key == "dd+" {
				value = "DDP"
			}
			if key == "ac3" {
				value = "DD"
			}
			if key == "dtshdhr" {
				value = "DTSHDHRA"
			}
		case "atmos", "dtsx":
			field, value = naming.AudioEffect, map[string]string{"atmos": "Atmos", "dtsx": "DTS-X"}[key]
		case "20", "11", "21", "51", "61", "71":
			// 裸数字可能属于作品名或版本号，声道必须紧邻音频编码或制式。
			if lastAudioEnd >= 0 && strings.TrimFunc(name[lastAudioEnd:index[2]], func(r rune) bool {
				return unicode.IsSpace(r) || unicode.IsPunct(r)
			}) == "" {
				field, value = naming.Channels, raw
			}
		case "progressive", "interlaced":
			field, value = naming.ScanType, map[string]string{"progressive": "Progressive", "interlaced": "Interlaced"}[key]
		case "cfr", "vfr":
			field, value = naming.FrameRateMode, strings.ToUpper(key)
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
		if field == naming.AudioCodec || field == naming.AudioEffect {
			lastAudioEnd = index[3]
		}
		tags[field] = appendUnique(tags[field], value)
	}
	// Profile、增强层和 RPU 单独出现时只是线索，只有明确的 DV 标签才写入命名字段。
	ranges := tags[naming.DynamicRange]
	hasDV, hasSDR, hasHLG := contains(ranges, "DV"), contains(ranges, "SDR"), contains(ranges, "HLG")
	hasPQ := contains(ranges, "HDR10") || contains(ranges, "HDR10+")
	rangeConflict := hasHLG && hasPQ || hasSDR && (hasHLG || hasPQ || contains(ranges, "HDR"))
	if hasDV && rangeConflict {
		values[naming.HDRBaseLayer] = ""
	}
	for field, list := range tags {
		if field == naming.DVProfile || field == naming.DVLayer || field == naming.DVMetadata {
			if !hasDV {
				continue
			}
		}
		if field == naming.HDRBaseLayer && (!hasDV || rangeConflict) {
			continue
		}
		switch {
		case field == naming.DynamicRange && rangeConflict,
			field == naming.Source && len(tags[naming.SourceMedium]) > 1:
			values[field] = ""
		case field == naming.DynamicRange && hasDV && hasSDR:
			values[field] = "DV"
		case field == naming.DynamicRange || field == naming.Source:
			values[field] = strings.Join(list, " ")
		case len(list) == 1:
			values[field] = list[0]
		default:
			values[field] = ""
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

// InheritSubtitles 仅在已知直属目录只有一个视频时补充缺失的字幕声明。
func InheritSubtitles(values map[naming.Field]string, directory string) {
	if values[naming.Languages] != "" {
		return
	}
	parent := Values(directory, releaseparse.Spec{}, "")
	if parent[naming.Languages] == "" {
		return
	}
	values[naming.Languages] = parent[naming.Languages]
	if _, exists := values[naming.SubtitleMode]; !exists {
		if mode, present := parent[naming.SubtitleMode]; present {
			values[naming.SubtitleMode] = mode
		}
	}
}
