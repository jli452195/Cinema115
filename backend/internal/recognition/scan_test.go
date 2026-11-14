package recognition

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRecognitionScansKnownGroupsAcrossFullName(t *testing.T) {
	snapshot, err := Compile("EPSiLON\nLoliHouse\nVCB-Studio\nNekomoe kissaten\n喵萌奶茶屋\n(?:桜|樱)都字幕[组組]", testPlatforms)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		groups []string
	}{
		{"EPSiLON -dfaef", []string{"EPSiLON"}},
		{"EPSiLON", []string{"EPSiLON"}},
		{"EPSiLON-示例电影.mkv", []string{"EPSiLON"}},
		{"(EPSiLON) 示例电影.mkv", []string{"EPSiLON"}},
		{"（EPSiLON）示例电影.mkv", []string{"EPSiLON"}},
		{"[EPSiLON 1080p] 示例电影.mkv", []string{"EPSiLON"}},
		{"示例电影-EPSiLON[1080p].mkv", []string{"EPSiLON"}},
		{"示例电影 EPSiLON 1080p.mkv", []string{"EPSiLON"}},
		{"示例动画-樱都字幕组-02 [1080p].mkv", []string{"樱都字幕组"}},
		{"-[1080p]-樱都字幕组- 示例动画.mkv", []string{"樱都字幕组"}},
		{"喵萌奶茶屋&LoliHouse 示例动画.mkv", []string{"喵萌奶茶屋", "LoliHouse"}},
		{"示例电影-Yumi@EPSiLON.mkv", []string{"EPSiLON"}},
		{"示例动画 Nekomoe kissaten＋VCB-Studio [1080p].mkv", []string{"Nekomoe kissaten", "VCB-Studio"}},
		{"[EPSiLON+LoliHouse] 示例动画.mkv", []string{"EPSiLON", "LoliHouse"}},
		{"[EPSiLON＆LoliHouse@VCB-Studio] 示例动画.mkv", []string{"EPSiLON", "LoliHouse", "VCB-Studio"}},
		{"EPSiLON · epsILON [EPSiLON].mkv", []string{"EPSiLON"}},
		{"[未知字幕组] 示例动画.mkv", []string{}},
		{"NotEPSiLON-示例电影.mkv", []string{}},
		{"EPSiLONABC-示例电影.mkv", []string{}},
		{"EPSiLON2-示例电影.mkv", []string{}},
		{"LoliHouse的故事.mkv", []string{}},
		{"前传LoliHouse 示例动画.mkv", []string{}},
		{"LoliHouse\u0301 示例动画.mkv", []string{}},
		{"X\u0301LoliHouse 示例动画.mkv", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := snapshot.Recognize(tc.name)
			if err != nil || !reflect.DeepEqual(got.ReleaseGroups, tc.groups) || len(got.Conflicts) != 0 {
				t.Fatalf("完整名称识别不符：%+v，错误：%v，期望：%q", got, err, tc.groups)
			}
			for _, match := range got.Matches {
				if match.Start < 0 || match.End > len(tc.name) || match.Start >= match.End || tc.name[match.Start:match.End] != match.Raw {
					t.Fatalf("命中字节偏移不符：%+v", match)
				}
			}
		})
	}
}

func TestRecognitionFullScanPreservesUnicodeAndCaseRules(t *testing.T) {
	cases := []struct {
		source, name string
		want         []string
	}{
		{"KStudio\nSStudio", "前缀 [KStudio&ſStudio] 后缀", []string{"KStudio", "ſStudio"}},
		{"(?-i:CaseGroup)", "[CaseGroup] casegroup", []string{"CaseGroup"}},
		{"(?-i:CaseGroup)", "[casegroup]", []string{}},
		{"(?-i:VCB)-Studio", "[VCB-studio]", []string{"VCB-studio"}},
		{"(?-i:VCB)-Studio", "[vcb-Studio]", []string{}},
		{"(?-i:CaseGroup[0-9])", "[CaseGroup2 1080p]", []string{"CaseGroup2"}},
		{"(?-i:CaseGroup[0-9])", "[casegroup2 1080p]", []string{}},
		{"(?:桜|樱)都字幕[组組]", "前缀（樱都字幕組）后缀", []string{"樱都字幕組"}},
		{"KStudio|X.KStudio", "AX.KStudio", []string{"KStudio"}},
		{"(?P<prefix>KStudio)|X.(?P<suffix>KStudio)", "AX.KStudio", []string{"KStudio"}},
	}
	for _, tc := range cases {
		t.Run(tc.source+"/"+tc.name, func(t *testing.T) {
			snapshot, err := Compile(tc.source, "")
			if err != nil {
				t.Fatal(err)
			}
			got, err := snapshot.Recognize(tc.name)
			if err != nil || !reflect.DeepEqual(got.ReleaseGroups, tc.want) {
				t.Fatalf("大小写或 Unicode 识别不符：%+v，%v", got, err)
			}
			for _, match := range got.Matches {
				if tc.name[match.Start:match.End] != match.Raw || match.Raw != match.Value {
					t.Fatalf("Unicode 命中必须保留原文及字节位置：%+v", match)
				}
			}
		})
	}
}

func TestRecognitionFullScanAnchorsUseEntireInput(t *testing.T) {
	snapshot, err := Compile("^EPSiLON$\n^PrefixGroup[0-9]\nSuffixGroup[0-9]$", "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want []string
	}{
		{"EPSiLON", []string{"EPSiLON"}},
		{"[EPSiLON]", []string{}},
		{"EPSiLON.mkv", []string{}},
		{"PrefixGroup2 示例动画.mkv", []string{"PrefixGroup2"}},
		{"[PrefixGroup2] 示例动画.mkv", []string{}},
		{"示例动画-SuffixGroup3", []string{"SuffixGroup3"}},
		{"示例动画-SuffixGroup3.mkv", []string{}},
	}
	for _, tc := range cases {
		got, err := snapshot.Recognize(tc.name)
		if err != nil || !reflect.DeepEqual(got.ReleaseGroups, tc.want) {
			t.Fatalf("锚点应作用于完整输入 %q：%+v，%v", tc.name, got, err)
		}
	}
}

func TestRecognitionFullScanShortGroupsRequireContext(t *testing.T) {
	snapshot, err := Compile("DON\nANi\nKS", "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want []string
	}{
		{"DON", []string{"DON"}},
		{"[DON] 示例电影.mkv", []string{"DON"}},
		{"示例电影-DON.mkv", []string{"DON"}},
		{"ANi-示例动画.mkv", []string{"ANi"}},
		{"示例 ANi WEB-DL 1080p.mkv", []string{"ANi"}},
		{"A Story About DON Today.mkv", []string{}},
		{"示例 ANi 标题.mkv", []string{}},
		{"DONald 的故事.mkv", []string{}},
		{"A Story About KS Today.mkv", []string{}},
		{"[KS] 示例动画.mkv", []string{"KS"}},
	}
	for _, tc := range cases {
		got, err := snapshot.Recognize(tc.name)
		if err != nil || !reflect.DeepEqual(got.ReleaseGroups, tc.want) {
			t.Fatalf("短名上下文识别不符 %q：%+v，%v", tc.name, got, err)
		}
	}
}

func TestRecognitionFullScanOverlapsAndMappingConflicts(t *testing.T) {
	cases := []struct {
		source    string
		name      string
		groups    []string
		conflicts int
	}{
		{"VCB\nVCB-Studio\nStudio", "[VCB-Studio] 示例动画.mkv", []string{"VCB-Studio"}, 0},
		{"EPSiLON\n规范组 ~= EPSiLON", "EPSiLON 示例电影.mkv", []string{"规范组"}, 0},
		{"EPSiLON\n甲 ~= EPSiLON\n乙 ~= EPSiLON", "示例电影（EPSiLON）[1080p].mkv", []string{}, 1},
		{"VCB-Studio\n甲 ~= VCB\n乙 ~= VCB", "[VCB-Studio] 示例动画.mkv", []string{}, 1},
		{"VCB\n甲 ~= VCB-Studio\n乙 ~= VCB-Studio", "[VCB-Studio] 示例动画.mkv", []string{}, 1},
	}
	for _, tc := range cases {
		snapshot, err := Compile(tc.source, "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := snapshot.Recognize(tc.name)
		if err != nil || !reflect.DeepEqual(got.ReleaseGroups, tc.groups) || len(got.Conflicts) != tc.conflicts {
			t.Fatalf("重叠或映射处理不符 %q：%+v，%v", tc.source, got, err)
		}
	}
}

func TestRecognitionFullScanSnapshotIntegrityAndConcurrency(t *testing.T) {
	snapshot, err := Compile("EPSiLON\nLoliHouse", testPlatforms)
	if err != nil {
		t.Fatal(err)
	}
	changed := snapshot
	changed.Groups = "OtherGroup"
	if _, err := changed.Recognize("OtherGroup Show.mkv"); !errors.Is(err, ErrConfig) {
		t.Fatal("修改词表后不可继续使用旧编译索引", err)
	}
	changed = snapshot
	changed.Platforms = "OTHER"
	if _, err := changed.Recognize("EPSiLON NF WEB-DL.mkv"); !errors.Is(err, ErrConfig) {
		t.Fatal("修改平台词表后不可继续使用旧编译索引", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restored Snapshot
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	got, err := restored.Recognize("EPSiLON Show.NF.WEB-DL.mkv")
	if err != nil || !reflect.DeepEqual(got.ReleaseGroups, []string{"EPSiLON"}) || !reflect.DeepEqual(got.Platforms, []string{"NF"}) {
		t.Fatal("持久化快照恢复后应保持识别结果", got, err)
	}
	empty, err := Compile("", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err = empty.Recognize("EPSiLON Show.NF.WEB-DL.mkv")
	if err != nil || len(got.ReleaseGroups) != 0 || len(got.Platforms) != 0 {
		t.Fatal("空词表不应隐式识别", got, err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 20; j++ {
				got, err := snapshot.Recognize("[EPSiLON+LoliHouse] Show.NF.WEB-DL.mkv")
				if err != nil || !reflect.DeepEqual(got.ReleaseGroups, []string{"EPSiLON", "LoliHouse"}) {
					t.Errorf("共享只读索引的并发识别不符：%+v，%v", got, err)
					return
				}
			}
		}()
	}
	workers.Wait()
}

func TestRecognitionFullScanRejectsExcessiveMatches(t *testing.T) {
	for _, pattern := range []string{"EPSiLON", "EPSiLON[0-9]"} {
		t.Run(pattern, func(t *testing.T) {
			var source strings.Builder
			for i := 0; i < 100; i++ {
				fmt.Fprintf(&source, "映射%d ~= %s\n", i, pattern)
			}
			snapshot, err := Compile(source.String(), "")
			if err != nil {
				t.Fatal(err)
			}
			name := "EPSiLON"
			if strings.Contains(pattern, "[") {
				name += "1"
			}
			_, err = snapshot.Recognize(strings.Repeat("["+name+"] ", 90))
			if !errors.Is(err, ErrInput) {
				t.Fatal("过量命中应报告输入错误，不能继续分配或静默截断", err)
			}
		})
	}
}
