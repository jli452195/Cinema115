// Package mediafacts extracts explicit filename tags without network or stream reads.
package mediafacts

import (
	"regexp"
	"slices"
	"strings"

	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
)

var mediaFactTokens = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(web[ .-]?dl|webrip|blu[ .-]?ray|bdrip|remux|hdtv|dvdrip|nf|netflix|amzn|dsnp|atvp|hmax|h[ .-]?26[45]|x26[45]|hevc|avc|av1|vp9|(?:8|10|12)[ .-]?bit|hdr10\+|hdr10|dovi|dv|hdr|hlg|sdr|truehd|dts[ .-]?hd[ .-]?ma|dts[ .-]?x|dts|ddp|dd|e[ .-]?ac[ .-]?3|ac[ .-]?3|aac|flac|opus|atmos|[12567]\.1|2\.0|[0-9]{2,3}(?:\.[0-9]+)?fps)`)
var mediaFactCanonical = strings.NewReplacer(" ", "", ".", "", "-", "")
var mediaJoinedChannels = regexp.MustCompile(`(?i)(truehd|dts|ddp|dd|eac3|ac3|aac|flac|opus)([12567]\.1|2\.0)`)

// Only explicit filename/source tags are used. Unknown and conflicting scalar
// tags stay empty; this is unrelated to RSS matching and never reads streams.
func Values(name string, hint model.AnimeSpec, group string) map[naming.Field]string {
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
	tags := make(map[naming.Field][]string, 10)
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
		case "nf", "netflix", "amzn", "dsnp", "atvp", "hmax":
			field = naming.Platform
			value = strings.ToUpper(key)
			if key == "netflix" {
				value = "NF"
			}
		case "h264", "x264", "avc":
			field, value = naming.VideoCodec, "H264"
		case "h265", "x265", "hevc":
			field, value = naming.VideoCodec, "HEVC"
		case "av1", "vp9":
			field, value = naming.VideoCodec, strings.ToUpper(key)
		case "8bit", "10bit", "12bit":
			field, value = naming.BitDepth, key
		case "dv", "dovi":
			field, value = naming.DynamicRange, "DV"
		case "hdr10+", "hdr10", "hdr", "hlg", "sdr":
			field, value = naming.DynamicRange, strings.ToUpper(key)
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
		default:
			if strings.HasSuffix(key, "fps") {
				field, value = naming.FrameRate, raw
			}
		}
		if field == "" {
			continue
		}
		found := false
		for _, old := range tags[field] {
			found = found || old == value
		}
		if !found {
			tags[field] = append(tags[field], value)
		}
	}
	for field, list := range tags {
		if len(list) == 1 || field == naming.DynamicRange && !strings.Contains(strings.Join(list, " "), "SDR") {
			values[field] = strings.Join(list, " ")
		}
	}
	return values
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
	parent := Values(directory, model.AnimeSpec{}, "")
	if parent[naming.Languages] == "" {
		return
	}
	values[naming.Languages] = parent[naming.Languages]
	if values[naming.SubtitleMode] == "" {
		values[naming.SubtitleMode] = parent[naming.SubtitleMode]
	}
}
