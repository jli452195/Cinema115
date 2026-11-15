package service

import (
	"encoding/json"
	"errors"
	"testing"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/utils/torrentmeta"
)

func TestMediaOrganizerRecognitionReusesPreparedSnapshot(t *testing.T) {
	snapshot := recognitionTestSnapshot(t, "规范组 ~= LoliHouse\n(?:桜|樱)都字幕[组組]", "NF ~= NF|Netflix")
	run := MediaOrganizeRun{Recognition: snapshot}
	for i := 0; i < 3; i++ {
		if err := run.prepareRecognition(); err != nil {
			t.Fatal(err)
		}
		if run.Recognition != snapshot {
			t.Fatal("同一批次重复准备不应重建已编译的只读词表")
		}
	}

	// JSON 恢复需要重新构建内存索引，完成后仍应复用本批次已恢复的索引。
	body, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var restored MediaOrganizeRun
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.prepareRecognition(); err != nil {
		t.Fatal(err)
	}
	prepared := restored.Recognition
	if err := restored.prepareRecognition(); err != nil || restored.Recognition != prepared {
		t.Fatal("已恢复的批次词表没有复用", err)
	}
	result, err := restored.Recognition.Recognize("[LoliHouse] Work.NF.WEB-DL.mkv")
	if err != nil || len(result.ReleaseGroups) != 1 || result.ReleaseGroups[0] != "规范组" || len(result.Platforms) != 1 || result.Platforms[0] != "NF" {
		t.Fatal("恢复后识别结果改变", result, err)
	}

	// 缓存存在也必须检查原文，禁止篡改后直接复用旧索引或换成全局配置。
	restored.Recognition.Groups = "其他组"
	tampered := restored.Recognition
	if err := restored.prepareRecognition(); !errors.Is(err, ErrMediaOrganizeInput) {
		t.Fatal("修改原文而未更新版本必须拒绝", err)
	}
	if restored.Recognition != tampered {
		t.Fatal("恢复失败不能覆盖原批次快照")
	}
}

func TestDownloadRecognitionReusedFileTagsKeepInheritanceOrder(t *testing.T) {
	rules := recognitionTestSnapshot(t, "LoliHouse\n喵萌奶茶屋", "NF ~= NF|Netflix\nIQ ~= IQ|iQIYI")
	cases := []struct {
		name, file, title, group, platform, languages, mode, resolution string
	}{
		{"目录字幕优先于标题", "Show [繁体][内封]/[LoliHouse] Show [01].mkv", "[喵萌奶茶屋] Show [01][NF WEB-DL][简体][2160p]", "LoliHouse", "NF", "繁体", "内封", "2160p"},
		{"文件字幕优先于目录", "Show [繁体][内封]/[LoliHouse] Show [01][简体][内嵌][1080p].mkv", "[喵萌奶茶屋] Show [01][NF WEB-DL][2160p]", "LoliHouse", "NF", "简体", "内嵌", "1080p"},
		{"文件平台保留并补组名", "Show [01][NF WEB-DL].mkv", "[喵萌奶茶屋] Show [01][IQ WEB-DL][简体][2160p]", "喵萌奶茶屋", "NF", "简体", "", "2160p"},
		{"标题补平台不覆盖组名", "[LoliHouse] Show [01].mkv", "[喵萌奶茶屋] Show [01][iQIYI WEB-DL][繁体][1080p]", "LoliHouse", "IQ", "繁体", "", "1080p"},
		{"未映射组名保留命中原文", "[lOLIHOUSE] Show [01].mkv", "[喵萌奶茶屋] Show [01][NF WEB-DL][简体][1080p]", "lOLIHOUSE", "NF", "简体", "", "1080p"},
		{"文件多平台不能漏出旧平台结果", "[LoliHouse] Show [01][NF IQ WEB-DL].mkv", "Show [01][简体][1080p]", "LoliHouse", "", "简体", "", "1080p"},
		{"文件多平台允许标题补唯一平台", "[LoliHouse] Show [01][NF IQ WEB-DL].mkv", "Show [01][NF WEB-DL][繁体][1080p]", "LoliHouse", "NF", "繁体", "", "1080p"},
		{"无匹配维持空发布信息", "Show [01].mkv", "Show [01][简体][1080p]", "", "", "简体", "", "1080p"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := organizeResourceFiles(gardenTestHash, []torrentmeta.File{{Path: tc.file, Size: 10}})
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareDownloadNaming(&out, DownloadResourceInput{Title: tc.title}, rules); err != nil {
				t.Fatal(err)
			}
			values := out.Files[0].NamingValues
			if values[naming.ReleaseGroup] != tc.group || values[naming.Platform] != tc.platform || values[naming.Languages] != tc.languages || values[naming.SubtitleMode] != tc.mode || values[naming.Resolution] != tc.resolution {
				t.Fatalf("复用文件识别改变了规格继承顺序：%+v", values)
			}
			if out.Specs[out.Files[0].ID].Fansub != tc.group {
				t.Fatal("清单规格未保留最终字幕组", out.Specs)
			}
		})
	}
}
