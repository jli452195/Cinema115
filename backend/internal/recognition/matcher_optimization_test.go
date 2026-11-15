package recognition

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"testing"
)

// 保留优化前的逐条正则扫描作为差分参照，不依赖新增的规则预筛选逻辑。
func recognitionLegacyMatcher(t *testing.T, rules []rule) matcher {
	t.Helper()
	m := matcher{patterns: make([]patternRule, 0, len(rules))}
	for index, r := range rules {
		parsed, err := syntax.Parse(r.re.String(), syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		for parsed.Op == syntax.OpCapture {
			parsed = parsed.Sub[0]
		}
		if parsed.Op == syntax.OpLiteral {
			if parsed.Flags&syntax.FoldCase != 0 {
				m.folded.add(parsed.Rune, index, true)
			} else {
				m.sensitive.add(parsed.Rune, index, false)
			}
			continue
		}
		var strip func(*syntax.Regexp) *syntax.Regexp
		strip = func(node *syntax.Regexp) *syntax.Regexp {
			if node.Op == syntax.OpCapture {
				return strip(node.Sub[0])
			}
			for i, child := range node.Sub {
				node.Sub[i] = strip(child)
			}
			return node
		}
		scan, err := regexp.Compile(`(?:\A|[^\p{L}\p{N}\p{M}])(` + strip(parsed).String() + `)`)
		if err != nil {
			t.Fatal(err)
		}
		scan.Longest()
		m.patterns = append(m.patterns, patternRule{index: index, re: scan})
	}
	return m
}

func recognitionLegacySnapshot(t *testing.T, value Snapshot) Snapshot {
	t.Helper()
	compiled := *value.compiled
	compiled.groupMatcher = recognitionLegacyMatcher(t, compiled.groups)
	compiled.platformMatcher = recognitionLegacyMatcher(t, compiled.platforms)
	value.compiled = &compiled
	return value
}

func assertRecognitionMatchesLegacy(t *testing.T, snapshot, legacy Snapshot, name string) {
	t.Helper()
	got, gotErr := snapshot.Recognize(name)
	want, wantErr := legacy.Recognize(name)
	if (gotErr == nil) != (wantErr == nil) || gotErr != nil && (gotErr.Error() != wantErr.Error() || errors.Is(gotErr, ErrInput) != errors.Is(wantErr, ErrInput)) {
		t.Fatalf("输入 %q 的错误与旧扫描不符：新=%v，旧=%v", name, gotErr, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("输入 %q 的完整结果与旧扫描不符：\n新=%+v\n旧=%+v", name, got, want)
	}
}

func TestRecognitionMatcherOptimizationMatchesLegacy(t *testing.T) {
	cases := []struct {
		label, groups, platforms string
		names                    []string
	}{
		{
			label:     "Unicode与混合大小写",
			groups:    "KStudio(?:-Raws)?\nSStudio(?:-Raws)?\n(?-i:CaseGroup)(?:-RAW|-Raw)?\n(?-i:VCB)-Studio\n(?:桜|樱)都字幕[组組]",
			platforms: "NF ~= NF|Netflix\nIQ ~= iQIYI|爱奇艺",
			names:     []string{"[KStudio-Raws&ſStudio] 示例电影 [NF WEB-DL].mkv", "[kstudio-raws&sstudio-Raws]", "[CaseGroup-RAW casegroup-Raw CaseGroup-Raw]", "[VCB-studio vcb-Studio VCB-Studio]", "前缀（樱都字幕組）后缀", "[桜都字幕组] [iQIYI WEB-DL]", "[KStudio\u0301 SStudio]"},
		},
		{
			label:     "锚点和可空内部结构",
			groups:    "^Prefix(?:Group|Studio)[0-9]$\n(?:Optional)?Studio\n(?:|Alt)Group\nRaws(?:A|)\n(?m:^LineGroup$)\n(?s:Dot.*Group)",
			platforms: "NF ~= ^NF$",
			names:     []string{"PrefixGroup1", "[PrefixGroup1]", "PrefixStudio2.mkv", "[Studio OptionalStudio Group AltGroup Raws RawsA]", "LineGroup\nLineGroup", "Dot\nGroup", "NF", "[NF]"},
		},
		{
			label:  "同一规则的最长和非重叠匹配",
			groups: "Alpha|Alpha-Beta\nLong(?:-Long)?\n(?:A-B|B-C)\n(?P<first>KStudio)|X.(?P<second>KStudio)",
			names:  []string{"[Alpha-Beta Alpha Alpha-Beta]", "[Long-Long-Long]", "[A-B-C A-B B-C]", "AX.KStudio", "[Alpha][Alpha-Beta]", "AlphaAlpha Alpha-BetaExtra"},
		},
		{
			label:     "映射冲突和重叠",
			groups:    "LoliHouse\n映射甲 ~= LoliHouse|Loli-House\n映射乙 ~= LoliHouse|Loli-House\n长组 ~= LoliHouse-Studio\n短组 ~= LoliHouse\n普通组 ~= (?:桜|樱)都字幕[组組]",
			platforms: "甲 ~= NF|Netflix\n乙 ~= Netflix|NF\nIQ ~= iQIYI|爱奇艺",
			names:     []string{"[LoliHouse] [NF WEB-DL]", "[LoliHouse-Studio] Netflix.WEB-DL", "[Loli-House] [iQIYI WEB-DL]", "[樱都字幕組&LoliHouse]", "[不认识的组]"},
		},
		{
			label:  "有限分支内部重叠和邻接边界",
			groups: "(?:Red|Red-Blue|Blue-Green)\n(?:First|Second)(?:Raws|Studio)\n(?:One|Two)-Group\n(?:K|K)Studio\n(?-i:Sharp|sharp)-Studio",
			names:  []string{"Red-Blue-Green", "[Red-Blue&Blue-Green]", "FirstStudio SecondRaws", "One-GroupTwo-Group", "[One-Group][Two-Group]", "[KStudio KStudio kstudio]", "[Sharp-Studio sharp-studio SHARP-Studio]", "前缀Red-Blue 后缀"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			snapshot, err := Compile(tc.groups, tc.platforms)
			if err != nil {
				t.Fatal(err)
			}
			legacy := recognitionLegacySnapshot(t, snapshot)
			for _, name := range tc.names {
				assertRecognitionMatchesLegacy(t, snapshot, legacy, name)
			}
		})
	}
}

func TestRecognitionMatcherOptimizationGeneratedCases(t *testing.T) {
	patterns := []string{
		`(?:Alpha|Beta)-Studio`, `(?:K|S)Studio[0-9]?`, `(?:桜|樱)都字幕[组組]`,
		`Mixed(?:Case|CAS[Ee])`, `(?:One|One-Two|Two-Three)`, `Studio(?:-Raws)?`,
		`(?-i:Case)(?i:Group)`, `(?:A|)Group`, `^Only(?:Name|Group)$`,
		`(?s:Wide.*Tail)`, `(?:[A-Z]{4}|[0-9]{4})-[0-9]{2}`,
		`(?:Prefix)?(?:One|Two)-Group`, `(?:Alpha|Beta)+Studio`, `(?:End|Else){2}`,
		`(?i:ſ|S)Studio`, `(?:AB|CD)[AB]{10}`,
	}
	atoms := []string{"Alpha-Studio", "Beta-Studio", "KStudio", "KStudio1", "ſStudio2", "樱都字幕組", "桜都字幕组", "MixedCase", "One-Two-Three", "Studio-Raws", "Casegroup", "caseGroup", "AGroup", "Group", "OnlyName", "Wide\nTail", "ABCD-12", "Unknown"}
	wrappers := [][2]string{{"", ""}, {"[", "]"}, {"电影-", " [1080p].mkv"}, {"X", "Y"}, {"\u0301", "\u0301"}, {"前", "后"}}
	for index, pattern := range patterns {
		snapshot, err := Compile("映射 ~= "+pattern+"\n"+pattern, "NF ~= Netflix|NF")
		if err != nil {
			t.Fatal(err)
		}
		legacy := recognitionLegacySnapshot(t, snapshot)
		for i, atom := range atoms {
			for _, wrapper := range wrappers {
				name := wrapper[0] + atom + wrapper[1]
				assertRecognitionMatchesLegacy(t, snapshot, legacy, name)
				joint := name + "&" + atoms[(i+index+1)%len(atoms)] + " [NF WEB-DL]"
				assertRecognitionMatchesLegacy(t, snapshot, legacy, joint)
			}
		}
	}
}

func TestRecognitionMatcherOptimizationMatchLimit(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&source, "Group%d ~= (?:Repeat|Repeat-Studio)\n", i)
	}
	snapshot, err := Compile(source.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	legacy := recognitionLegacySnapshot(t, snapshot)
	assertRecognitionMatchesLegacy(t, snapshot, legacy, strings.Repeat("[Repeat-Studio]", 10))
	assertRecognitionMatchesLegacy(t, snapshot, legacy, strings.Repeat("x", MaxNameBytes+1))
	assertRecognitionMatchesLegacy(t, snapshot, legacy, "\xff")
}

func TestRecognitionMatcherCandidateBitmapBoundariesAndConcurrency(t *testing.T) {
	var source strings.Builder
	for i := 0; i < MaxRules; i++ {
		fmt.Fprintf(&source, "规范组%d ~= Candidate%04d(?:-Studio)?\n", i, i)
	}
	snapshot, err := Compile(source.String(), "NF ~= NF|Netflix")
	if err != nil {
		t.Fatal(err)
	}
	legacy := recognitionLegacySnapshot(t, snapshot)
	names := []string{
		"[Candidate0000-Studio&Candidate0063] NF.WEB-DL.mkv",
		"[Candidate0064&Candidate0127-Studio] Netflix.WEB-DL.mkv",
		"[Candidate0128&Candidate1999-Studio] NF.WEB-DL.mkv",
		"[UnknownGroup] WEB-DL.mkv",
	}
	want := make([]Result, len(names))
	for i, name := range names {
		assertRecognitionMatchesLegacy(t, snapshot, legacy, name)
		want[i], err = legacy.Recognize(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	// 同一只读词表的并发请求不能串用候选位图，命中与未命中交替验证。
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(offset int) {
			defer workers.Done()
			for iteration := 0; iteration < 40; iteration++ {
				index := (offset + iteration) % len(names)
				got, err := snapshot.Recognize(names[index])
				if err != nil || !reflect.DeepEqual(got, want[index]) {
					t.Errorf("共享索引的并发结果不符：%+v，%v", got, err)
					return
				}
			}
		}(worker)
	}
	workers.Wait()
}

// 由 Go 模糊测试持续比较完整输出，覆盖手写样例之外的正则与输入组合。
func FuzzRecognitionMatcherPrefilter(f *testing.F) {
	for _, seed := range [][2]string{
		{`(?:桜|樱)都字幕[组組]`, "前缀（樱都字幕組）后缀"},
		{`(?i:KStudio)(?-i:-RAW)`, "[KStudio-RAW]"},
		{`(?:Alpha|Alpha-Beta)`, "Alpha-Beta-Alpha"},
		{`(?:Alpha)?Studio`, "[Studio AlphaStudio]"},
		{`(?:(?:Alpha){0}|Beta)-Studio`, "[-Studio Beta-Studio]"},
		{`(?:Literal|[0-9]+)-Studio`, "[123-Studio]"},
		{`(?s:BEGIN.*END)`, "[BEGIN\nEND]"},
		{`(?m:^Line[0-9]$)`, "Line1\nLine2"},
		{`(?-i:Case)(?i:Group)`, "[CaseGROUP caseGroup]"},
		{`(?:Alpha|Beta)+Studio`, "[AlphaBetaStudio]"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, source, name string) {
		if len(source) > 512 || len(name) > 1024 {
			return
		}
		snapshot, err := Compile(source, "NF ~= NF|Netflix")
		if err != nil {
			return
		}
		assertRecognitionMatchesLegacy(t, snapshot, recognitionLegacySnapshot(t, snapshot), name)
	})
}
