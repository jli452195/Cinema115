package subtitle

import (
	_ "embed"
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"unicode"
)

//go:embed subtitleNamingRules.json
var ruleData []byte

type tagRule struct{ language, tag string }

type namingRules struct {
	aliases       map[string]tagRule
	needsLanguage map[string]bool
}

var rules = loadRules()

func loadRules() namingRules {
	var data struct {
		Languages              map[string][]string
		Bilingual              map[string]map[string][]string
		Variants, Flags        map[string][]string
		FlagsRequiringLanguage []string
	}
	if err := json.Unmarshal(ruleData, &data); err != nil {
		panic(err) // 构建时嵌入的规则由前后端一致性检查覆盖。
	}
	result := namingRules{aliases: make(map[string]tagRule, 64), needsLanguage: make(map[string]bool, len(data.FlagsRequiringLanguage))}
	for language, aliases := range data.Languages {
		for _, alias := range aliases {
			result.aliases[alias] = tagRule{language: language}
		}
	}
	for tag, languages := range data.Bilingual {
		for language, aliases := range languages {
			for _, alias := range aliases {
				result.aliases[alias] = tagRule{language: language, tag: tag}
			}
		}
	}
	for _, tags := range []map[string][]string{data.Variants, data.Flags} {
		for tag, aliases := range tags {
			for _, alias := range aliases {
				result.aliases[alias] = tagRule{tag: tag}
			}
		}
	}
	for _, alias := range data.FlagsRequiringLanguage {
		result.needsLanguage[alias] = true
	}
	return result
}

var (
	audioBlock    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:AAC|FLAC|ALAC|AC-?3|E-?AC-?3|DDP?|DTS(?:-HD)?|TRUEHD|OPUS|AUDIO)[ ._-]*\[[^\[\]]*\]`)
	subtitleBlock = regexp.MustCompile(`(?i)(^|[^a-z0-9])SUB(?:TITLE)?S?[ ._:-]*\[([^\[\]]*)\]`)
	bracketBlock  = regexp.MustCompile(`[\[【(（]([^\[\]【】()（）]+)[\]】)）]`)
	chineseAlias  = regexp.MustCompile(`(?i)\bzh[._](cn|tw|hk|sg|hans|hant|en)\b`)
)

type metadata struct {
	Language string
	Variants []string
	Flags    []string
}

type evidence struct {
	languages             map[string]bool
	tags                  map[string]bool
	hi, explicitBilingual bool
}

func newEvidence() evidence {
	return evidence{languages: make(map[string]bool, 3), tags: make(map[string]bool, 6)}
}

func metadataTokens(group string) []string {
	group = strings.ToLower(group)
	if _, known := rules.aliases[group]; known {
		return []string{group}
	}
	if strings.ContainsAny(group, "._") {
		group = chineseAlias.ReplaceAllString(group, "zh-$1")
	}
	tokens := strings.FieldsFunc(group, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("._+&/", r) })
	for _, token := range tokens {
		if _, known := rules.aliases[token]; !known {
			return nil
		}
	}
	return tokens
}

// KnownTag 判断完整标签组是否由已知字幕元数据组成。
func KnownTag(group string) bool { return len(metadataTokens(group)) > 0 }

// IsBilingualAlias 判断单个标记是否是带语言的双语字幕别名。
func IsBilingualAlias(tag string) bool {
	rule := rules.aliases[strings.ToLower(tag)]
	return rule.language != "" && rule.tag != ""
}

// LanguageCodes 将完整字幕标签中的别名转换为语言代码，保留每种语言。
func LanguageCodes(group string) []string {
	tokens := metadataTokens(strings.TrimSpace(group))
	if len(tokens) == 0 {
		return nil
	}
	languages := make([]string, 0, len(tokens))
	for _, token := range tokens {
		rule := rules.aliases[token]
		if rule.language == "" {
			continue
		}
		languages = append(languages, rule.language)
		switch rule.tag {
		case "bilingual":
			languages = append(languages, "en")
		case "bilingual-ja":
			languages = append(languages, "ja")
		}
	}
	return languages
}

func (e *evidence) add(tokens []string) {
	for _, token := range tokens {
		rule := rules.aliases[token]
		if rule.language != "" {
			e.languages[rule.language] = true
		}
		if rules.needsLanguage[token] {
			e.hi = true
		} else if rule.tag != "" {
			e.tags[rule.tag] = true
		}
		if rule.tag == "bilingual" && token != "双语" && token != "雙語" {
			e.explicitBilingual = true
		}
	}
}

func (e evidence) language() string {
	var chinese, single string
	for language := range e.languages {
		single = language
		if strings.HasPrefix(language, "zh-") {
			if chinese != "" {
				return ""
			}
			chinese = language
		}
	}
	if chinese == "" && e.languages["zh"] {
		chinese = "zh"
	}
	if chinese != "" {
		if e.languages["en"] {
			e.tags["bilingual"] = true
		}
		if e.languages["ja"] {
			e.tags["bilingual-ja"] = true
		}
	}
	if e.tags["bilingual-ja"] && !e.languages["en"] && !e.explicitBilingual {
		delete(e.tags, "bilingual")
	}
	if len(e.languages) == 1 {
		return single
	}
	return chinese
}

func makeMetadata(language string, tags map[string]bool) metadata {
	result := metadata{Language: language}
	for _, variant := range []string{"bilingual", "bilingual-ja", "effects", "commentary", "lyrics"} {
		if tags[variant] {
			result.Variants = append(result.Variants, variant)
		}
	}
	for _, flag := range []string{"forced", "sdh", "default"} {
		if tags[flag] {
			result.Flags = append(result.Flags, flag)
		}
	}
	return result
}

func inspectSource(filename, selectedLanguage string) metadata {
	name := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	stem := audioBlock.ReplaceAllString(strings.TrimSuffix(name, path.Ext(name)), " ")
	explicit := subtitleBlock.FindAllStringSubmatch(stem, -1)
	stem = subtitleBlock.ReplaceAllString(stem, "$1 ")
	source := newEvidence()
	addAmbient := func(group string) {
		for _, token := range metadataTokens(group) {
			if len(explicit) == 0 || rules.aliases[token].language == "" {
				source.add([]string{token})
			}
		}
	}
	for _, match := range bracketBlock.FindAllStringSubmatch(stem, -1) {
		addAmbient(match[1])
	}
	stem = strings.TrimSpace(chineseAlias.ReplaceAllString(strings.ToLower(bracketBlock.ReplaceAllString(stem, " ")), "zh-$1"))
	parts := strings.Split(stem, ".")
	if len(parts) > 1 {
		parts = parts[1:]
	}
	for _, part := range parts {
		addAmbient(part)
	}
	end := strings.FieldsFunc(parts[len(parts)-1], func(r rune) bool { return unicode.IsSpace(r) || r == '_' })
	for i := len(end) - 1; i >= 0 && KnownTag(end[i]); i-- {
		addAmbient(end[i])
	}
	inferred := source.language()
	for i, match := range explicit {
		declared := newEvidence()
		declared.add(metadataTokens(match[2]))
		language := declared.language()
		if i == 0 {
			inferred = language
		} else if language != inferred {
			inferred = ""
		}
		for tag := range declared.tags {
			source.tags[tag] = true
		}
		source.hi = source.hi || declared.hi
	}
	if selectedLanguage == "zh-en" {
		selectedLanguage = "zh"
		if strings.HasPrefix(inferred, "zh-") {
			selectedLanguage = inferred
		}
		source.tags["bilingual"] = true
	}
	if source.hi && (inferred != "" || selectedLanguage != "") {
		source.tags["sdh"] = true
	}
	return makeMetadata(selectedLanguage, source.tags)
}

// InferredTail 供文件名匹配后的字幕使用，语言来自已识别的字幕标签。
func InferredTail(groups, legacyLanguages []string) string {
	facts := newEvidence()
	for _, group := range groups {
		facts.add(metadataTokens(group))
	}
	if len(facts.languages) == 0 {
		for _, language := range legacyLanguages {
			facts.add(metadataTokens(language))
			groups = append(groups, rules.aliases[strings.ToLower(language)].language)
		}
	}
	language := facts.language()
	if language != "" && facts.hi {
		facts.tags["sdh"] = true
	}
	if language == "" && len(facts.languages) > 1 {
		return "." + strings.Join(groups, ".")
	}
	if language == "" && facts.hi {
		return makeMetadata(language, facts.tags).tail("hi")
	}
	return makeMetadata(language, facts.tags).tail("")
}

func tailParts(value string) []string {
	if !strings.Contains(value, ".") {
		return []string{value}
	}
	positions := chineseAlias.FindAllStringIndex(value, -1)
	parts := make([]string, 0, strings.Count(value, ".")+1)
	for start, i, alias := 0, 0, 0; i <= len(value); i++ {
		if alias < len(positions) && i == positions[alias][0] {
			i = positions[alias][1] - 1
			alias++
			continue
		}
		if i == len(value) || value[i] == '.' {
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	return parts
}

// KnownTail 判断后缀是否全是可识别的字幕标签，供需要严格匹配的入口使用。
func KnownTail(tail string) bool {
	for _, part := range tailParts(strings.TrimPrefix(tail, ".")) {
		if !KnownTag(part) {
			return false
		}
	}
	return true
}

// NormalizeTail 只处理已确认属于该视频的字幕后缀，保留版本和无法解释的原始标签。
func NormalizeTail(tail string) string {
	if tail == "" {
		return ""
	}
	facts := newEvidence()
	var version []string
	for _, part := range tailParts(strings.TrimPrefix(tail, ".")) {
		tokens := metadataTokens(part)
		if len(tokens) == 0 || len(tokens) == 1 && rules.needsLanguage[tokens[0]] {
			if part != "" {
				version = append(version, part)
			}
			continue
		}
		facts.add(tokens)
	}
	language := facts.language()
	if language == "" && len(facts.languages) > 1 {
		return tail
	}
	if language != "" {
		if facts.hi {
			facts.tags["sdh"] = true
		}
		for i, part := range version {
			if rules.needsLanguage[strings.ToLower(strings.TrimSpace(part))] {
				facts.tags["sdh"] = true
				version[i] = ""
			}
		}
	} else if facts.hi {
		return tail
	}
	return makeMetadata(language, facts.tags).tail(strings.Join(nonempty(version), "."))
}

func nonempty(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (m metadata) tail(version string) string {
	var result strings.Builder
	for _, value := range []string{m.Language, version} {
		if value != "" {
			result.WriteByte('.')
			result.WriteString(value)
		}
	}
	for _, tags := range [][]string{m.Variants, m.Flags} {
		for _, tag := range tags {
			if !strings.EqualFold(tag, version) {
				result.WriteByte('.')
				result.WriteString(tag)
			}
		}
	}
	return result.String()
}
