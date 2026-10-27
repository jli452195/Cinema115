package tguser

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
)

// ChannelMetadata is an unverified description from a post or ED2K filename.
// It is only for display, never episode mapping or download authorization.
type ChannelMetadata struct {
	Episode string   `json:"episode,omitempty"`
	Specs   []string `json:"specs,omitempty"`
	Size    string   `json:"size,omitempty"`
}

var (
	channelSeasonEpisode = regexp.MustCompile(`(?i)S[0-9]{1,2}\s*E[0-9]{1,5}(?:\.[0-9]{1,3})?(?:\s*[-~～至]\s*(?:S[0-9]{1,2}\s*)?E?[0-9]{1,5}(?:\.[0-9]{1,3})?)?`)
	channelQualityDot    = regexp.MustCompile(`(?i)\.((?:480|576|720|1080|1440|2160|4320)[pi])\b`)
	channelDeclaredSize  = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(TiB|GiB|MiB|KiB|TB|GB|MB|KB|B|T|G|M|K)\s*$`)
	channelTitlePrefix   = regexp.MustCompile(`^(?:影视|影視|剧集|劇集|电影|電影|名称|名稱|国产剧|國產劇|外语电影|外語電影|日番|番剧|番劇)\s*[:：]\s*`)
	channelExtraSpec     = regexp.MustCompile(`(?i)\bEDR\b|(?:简繁字幕|簡繁字幕|简体字幕|簡體字幕|繁体字幕|繁體字幕|中文字幕|中英字幕|中字|多音轨|多音軌|国语|國語|粤语|粵語|英语|英語|日语|日語)+`)
	channelDolbyVision   = regexp.MustCompile(`(?i)\bDolby\s*Vision\b`)
	channelSpecLine      = regexp.MustCompile(`(?i)^(?:热更\s+)?(?:[0-9]{3,4}[pi]|[48]k)\b`)
	channelLabelLine     = regexp.MustCompile(`^(季\s*集|集\s*数|集\s*數|质量|質量|画质|畫質|版本|规格|規格|字幕|音\s*轨|音\s*軌|大小|体\s*积|體\s*積|文件\s*大小)\s+(.+)$`)
)

func channelTextKey(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func channelRelevant(text string, links []ChannelLink, query string) bool {
	key := channelTextKey(query)
	if key == "" { // Punctuation-only titles have no meaningful local comparison.
		return true
	}
	// Links and tracking parameters are not evidence of a matching title.
	if strings.Contains(channelTextKey(channelURLPattern.ReplaceAllString(text, " ")), key) {
		return true
	}
	for _, link := range links {
		if name, _ := channelED2KFile(link); name != "" && strings.Contains(channelTextKey(name), key) {
			return true
		}
	}
	return false
}

func channelED2KFile(link ChannelLink) (string, string) {
	if link.Type != "ed2k" {
		return "", ""
	}
	parts := strings.Split(link.URL, "|") // Already validated by ParseCloudResource.
	if len(parts) < 6 {
		return "", ""
	}
	name, err := url.PathUnescape(parts[2]) // Unlike QueryUnescape, keeps literal '+'.
	if err != nil {
		name = parts[2]
	}
	bytes, err := strconv.ParseUint(parts[3], 10, 63)
	if err != nil {
		return name, ""
	}
	value, unit := float64(bytes), 0
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return name, strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 2, 64), "0"), ".") + " " + units[unit]
}

func channelEpisode(text string) string {
	text = channelQualityDot.ReplaceAllString(text, " $1")
	values := make([]string, 0, 2)
	for _, span := range channelSeasonEpisode.FindAllStringIndex(text, -1) {
		ascii := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }
		if span[0] > 0 && ascii(text[span[0]-1]) || span[1] < len(text) && ascii(text[span[1]]) {
			continue
		}
		value := strings.ToUpper(strings.Join(strings.Fields(text[span[0]:span[1]]), ""))
		if !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	if len(values) == 1 {
		return values[0]
	}
	if len(values) > 1 {
		return "" // Different episodes are not a single file's episode label.
	}
	parsed, err := releaseparse.Title(text)
	if err == nil && parsed.Kind == "episode" && parsed.Episode != nil {
		return "第" + strconv.Itoa(*parsed.Episode) + "集"
	}
	if err == nil && (parsed.Kind == "batch" || parsed.Kind == "special") {
		return parsed.RawEpisode
	}
	return ""
}

func channelMetadata(title, text string, links []ChannelLink) *ChannelMetadata {
	// Only explicit header/quality fields participate, never synopsis or adverts.
	episodeText, specs, size := title, title, ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimLeftFunc(strings.TrimSpace(line), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		label, value, found := strings.Cut(strings.ReplaceAll(line, "：", ":"), ":")
		if !found {
			if match := channelLabelLine.FindStringSubmatch(line); len(match) == 3 {
				label, value, found = match[1], match[2], true
			}
		}
		if !found {
			if channelSpecLine.MatchString(line) {
				specs += "\n[" + line + "]"
			}
			continue
		}
		switch strings.Join(strings.Fields(label), "") {
		case "季集", "集数", "集數":
			episodeText += "\n" + value
		case "质量", "質量", "画质", "畫質", "版本", "规格", "規格", "字幕", "音轨", "音軌":
			specs += "\n[" + value + "]"
		case "大小", "体积", "體積", "文件大小":
			if match := channelDeclaredSize.FindStringSubmatch(value); match != nil {
				unit := strings.ToUpper(match[2])
				if len(unit) == 1 && unit != "B" {
					unit += "B"
				}
				if strings.HasSuffix(unit, "IB") {
					unit = unit[:1] + "iB"
				}
				size = match[1] + " " + unit
			}
		}
	}
	for i := range links {
		name, fileSize := channelED2KFile(links[i])
		facts := make(map[naming.Field]string)
		episode := ""
		if len(links) == 1 {
			combined := specs
			if name != "" {
				combined = name + "\n[" + specs + "]"
			}
			facts = channelFacts(combined)
			fileEpisode, postEpisode := channelEpisode(name), channelEpisode(episodeText)
			switch {
			case fileEpisode != "" && postEpisode != "" && fileEpisode != postEpisode:
				// Conflicting numbering is deliberately left unknown.
			case name != "" && channelSeasonEpisode.MatchString(name) && fileEpisode == "":
				// Multiple season/episode expressions in a filename are
				// ambiguous; do not fall back to the post's single number.
			case fileEpisode != "":
				episode = fileEpisode
			default:
				episode = postEpisode
			}
			if fileSize == "" {
				fileSize = size
			}
		} else if name != "" {
			facts, episode = channelFacts(name), channelEpisode(name)
		} else if links[i].Type == "115" {
			// A 115 URL has no filename; keep explicit post fields on its
			// own link instead of lifting them to a multi-link card.
			facts, episode, fileSize = channelFacts(specs), channelEpisode(episodeText), size
		}
		links[i].Metadata = channelFactsLabel(facts, episode, fileSize)
	}
	if len(links) == 1 {
		metadata := links[0].Metadata
		links[0].Metadata = nil // One copy at card level; different files stay on their own rows.
		return metadata
	}
	return nil
}

func channelFacts(text string) map[naming.Field]string {
	text = channelDolbyVision.ReplaceAllString(text, "DoVi")
	// Values performs the shared title/spec scan once and also extracts the
	// filename quality tokens; avoid a second independent parse here.
	values := mediafacts.Values(text, releaseparse.Spec{}, "")
	// Retain explicit channel terms that the shared filename parser does not know.
	for _, span := range channelExtraSpec.FindAllStringIndex(text, -1) {
		before, _ := utf8.DecodeLastRuneInString(text[:span[0]])
		after, _ := utf8.DecodeRuneInString(text[span[1]:])
		if unicode.IsLetter(before) || unicode.IsDigit(before) || unicode.IsLetter(after) || unicode.IsDigit(after) {
			continue
		}
		value := text[span[0]:span[1]]
		if strings.EqualFold(value, "edr") {
			if values[naming.DynamicRange] == "" {
				values[naming.DynamicRange] = "EDR"
			}
		} else if !strings.Contains(values[naming.Languages], value) {
			values[naming.Languages] = strings.TrimSpace(values[naming.Languages] + " " + value)
		}
	}
	return values
}

func channelFactsLabel(values map[naming.Field]string, episode, size string) *ChannelMetadata {
	specs := make([]string, 0, 8)
	for _, field := range []naming.Field{naming.Resolution, naming.DynamicRange, naming.Source, naming.VideoCodec, naming.BitDepth, naming.AudioCodec, naming.Languages, naming.SubtitleMode} {
		value := values[field]
		if field == naming.AudioCodec {
			value = strings.TrimSpace(value + " " + values[naming.Channels] + " " + values[naming.AudioEffect])
		}
		if value != "" {
			specs = append(specs, value)
		}
	}
	if episode == "" && len(specs) == 0 && size == "" {
		return nil
	}
	return &ChannelMetadata{Episode: episode, Specs: specs, Size: size}
}

func channelDisplayTitle(title string, metadata *ChannelMetadata) string {
	title = strings.TrimLeftFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("[【(（", r)
	})
	title = channelTitlePrefix.ReplaceAllString(title, "")
	if metadata != nil && metadata.Episode != "" {
		if trimmed := strings.TrimSpace(strings.TrimSuffix(title, metadata.Episode)); trimmed != "" {
			title = trimmed
		}
	}
	return title
}
