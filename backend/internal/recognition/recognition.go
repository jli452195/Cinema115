// Package recognition 解析字幕组和来源平台词表，返回命中原文及可解释的规则证据。
package recognition

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

const (
	MaxTextBytes    = 256 << 10
	MaxNameBytes    = 8192
	MaxRules        = 2000
	MaxPatternBytes = 2048
	MaxMatches      = 8192
)

var ErrConfig = errors.New("识别词表无效")
var ErrInput = errors.New("识别名称无效")

type Diagnostic struct {
	Kind    string `json:"kind"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

func (e *Diagnostic) Error() string {
	return fmt.Sprintf("%s第 %d 行：%s", map[string]string{"groups": "字幕组词表", "platforms": "平台词表"}[e.Kind], e.Line, e.Message)
}
func (e *Diagnostic) Unwrap() error { return ErrConfig }

type Match struct {
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Raw    string `json:"raw"`
	Value  string `json:"value"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Mapped bool   `json:"mapped"`
}

type Conflict struct {
	Kind   string   `json:"kind"`
	Raw    string   `json:"raw"`
	Lines  []int    `json:"lines"`
	Values []string `json:"values"`
}

type Result struct {
	ReleaseGroups []string   `json:"releaseGroups"`
	Platforms     []string   `json:"platforms"`
	Matches       []Match    `json:"matches"`
	Conflicts     []Conflict `json:"conflicts"`
}

type rule struct {
	line   int
	output string
	re     *regexp.Regexp
}
type compiled struct {
	groups, platforms []rule
	groupMatcher      matcher
	platformMatcher   matcher
	groupsText        string
	platformsText     string
	revision          string
}

// Snapshot 的文本与版本可随任务持久化；已编译规则只在内存中共享且不再修改。
type Snapshot struct {
	Groups    string `json:"groups"`
	Platforms string `json:"platforms"`
	Revision  string `json:"revision"`
	compiled  *compiled
}

func Revision(groups, platforms string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%d:%s", len(groups), groups, len(platforms), platforms)))
	return hex.EncodeToString(sum[:])
}

func Compile(groups, platforms string) (Snapshot, error) {
	g, err := parseRules("groups", groups)
	if err != nil {
		return Snapshot{}, err
	}
	p, err := parseRules("platforms", platforms)
	if err != nil {
		return Snapshot{}, err
	}
	groupMatcher, err := compileMatcher("groups", g)
	if err != nil {
		return Snapshot{}, err
	}
	platformMatcher, err := compileMatcher("platforms", p)
	if err != nil {
		return Snapshot{}, err
	}
	revision := Revision(groups, platforms)
	return Snapshot{Groups: groups, Platforms: platforms, Revision: revision, compiled: &compiled{
		groups: g, platforms: p, groupMatcher: groupMatcher, platformMatcher: platformMatcher,
		groupsText: groups, platformsText: platforms, revision: revision,
	}}, nil
}

func parseRules(kind, source string) ([]rule, error) {
	fail := func(line int, message string) error {
		return &Diagnostic{Kind: kind, Line: line, Column: 1, Message: message}
	}
	if len(source) > MaxTextBytes {
		return nil, fail(1, "词表不能超过 256 KiB")
	}
	if !utf8.ValidString(source) {
		return nil, fail(1, "词表须使用 UTF-8 编码")
	}
	lines := strings.Split(strings.TrimPrefix(source, "\ufeff"), "\n")
	out := make([]rule, 0, min(len(lines), MaxRules))
	for i, original := range lines {
		line := strings.TrimSpace(original)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(out) >= MaxRules {
			return nil, fail(i+1, "规则数量不能超过 2000 条")
		}
		output, pattern := "", line
		if left, right, ok := strings.Cut(line, "~="); ok {
			output, pattern = strings.TrimSpace(left), strings.TrimSpace(right)
			if output == "" || len(output) > 256 || strings.ContainsAny(output, "\x00\r\n/\\") {
				return nil, fail(i+1, "映射名称须为 1～256 字节的普通名称，不能包含路径分隔符")
			}
		}
		if pattern == "" || len(pattern) > MaxPatternBytes {
			return nil, fail(i+1, "正则须为 1～2048 字节")
		}
		re, err := regexp.Compile("(?i:" + pattern + ")")
		if err != nil {
			return nil, fail(i+1, "正则无效："+err.Error())
		}
		if re.MatchString("") {
			return nil, fail(i+1, "正则不能匹配空字符串")
		}
		re.Longest()
		out = append(out, rule{line: i + 1, output: output, re: re})
	}
	return out, nil
}

var active atomic.Pointer[Snapshot]

func Current() Snapshot {
	if s := active.Load(); s != nil {
		return *s
	}
	// 尚未加载用户词表时固定为空快照，不读取任何内置名单。
	revision := Revision("", "")
	return Snapshot{Revision: revision, compiled: &compiled{revision: revision}}
}

// Deactivate 用于启动时无法加载词表的情况，保持空识别。
func Deactivate() { active.Store(&Snapshot{}) }

// Activate 只接受完整校验后的快照，避免管理端草稿改变正在使用的词表。
func Activate(s Snapshot) error {
	if s.Revision == "" && s.Groups == "" && s.Platforms == "" {
		Deactivate()
		return nil
	}
	var err error
	s, err = s.Prepared()
	if err != nil {
		return err
	}
	active.Store(&s)
	return nil
}

func (s Snapshot) Recognize(name string) (Result, error) {
	out := Result{ReleaseGroups: []string{}, Platforms: []string{}, Matches: []Match{}, Conflicts: []Conflict{}}
	if len(name) > MaxNameBytes || !utf8.ValidString(name) {
		return out, ErrInput
	}
	if s.Revision == "" && s.Groups == "" && s.Platforms == "" {
		return out, nil
	}
	var err error
	s, err = s.Prepared()
	if err != nil {
		return out, err
	}
	out.Matches, err = s.compiled.groupMatcher.find("groups", name, s.compiled.groups, out.Matches)
	if err != nil {
		return Result{}, err
	}
	out.Matches, err = s.compiled.platformMatcher.find("platforms", name, s.compiled.platforms, out.Matches)
	if err != nil {
		return Result{}, err
	}
	sort.SliceStable(out.Matches, func(i, j int) bool {
		if out.Matches[i].Start == out.Matches[j].Start {
			if out.Matches[i].End == out.Matches[j].End {
				if out.Matches[i].Kind != out.Matches[j].Kind {
					return out.Matches[i].Kind < out.Matches[j].Kind
				}
				return out.Matches[i].Line < out.Matches[j].Line
			}
			return out.Matches[i].End > out.Matches[j].End
		}
		return out.Matches[i].Start < out.Matches[j].Start
	})
	accepted := make([]Match, 0, len(out.Matches))
	blocked := make([]Match, 0, len(out.Matches))
	for i := 0; i < len(out.Matches); {
		j := i + 1
		for j < len(out.Matches) && out.Matches[j].Kind == out.Matches[i].Kind && out.Matches[j].Start == out.Matches[i].Start && out.Matches[j].End == out.Matches[i].End {
			j++
		}
		group := out.Matches[i:j]
		chosen := group[0]
		mapped := make([]Match, 0, len(group))
		for _, m := range group {
			if m.Mapped {
				mapped = append(mapped, m)
			}
		}
		if len(mapped) > 0 {
			chosen = mapped[0]
		}
		conflict := Conflict{Kind: chosen.Kind, Raw: chosen.Raw, Lines: []int{}, Values: []string{}}
		for _, m := range mapped {
			if !containsFold(conflict.Values, m.Value) {
				conflict.Values = append(conflict.Values, m.Value)
			}
			conflict.Lines = append(conflict.Lines, m.Line)
		}
		if len(conflict.Values) > 1 {
			out.Conflicts = append(out.Conflicts, conflict)
			blocked = append(blocked, chosen)
			i = j
			continue
		}
		// 同一位置优先最长完整命中，避免 VCB 与 VCB-Studio 被重复输出。
		overlaps := false
		for _, m := range blocked {
			if m.Kind == chosen.Kind && chosen.Start < m.End && chosen.End > m.Start {
				overlaps = true
				break
			}
		}
		for _, m := range accepted {
			if m.Kind == chosen.Kind && chosen.Start < m.End && chosen.End > m.Start {
				overlaps = true
				break
			}
		}
		if !overlaps {
			accepted = append(accepted, chosen)
		}
		i = j
	}
	// 较短片段的冲突也会影响先前接受的较长命中，不能保留覆盖冲突区域的结果。
	filtered := make([]Match, 0, len(accepted))
	for _, m := range accepted {
		conflicted := false
		for _, b := range blocked {
			if b.Kind == m.Kind && m.Start < b.End && m.End > b.Start {
				conflicted = true
				break
			}
		}
		if !conflicted {
			filtered = append(filtered, m)
		}
	}
	accepted = filtered
	out.Matches = accepted
	for _, m := range accepted {
		if m.Kind == "groups" {
			if !containsFold(out.ReleaseGroups, m.Value) {
				out.ReleaseGroups = append(out.ReleaseGroups, m.Value)
			}
		} else if !containsFold(out.Platforms, m.Value) {
			out.Platforms = append(out.Platforms, m.Value)
		}
	}
	return out, nil
}

func containsFold(values []string, value string) bool {
	for _, v := range values {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}

// Prepared 校验固定快照并复用已编译规则，持久化恢复时才重新校验哈希与编译。
func (s Snapshot) Prepared() (Snapshot, error) {
	if c := s.compiled; c != nil && c.revision == s.Revision && c.groupsText == s.Groups && c.platformsText == s.Platforms {
		return s, nil
	}
	if s.Revision != Revision(s.Groups, s.Platforms) {
		return Snapshot{}, fmt.Errorf("%w：词表版本与内容不一致", ErrConfig)
	}
	return Compile(s.Groups, s.Platforms)
}

func shortASCIICode(raw string) bool {
	count := 0
	for _, r := range raw {
		r = foldRune(r)
		count++
		if count > 3 || r < 'A' || r > 'Z' {
			return false
		}
	}
	return count > 0
}

func boundary(name string, index int, before bool) bool {
	if before {
		if index == 0 {
			return true
		}
		r, _ := utf8.DecodeLastRuneInString(name[:index])
		return !wordRune(r)
	}
	if index == len(name) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[index:])
	return !wordRune(r)
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) }

var platformEvidence = regexp.MustCompile(`(?i)(?:web[ ._-]?(?:dl|rip)|(?:720|1080|2160)p|S\d{1,3}E\d{1,4}|HEVC|H[ ._-]?26[45]|AVC)`)

func platformContext(name string, start, end int) bool {
	// 方括号中的独立标记或规格附近的短代码才作为平台，避免把作品标题里的 IQ 当成来源。
	prefix := name[:start]
	open := strings.LastIndexAny(prefix, "[【(（{")
	close := strings.LastIndexAny(prefix, "]】)）}")
	if open > close && strings.ContainsAny(name[end:], "]】)）}") {
		return true
	}
	left, right := max(0, start-80), min(len(name), end+80)
	return platformEvidence.MatchString(name[left:right])
}
