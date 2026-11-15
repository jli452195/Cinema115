package recognition

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"
)

// matcher 将普通名称集中索引，只有包含实际正则结构的规则才逐条执行正则。
// 所有索引只在编译时构造，之后可由多个文件和任务并发只读使用。
type matcher struct {
	folded, sensitive literalTrie
	patterns          []patternRule
	requiredFolded    literalTrie
	requiredSensitive literalTrie
}

type patternRule struct {
	index    int
	re       *regexp.Regexp
	filtered bool
}

type literalNode struct {
	next  map[rune]int
	rules []int
}

type literalTrie struct {
	nodes []literalNode
}

func compileMatcher(kind string, rules []rule) (matcher, error) {
	m := matcher{patterns: make([]patternRule, 0, len(rules))}
	for i, r := range rules {
		parsed, err := syntax.Parse(r.re.String(), syntax.Perl)
		if err != nil {
			return matcher{}, &Diagnostic{Kind: kind, Line: r.line, Column: 1, Message: "无法解析识别规则：" + err.Error()}
		}
		for parsed.Op == syntax.OpCapture {
			parsed = parsed.Sub[0]
		}
		if parsed.Op != syntax.OpLiteral {
			// 左边界参与正则匹配，避免非法起点吞掉后面的合法组名；锚点仍相对完整输入。
			// 词表捕获组不参与输出，去掉内部捕获，限制每次命中索引的分配量。
			scan, err := regexp.Compile(`(?:\A|[^\p{L}\p{N}\p{M}])(` + withoutCaptures(parsed).String() + `)`)
			if err != nil {
				return matcher{}, &Diagnostic{Kind: kind, Line: r.line, Column: 1, Message: "无法编译完整名称规则：" + err.Error()}
			}
			scan.Longest()
			// 固定文字只用于排除不可能命中的规则，最终仍使用原正则扫描完整名称。
			// 不把有限分支直接当成独立名称，保留最长匹配、非重叠和锚点的原有语义。
			hints := requiredLiterals(parsed)
			filtered := literalHintScore(hints) >= 2
			if filtered {
				for _, hint := range hints {
					if hint.fold {
						m.requiredFolded.add(hint.runes, len(m.patterns), true)
					} else {
						m.requiredSensitive.add(hint.runes, len(m.patterns), false)
					}
				}
			}
			m.patterns = append(m.patterns, patternRule{index: i, re: scan, filtered: filtered})
			continue
		}
		if parsed.Flags&syntax.FoldCase != 0 {
			m.folded.add(parsed.Rune, i, true)
		} else {
			m.sensitive.add(parsed.Rune, i, false)
		}
	}
	return m, nil
}

func withoutCaptures(parsed *syntax.Regexp) *syntax.Regexp {
	if parsed.Op == syntax.OpCapture {
		return withoutCaptures(parsed.Sub[0])
	}
	for i, sub := range parsed.Sub {
		parsed.Sub[i] = withoutCaptures(sub)
	}
	return parsed
}

func (t *literalTrie) add(value []rune, index int, fold bool) {
	if len(t.nodes) == 0 {
		t.nodes = make([]literalNode, 1)
	}
	node := 0
	for _, r := range value {
		if fold {
			r = foldRune(r)
		}
		if t.nodes[node].next == nil {
			t.nodes[node].next = make(map[rune]int)
		}
		next, ok := t.nodes[node].next[r]
		if !ok {
			next = len(t.nodes)
			t.nodes[node].next[r] = next
			t.nodes = append(t.nodes, literalNode{})
		}
		node = next
	}
	t.nodes[node].rules = append(t.nodes[node].rules, index)
}

// foldRune 与 Go 正则使用相同的 Unicode SimpleFold 等价类，不改写原文或字节偏移。
func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if r >= 'a' && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	minimum := r
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		minimum = min(minimum, next)
	}
	return minimum
}

func (m matcher) find(kind, name string, rules []rule, out []Match) ([]Match, error) {
	// 候选位图只属于本次调用，索引保持只读；无需共享缓存或逐规则分配布尔切片。
	var candidates [(MaxRules + 63) / 64]uint64
	for start := range name {
		if boundary(name, start, true) {
			var err error
			out, err = m.folded.find(kind, name, start, rules, out, true)
			if err != nil {
				return nil, err
			}
			out, err = m.sensitive.find(kind, name, start, rules, out, false)
			if err != nil {
				return nil, err
			}
		}
		// 必含文字可能位于组名内部，预筛选不能要求它自身处在词边界。
		m.requiredFolded.markCandidates(name, start, &candidates, true)
		m.requiredSensitive.markCandidates(name, start, &candidates, false)
	}
	for index, pattern := range m.patterns {
		if pattern.filtered && candidates[index/64]&(uint64(1)<<uint(index%64)) == 0 {
			continue
		}
		r := rules[pattern.index]
		for _, span := range pattern.re.FindAllStringSubmatchIndex(name, -1) {
			var err error
			out, err = appendMatch(out, kind, name, span[2], span[3], r)
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (t literalTrie) markCandidates(name string, start int, candidates *[(MaxRules + 63) / 64]uint64, fold bool) {
	if len(t.nodes) == 0 {
		return
	}
	node := 0
	for end := start; end < len(name); {
		r, size := utf8.DecodeRuneInString(name[end:])
		if fold {
			r = foldRune(r)
		}
		next, ok := t.nodes[node].next[r]
		if !ok {
			return
		}
		node = next
		end += size
		for _, index := range t.nodes[node].rules {
			candidates[index/64] |= uint64(1) << uint(index%64)
		}
	}
}

func (t literalTrie) find(kind, name string, start int, rules []rule, out []Match, fold bool) ([]Match, error) {
	if len(t.nodes) == 0 {
		return out, nil
	}
	node := 0
	for end := start; end < len(name); {
		r, size := utf8.DecodeRuneInString(name[end:])
		if fold {
			r = foldRune(r)
		}
		next, ok := t.nodes[node].next[r]
		if !ok {
			break
		}
		node = next
		end += size
		for _, index := range t.nodes[node].rules {
			var err error
			out, err = appendMatch(out, kind, name, start, end, rules[index])
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func appendMatch(out []Match, kind, name string, start, end int, r rule) ([]Match, error) {
	if start == end || !boundary(name, start, true) || !boundary(name, end, false) {
		return out, nil
	}
	raw := name[start:end]
	if shortASCIICode(raw) {
		if kind == "platforms" && !platformContext(name, start, end) || kind == "groups" && !shortGroupContext(name, start, end) {
			return out, nil
		}
	}
	if len(out) >= MaxMatches {
		return nil, fmt.Errorf("%w：单个名称命中规则超过 %d 处，请缩小词表规则或测试名称", ErrInput, MaxMatches)
	}
	value := raw
	if r.output != "" {
		value = r.output
	}
	return append(out, Match{Kind: kind, Line: r.line, Raw: raw, Value: value, Start: start, End: end, Mapped: r.output != ""}), nil
}

// 短英文名容易与片名撞词，要求独立输入、发布分隔符、标签或附近规格作为证据。
func shortGroupContext(name string, start, end int) bool {
	before, after := strings.TrimSpace(name[:start]), strings.TrimSpace(name[end:])
	if before == "" && after == "" {
		return true
	}
	if before != "" {
		r, _ := utf8.DecodeLastRuneInString(before)
		if strings.ContainsRune("-@＠&＆+＋·，", r) {
			return true
		}
	}
	if after != "" {
		r, _ := utf8.DecodeRuneInString(after)
		if strings.ContainsRune("-@＠&＆+＋·，", r) {
			return true
		}
	}
	return platformContext(name, start, end)
}
