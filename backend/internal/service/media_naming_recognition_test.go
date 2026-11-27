package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/recognition"
	"strm115/backend/internal/utils/torrentmeta"
)

// 旧规格断言使用的辅助函数；生产入口均显式检查识别错误。
func mediaFileValues(name string, hint DownloadFileSpec) map[naming.Field]string {
	values, err := mediafacts.ValuesWithRecognition(name, hint.Spec, recognition.Current())
	if err != nil {
		return nil
	}
	return values
}

func recognitionTestSnapshot(t *testing.T, groups, platforms string) recognition.Snapshot {
	t.Helper()
	snapshot, err := recognition.Compile(groups, platforms)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestDownloadRecognitionUsesFullJointGroupAndScopedTitle(t *testing.T) {
	rules := recognitionTestSnapshot(t, "喵萌奶茶屋\nLoliHouse\nEPSiLON\n桜都字幕组 ~= (?:桜|樱)都字幕[组組]", "NF ~= NF|Netflix\nIQ ~= IQ|iQIYI|爱奇艺")
	cases := []struct {
		name, title, sourceGroup string
		files                    []string
		groups, platforms        []string
	}{
		{"联合组完整保留", "[LoliHouse] Show [01]", "LoliHouse", []string{"[喵萌奶茶屋&LoliHouse] Show [01][NF WEB-DL].mkv"}, []string{"喵萌奶茶屋&LoliHouse"}, []string{"NF"}},
		{"单文件标题补缺", "[喵萌奶茶屋&LoliHouse] Show [01][iQIYI WEB-DL]", "LoliHouse", []string{"Show [01].mkv"}, []string{"喵萌奶茶屋&LoliHouse"}, []string{"IQ"}},
		{"文件标记优先", "[LoliHouse] Show [01][NF WEB-DL]", "LoliHouse", []string{"[樱都字幕組] Show [01][IQ WEB-DL].mkv"}, []string{"桜都字幕组"}, []string{"IQ"}},
		{"短横线前缀", "Show [01]", "LoliHouse", []string{"-樱都字幕组- 示例动画 - 02 [1080p].mkv"}, []string{"桜都字幕组"}, []string{""}},
		{"短横线联合组", "Show [01]", "LoliHouse", []string{"-喵萌奶茶屋&LoliHouse- 示例动画 [01][NF WEB-DL].mkv"}, []string{"喵萌奶茶屋&LoliHouse"}, []string{"NF"}},
		{"裸前缀发布组", "Show [01]", "LoliHouse", []string{"EPSiLON -dfaef.mkv"}, []string{"EPSiLON"}, []string{""}},
		{"混合规格标签", "Show [01]", "LoliHouse", []string{"[EPSiLON 1080p] 示例电影.mkv"}, []string{"EPSiLON"}, []string{""}},
		{"中间位置联合组", "Show [01]", "LoliHouse", []string{"示例动画 喵萌奶茶屋+LoliHouse [01][NF WEB-DL].mkv"}, []string{"喵萌奶茶屋&LoliHouse"}, []string{"NF"}},
		{"合集不继承标题", "[喵萌奶茶屋&LoliHouse] Show [01-02][NF WEB-DL]", "LoliHouse", []string{"Show [01].mkv", "[LoliHouse] Show [02].mkv"}, []string{"", "LoliHouse"}, []string{"", ""}},
		{"仅有来源组时留空", "Show [01]", "LoliHouse", []string{"Show [01].mkv"}, []string{""}, []string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := make([]torrentmeta.File, 0, len(tc.files))
			for _, name := range tc.files {
				files = append(files, torrentmeta.File{Path: name, Size: 10})
			}
			out, err := organizeResourceFiles(gardenTestHash, files)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareDownloadNaming(&out, DownloadResourceInput{Title: tc.title, Fansub: tc.sourceGroup}, rules); err != nil {
				t.Fatal(err)
			}
			for i, file := range out.Files {
				// 清单按自然顺序排序，按原文件名找期望结果。
				for j, name := range tc.files {
					if file.Name == name && (file.NamingValues[naming.ReleaseGroup] != tc.groups[j] || file.NamingValues[naming.Platform] != tc.platforms[j]) {
						t.Fatalf("文件 %d 的组名或平台错误：%s %+v", i, file.Name, file.NamingValues)
					}
				}
			}
		})
	}
}

func TestMediaNamingRecognitionPinsPreviewAndRejectsConflict(t *testing.T) {
	rules := recognitionTestSnapshot(t, "规范组 ~= LoliHouse", "IQ ~= IQ|iQIYI")
	ctx := pinNamingRecognition(context.Background(), rules)
	template, err := naming.Default(naming.TV)
	if err != nil {
		t.Fatal(err)
	}
	s := MediaNaming{Templates: &mediaNamingTemplates{record: naming.Snapshot{Template: template}}, Metadata: &mediaNamingMetadata{}}
	p, err := s.prepare(ctx, "tv", 42, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := MediaNamingFile{OrganizeSelection: OrganizeSelection{ID: "v", Season: 1, Episode: 1}, Name: "[LoliHouse] Show.S01E01.IQ.WEB-DL.mkv"}
	result, err := p.render(ctx, item)
	if err != nil || !strings.Contains(result.Name, "-规范组.mkv") || !strings.Contains(result.Name, ".IQ-") {
		t.Fatal("公共命名没有使用固定词表", result, err)
	}
	conflict := recognitionTestSnapshot(t, "组甲 ~= LoliHouse\n组乙 ~= LoliHouse", "")
	p.recognition = conflict
	if _, err := p.render(ctx, item); err == nil {
		t.Fatal("冲突映射不能生成可执行命名结果")
	}
}

func TestDownloadRecognitionSkipsUnneededTitleConflicts(t *testing.T) {
	rules := recognitionTestSnapshot(t, "ANi\n组甲 ~= LoliHouse\n组乙 ~= LoliHouse", "NF ~= NF|Netflix")
	out, err := organizeResourceFiles(gardenTestHash, []torrentmeta.File{{Path: "[ANi] Show.S01E01.NF.WEB-DL.mkv", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareDownloadNaming(&out, DownloadResourceInput{Title: "[LoliHouse] Show [01][2160p][简体]"}, rules); err != nil {
		t.Fatal("文件已有完整组名和平台时不应受无关标题冲突影响", err)
	}
	values := out.Files[0].NamingValues
	if values[naming.ReleaseGroup] != "ANi" || values[naming.Platform] != "NF" || values[naming.Resolution] != "2160p" || values[naming.Languages] != "zh-CN" {
		t.Fatal("跳过标题词表不能丢失文件事实或原有规格继承", values)
	}
}

func TestMediaOrganizerRecognitionSnapshotSurvivesConfigChangeAndJSON(t *testing.T) {
	previous := recognition.Current()
	t.Cleanup(func() {
		if err := recognition.Activate(previous); err != nil {
			t.Error(err)
		}
	})
	first := recognitionTestSnapshot(t, "原组 ~= LoliHouse", "NF ~= NF|Netflix")
	if err := recognition.Activate(first); err != nil {
		t.Fatal(err)
	}
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "[LoliHouse] Work.S01E01.NF.WEB-DL.strm", "original")
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var restored MediaOrganizeRun
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	changed := recognitionTestSnapshot(t, "新组 ~= LoliHouse", "OTHER ~= NF|Netflix")
	if err := recognition.Activate(changed); err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &restored, "auto")
	if len(restored.Entries) != 1 || restored.Entries[0].Status != "done" || !strings.Contains(restored.Entries[0].Relative, "-原组.strm") || strings.Contains(restored.Entries[0].Relative, "OTHER") {
		t.Fatal("词表修改或序列化改变了已排队任务", restored.Entries)
	}
}

func TestMediaOrganizerRecognitionRejectsDamagedSnapshot(t *testing.T) {
	valid := recognitionTestSnapshot(t, "LoliHouse", "NF")
	for _, snapshot := range []recognition.Snapshot{
		{Groups: valid.Groups, Platforms: valid.Platforms},
		{Groups: valid.Groups, Platforms: valid.Platforms, Revision: "changed"},
		{Groups: "(", Revision: valid.Revision},
	} {
		run := MediaOrganizeRun{Recognition: snapshot}
		if err := run.prepareRecognition(); err == nil {
			t.Fatal("损坏快照不能被当前配置替换", snapshot)
		}
	}
}

func TestMediaOrganizerZeroRecognitionStaysEmpty(t *testing.T) {
	previous := recognition.Current()
	t.Cleanup(func() { _ = recognition.Activate(previous) })
	run := MediaOrganizeRun{}
	if err := run.prepareRecognition(); err != nil {
		t.Fatal(err)
	}
	if err := recognition.Activate(recognitionTestSnapshot(t, "LoliHouse", "NF")); err != nil {
		t.Fatal(err)
	}
	if err := run.prepareRecognition(); err != nil {
		t.Fatal(err)
	}
	values, err := mediafacts.ValuesWithRecognition("[LoliHouse] Show.S01E01.NF.WEB-DL.mkv", DownloadFileSpec{}.Spec, run.Recognition)
	if err != nil || values[naming.ReleaseGroup] != "" || values[naming.Platform] != "" || values[naming.Source] != "WEB-DL" {
		t.Fatal("空任务快照不能跟随当前词表变化，其他规格仍应识别", values, err)
	}
}
