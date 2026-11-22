package mediafacts

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"strm115/backend/internal/releaseparse"
	"strm115/backend/internal/utils/subtitle"
)

// SubtitleVideo describes every video in the listing, including unselected ones.
type SubtitleVideo struct{ ID, ParentID, Name string }

type subtitleIdentity struct {
	key, version, tail string
	episode            int
	spec               releaseparse.Spec
}

type subtitleCandidate struct {
	SubtitleVideo
	identity *subtitleIdentity
	parsed   bool
}

// SubtitleMatcher 复用清单内的文件名索引；视频事实按需解析，实例不跨协程共享。
type SubtitleMatcher struct {
	videos map[string][]*subtitleCandidate
	stems  map[string][]*subtitleCandidate
}

func NewSubtitleMatcher(videos []SubtitleVideo) *SubtitleMatcher {
	m := &SubtitleMatcher{videos: make(map[string][]*subtitleCandidate, len(videos)), stems: make(map[string][]*subtitleCandidate, len(videos))}
	for _, video := range videos {
		candidate := &subtitleCandidate{SubtitleVideo: video}
		m.videos[video.ParentID] = append(m.videos[video.ParentID], candidate)
		stem := subtitleFold(strings.TrimSuffix(video.Name, path.Ext(video.Name)))
		m.stems[stem] = append(m.stems[stem], candidate)
	}
	return m
}

// 与 EqualFold 一致地归一化 Unicode 大小写，包含希腊尾字母等特殊折叠。
func subtitleFold(value string) string {
	return strings.Map(func(r rune) rune {
		folded := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < folded {
				folded = next
			}
		}
		return folded
	}, value)
}

// StemCandidates 按点号边界从长到短查完整视频文件名，保留同名文件的歧义。
func (m *SubtitleMatcher) StemCandidates(parentID, name string, sameDirectory bool) ([]string, string) {
	return m.stemCandidates(parentID, name, sameDirectory, false)
}

// TaggedStemCandidates 仅在同目录使用完整主干和有标签依据的后缀匹配字幕。
func (m *SubtitleMatcher) TaggedStemCandidates(parentID, name string) ([]string, string) {
	return m.stemCandidates(parentID, name, true, true)
}

func (m *SubtitleMatcher) stemCandidates(parentID, name string, sameDirectory, taggedOnly bool) ([]string, string) {
	stem := strings.TrimSuffix(name, path.Ext(name))
	for end := len(stem); end >= 0; end = strings.LastIndexByte(stem[:end], '.') {
		var owners []string
		for _, video := range m.stems[subtitleFold(stem[:end])] {
			if !sameDirectory || video.ParentID == parentID {
				owners = append(owners, video.ID)
			}
		}
		if len(owners) > 0 {
			if taggedOnly && stem[end:] != "" && !subtitle.KnownTail(stem[end:]) {
				return nil, ""
			}
			return owners, subtitle.NormalizeTail(stem[end:])
		}
	}
	// Ani-One 的音轨与字幕说明不同，仅在同目录按完整作品及集号原文匹配。
	sub := aniOneSubtitleStem.FindStringSubmatch(stem)
	if sub == nil || sub[2] == "日語原聲" {
		return nil, ""
	}
	var owners []string
	for _, video := range m.videos[parentID] {
		name := strings.TrimSuffix(video.Name, path.Ext(video.Name))
		v := aniOneSubtitleStem.FindStringSubmatch(name)
		if v != nil && v[2] == "日語原聲" && strings.EqualFold(v[1], sub[1]) {
			owners = append(owners, video.ID)
		}
	}
	if len(owners) > 0 {
		tails := map[string]string{"簡中字幕": ".zh-CN", "繁中字幕": ".zh-TW", "日文自動產生字幕": ".ja"}
		return owners, tails[sub[2]]
	}
	return nil, ""
}

// Only complete, known metadata fields may be removed. Title/episode/group and
// unknown edition tags stay part of identity; this is not fuzzy title matching.
const subtitleMetadataToken = `(?:[0-9]{3,4}[pi]|[0-9]{3,4}\s*[x×]\s*[0-9]{3,4}(?:[pi])?|[48]k|h[ ._-]?26[45]|x26[45]|avc|hevc|av1|vp9|(?:8|10|12)[ -]?bit|mkv|mp4|chs|cht|sc|tc|gb|big5|ja|jpn|eng|简体(?:中文)?|簡體(?:中文)?|繁体(?:中文)?|繁體(?:中文)?|简繁|簡繁|简|繁|内嵌|內嵌|内封|內封|外挂|外掛|forced|sdh|default)`

var (
	subtitleMetadata   = regexp.MustCompile(`(?i)^` + subtitleMetadataToken + `(?:[ ._+&/-]+` + subtitleMetadataToken + `)*$`)
	subtitleBrackets   = regexp.MustCompile(`[\[【(（]([^\[\]【】()（）]+)[\]】)）]`)
	aniOneSubtitleStem = regexp.MustCompile(`^(.+?)\s*\((日語原聲|簡中字幕|繁中字幕|日文自動產生字幕)\)【Ani-One】$`)
)

func subtitleFileIdentity(name string, isSubtitle bool) *subtitleIdentity {
	stem := strings.TrimSuffix(name, path.Ext(name))
	episodeStem := stem
	if isSubtitle {
		// 裸集号解析前跳过已知双语尾标记，原标记仍参与字幕语言归一。
		if dot := strings.LastIndexByte(stem, '.'); dot >= 0 && subtitle.IsBilingualAlias(stem[dot+1:]) {
			episodeStem = stem[:dot]
		}
	}
	parsed, err := releaseparse.Title(episodeStem)
	if err != nil || parsed.Kind != "episode" || parsed.Episode == nil || parsed.SpecAmbiguous {
		return nil
	}
	parsed.Spec.Resolution = resolution(stem)
	if resourceQuality.MatchString(stem) && parsed.Spec.Resolution == "" {
		return nil // Conflicting explicit qualities must not become unspecified.
	}
	groups := make([]string, 0, 3)
	metadata := func(tag string) bool {
		if isSubtitle && subtitle.KnownTag(tag) {
			groups = append(groups, tag)
			return true
		}
		if !subtitleMetadata.MatchString(strings.TrimSpace(tag)) {
			return false
		}
		if isSubtitle {
			for _, field := range strings.FieldsFunc(tag, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("._+&/-", r) }) {
				if subtitle.KnownTag(field) {
					groups = append(groups, field)
				}
			}
		}
		return true
	}
	core := subtitleBrackets.ReplaceAllStringFunc(stem, func(block string) string {
		parts := subtitleBrackets.FindStringSubmatch(block)
		if metadata(parts[1]) {
			return " "
		}
		return block
	})
	for {
		end := strings.LastIndexByte(core, '.')
		if isSubtitle && end >= 3 && strings.EqualFold(core[end-3:end], ".zh") && subtitle.KnownTag(core[end-2:]) {
			end -= 3
		}
		if end < 0 {
			break
		}
		tag := strings.TrimSpace(core[end+1:])
		if tag != "" && !metadata(tag) {
			// 只移除视频末尾的规范字幕字段，保留作品名和版本里的正常单词。
			unwrapped := strings.TrimSuffix(strings.TrimPrefix(tag, "["), "]")
			if isSubtitle || !videoSubtitleContext.MatchString(core[:end]) || !videoSubtitleCodeTag.MatchString(unwrapped) && videoSubtitleModeValue(tag) == "" {
				break
			}
		}
		core = core[:end]
	}
	fields := strings.FieldsFunc(strings.ToLower(core), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("[]【】()（）._-!！?？", r) })
	for len(fields) > 0 && metadata(fields[len(fields)-1]) {
		fields = fields[:len(fields)-1]
	}
	if len(fields) < 2 {
		return nil // A bare episode number cannot establish the work's identity.
	}
	for i, field := range fields {
		if field == strings.ToLower(parsed.RawEpisode) {
			fields[i] = strconv.Itoa(*parsed.Episode)
		}
	}
	tail := ""
	if isSubtitle {
		tail = subtitle.InferredTail(groups, parsed.Spec.Languages)
	}
	return &subtitleIdentity{key: strings.Join(fields, " "), version: parsed.VersionLabel, episode: *parsed.Episode, spec: parsed.Spec, tail: tail}
}

// Candidates includes unselected videos so callers can retain ambiguous subtitles.
// A subtitle may be assigned only when exactly one candidate remains.
func (m *SubtitleMatcher) Candidates(parentID, name string) ([]string, string) {
	sub := subtitleFileIdentity(name, true)
	if sub == nil {
		return nil, ""
	}
	var owners []string
	for _, video := range m.videos[parentID] {
		if !video.parsed {
			video.identity, video.parsed = subtitleFileIdentity(video.Name, false), true
		}
		v := video.identity
		if v == nil || v.key != sub.key || v.episode != sub.episode || v.version != sub.version {
			continue
		}
		compatible := true
		for _, pair := range [][2]string{{sub.spec.Resolution, v.spec.Resolution}, {sub.spec.Codec, v.spec.Codec}, {sub.spec.BitDepth, v.spec.BitDepth}, {sub.spec.Container, strings.ToLower(strings.TrimPrefix(path.Ext(video.Name), "."))}} {
			if pair[0] != "" && pair[0] != pair[1] {
				compatible = false
			}
		}
		if !compatible {
			continue
		}
		owners = append(owners, video.ID)
	}
	if len(owners) == 0 {
		return nil, ""
	}
	return owners, sub.tail
}
