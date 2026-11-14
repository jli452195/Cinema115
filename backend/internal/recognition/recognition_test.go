package recognition

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// 测试显式提供词表；生产环境只读取用户保存的 TXT。
const testPlatforms = `NF ~= NF|Netflix
IQ ~= IQ|iQIYI|爱奇艺
YOUKU ~= YOUKU|优酷
`

func TestRecognitionPreservesJointGroupsAndOptionalMapping(t *testing.T) {
	s, err := Compile("LoliHouse\n喵萌奶茶屋\n(?:桜|樱)都字幕[组組]\nVCB-Studio\nNekomoe kissaten", testPlatforms)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name              string
		groups, platforms []string
	}{
		{"[喵萌奶茶屋&LoliHouse] 某动画 S01E01 NF WEB-DL 1080p.mkv", []string{"喵萌奶茶屋", "LoliHouse"}, []string{"NF"}},
		{"【樱都字幕組】 某动画 [01][iQIYI WEB-DL].mkv", []string{"樱都字幕組"}, []string{"IQ"}},
		{"[Nekomoe kissaten＆VCB-Studio] 某动画 [01].mkv", []string{"Nekomoe kissaten", "VCB-Studio"}, []string{}},
		{"Show.S01E01.1080p.WEB-DL-VCB-Studio.mkv", []string{"VCB-Studio"}, []string{}},
		{"LoliHouse的故事.S01E01.mkv", []string{}, []string{}},
		{"Show.NotLoliHouse.1080p.mkv", []string{}, []string{}},
		{"[LoliHouse&lolihouse] Show.S01E01.mkv", []string{"LoliHouse"}, []string{}},
		{"IQ.2020.mkv", []string{}, []string{}},
		{"Show.iQIYI.mkv", []string{}, []string{"IQ"}},
		{"Show.爱奇艺.mkv", []string{}, []string{"IQ"}},
		{"Show.优酷.mkv", []string{}, []string{"YOUKU"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Recognize(tc.name)
			if err != nil || !reflect.DeepEqual(got.ReleaseGroups, tc.groups) || !reflect.DeepEqual(got.Platforms, tc.platforms) {
				t.Fatalf("识别结果不符：%+v, %v", got, err)
			}
		})
	}
	mapped, err := Compile(s.Groups+"\n桜都字幕组 ~= (?:桜|樱)都字幕[组組]", s.Platforms)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mapped.Recognize("[樱都字幕組] 动画.mkv")
	if err != nil || !reflect.DeepEqual(got.ReleaseGroups, []string{"桜都字幕组"}) || len(got.Matches) != 1 || got.Matches[0].Raw != "樱都字幕組" || !got.Matches[0].Mapped {
		t.Fatal(got, err)
	}
}

func TestRecognitionHyphenWrappedPrefix(t *testing.T) {
	s, err := Compile("(?:桜|樱)都字幕[组組]\n(?:桜|樱)花字幕[组組]\n喵萌奶茶屋\nLoliHouse\nVCB\nVCB-Studio\nNekomoe kissaten\nNTb", "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		groups []string
	}{
		{"-樱都字幕组- 示例动画 - 02 [1080p].mkv", []string{"樱都字幕组"}},
		{"-樱花字幕组-示例动画 - 02 [1080p].mkv", []string{"樱花字幕组"}},
		{" \t-桜都字幕組- 示例动画.mkv", []string{"桜都字幕組"}},
		{"-喵萌奶茶屋&LoliHouse- 示例动画.mkv", []string{"喵萌奶茶屋", "LoliHouse"}},
		{"-Nekomoe kissaten＆VCB-Studio- 示例动画.mkv", []string{"Nekomoe kissaten", "VCB-Studio"}},
		{"-VCB-Studio- Show.S01E01.1080p-NTb.mkv", []string{"VCB-Studio", "NTb"}},
		{"-LoliHouse&lolihouse- 示例动画.mkv", []string{"LoliHouse"}},
		{"-未知字幕组- 示例动画.mkv", []string{}},
		{"-NotLoliHouse- 示例动画.mkv", []string{}},
		{"-LoliHouse的故事- 示例动画.mkv", []string{}},
		{"示例动画-樱都字幕组-02 [1080p].mkv", []string{"樱都字幕组"}},
		{"-[1080p]-樱都字幕组- 示例动画.mkv", []string{"樱都字幕组"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Recognize(tc.name)
			if err != nil || !reflect.DeepEqual(got.ReleaseGroups, tc.groups) || len(got.Conflicts) != 0 {
				t.Fatalf("前缀识别结果不符：%+v, %v", got, err)
			}
			for _, match := range got.Matches {
				if tc.name[match.Start:match.End] != match.Raw || match.Raw != match.Value {
					t.Fatalf("命中位置或原文不符：%+v", match)
				}
			}
		})
	}
	for _, source := range []string{"", "桜都字幕组 ~= (?:桜|樱)都字幕[组組]", "甲 ~= 樱都字幕组\n乙 ~= 樱都字幕组"} {
		snapshot, err := Compile(source, "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := snapshot.Recognize("-樱都字幕组- 示例动画.mkv")
		if err != nil {
			t.Fatal(err)
		}
		switch source {
		case "":
			if len(got.ReleaseGroups) != 0 {
				t.Fatal("空词表不可识别前缀", got)
			}
		case "桜都字幕组 ~= (?:桜|樱)都字幕[组組]":
			if !reflect.DeepEqual(got.ReleaseGroups, []string{"桜都字幕组"}) || len(got.Matches) != 1 || !got.Matches[0].Mapped {
				t.Fatal("前缀应支持可选映射", got)
			}
		default:
			if len(got.ReleaseGroups) != 0 || len(got.Conflicts) != 1 {
				t.Fatal("前缀映射冲突不可静默选择", got)
			}
		}
	}
}

func TestRecognitionConflictAndPersistentSnapshot(t *testing.T) {
	s, err := Compile("LoliHouse\n甲 ~= LoliHouse\n乙 ~= LoliHouse", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Recognize("[LoliHouse] 动画.mkv")
	if err != nil || len(got.ReleaseGroups) != 0 || len(got.Conflicts) != 1 || !reflect.DeepEqual(got.Conflicts[0].Lines, []int{2, 3}) {
		t.Fatal(got, err)
	}
	mixed, err := Compile("甲 ~= EPSiLON[0-9]\n乙 ~= EPSiLON1", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err = mixed.Recognize("[EPSiLON1] 示例电影.mkv")
	if err != nil || len(got.Conflicts) != 1 || !reflect.DeepEqual(got.Conflicts[0].Lines, []int{1, 2}) || !reflect.DeepEqual(got.Conflicts[0].Values, []string{"甲", "乙"}) {
		t.Fatal("字面量和正则索引的执行顺序不可改变词表行号顺序", got, err)
	}
	platformRules, err := Compile("", "甲 ~= Amazon Prime\n乙 ~= Amazon Prime\nAmazon")
	if err != nil {
		t.Fatal(err)
	}
	got, err = platformRules.Recognize("Show.Amazon Prime.WEB-DL.mkv")
	if err != nil || len(got.Platforms) != 0 || len(got.Conflicts) != 1 {
		t.Fatal("映射冲突不能通过较短重叠规则绕过", got, err)
	}
	platformRules, err = Compile("", "Amazon Prime\n甲 ~= Amazon\n乙 ~= Amazon")
	if err != nil {
		t.Fatal(err)
	}
	got, err = platformRules.Recognize("Show.Amazon Prime.WEB-DL.mkv")
	if err != nil || len(got.Platforms) != 0 || len(got.Conflicts) != 1 {
		t.Fatal("较短映射冲突也必须阻止覆盖它的长命中", got, err)
	}
	s, err = Compile("LoliHouse", testPlatforms)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored Snapshot
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	got, err = restored.Recognize("[LoliHouse] Show.S01E01.NF.WEB-DL.mkv")
	if err != nil || len(got.ReleaseGroups) != 1 || len(got.Platforms) != 1 {
		t.Fatal(got, err)
	}
	restored.Groups = "Other"
	if _, err := restored.Recognize("[Other] 动画.mkv"); !errors.Is(err, ErrConfig) {
		t.Fatal("快照篡改未被拒绝", err)
	}
	got, err = (Snapshot{}).Recognize("[LoliHouse] 动画.mkv")
	if err != nil || len(got.ReleaseGroups) != 0 {
		t.Fatal("空快照不应读取全局词表", got, err)
	}
	s.Groups = "Other"
	s.Revision = Revision(s.Groups, s.Platforms)
	got, err = s.Recognize("[Other] 动画.mkv")
	if err != nil || !reflect.DeepEqual(got.ReleaseGroups, []string{"Other"}) {
		t.Fatal("修改后的快照不可继续使用旧编译规则", got, err)
	}
}

func TestRecognitionDiagnostics(t *testing.T) {
	for _, source := range []string{"\n#注释\n[", "\n#注释\na*", "\n#注释\n名字 ~= ", "\n#注释\n ~= Name", "\n#注释\n(?<=a)b"} {
		_, err := Compile(source, "")
		var diagnostic *Diagnostic
		if !errors.As(err, &diagnostic) || diagnostic.Kind != "groups" || diagnostic.Line != 3 {
			t.Fatalf("缺少准确行诊断：%q, %v", source, err)
		}
	}
	if _, err := Compile(strings.Repeat("x", MaxTextBytes+1), ""); !errors.Is(err, ErrConfig) {
		t.Fatal("未限制词表大小", err)
	}
	if _, err := Compile(strings.Repeat("x\n", MaxRules+1), ""); !errors.Is(err, ErrConfig) {
		t.Fatal("未限制规则数", err)
	}
	if _, err := Compile("", ""); err != nil {
		t.Fatal("空词表必须有效", err)
	}
}
