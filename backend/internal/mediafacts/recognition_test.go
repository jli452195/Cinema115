package mediafacts

import (
	"reflect"
	"testing"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/recognition"
	"strm115/backend/internal/releaseparse"
)

func TestRecognitionDoesNotChangeRSSSourceGroupValues(t *testing.T) {
	name := "[喵萌奶茶屋&LoliHouse] Show.S01E01.NF.WEB-DL.mkv"
	legacy := Values(name, releaseparse.Spec{}, "LoliHouse")
	if legacy[naming.ReleaseGroup] != "LoliHouse" || legacy[naming.Platform] != "NF" {
		t.Fatal("RSS 既有来源组及平台行为被改变", legacy)
	}
	rules, err := recognition.Compile("喵萌奶茶屋\nLoliHouse", "Netflix ~= NF")
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := ValuesWithRecognition(name, releaseparse.Spec{}, rules)
	if err != nil || ordinary[naming.ReleaseGroup] != "喵萌奶茶屋&LoliHouse" || ordinary[naming.Platform] != "Netflix" {
		t.Fatal("普通整理没有独立使用识别词表", ordinary, err)
	}
	legacy = Values(name, releaseparse.Spec{}, "LoliHouse")
	if legacy[naming.ReleaseGroup] != "LoliHouse" || legacy[naming.Platform] != "NF" {
		t.Fatal("普通识别污染了 RSS 原接口", legacy)
	}
}

func TestRecognizedReleaseReuseMatchesFreshRecognition(t *testing.T) {
	rules, err := recognition.Compile("规范组 ~= LoliHouse\n(?:桜|樱)都字幕[组組]", "NF ~= NF|Netflix\nIQ ~= iQIYI|爱奇艺")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"[LoliHouse] Show.S01E01.NF.WEB-DL.mkv",
		"[樱都字幕組] Show.1080p.HEVC.mkv",
		"Show.S01E01.NF.iQIYI.WEB-DL.mkv",
		"Show.S01E01.WEB-DL.mkv",
	} {
		t.Run(name, func(t *testing.T) {
			original, err := ValuesWithRecognition(name, releaseparse.Spec{}, rules)
			if err != nil {
				t.Fatal(err)
			}
			before := make(map[naming.Field]string, len(original))
			for field, value := range original {
				before[field] = value
			}
			hint := releaseparse.Spec{Resolution: "2160p", Languages: []string{"zh-Hant"}, SubtitleMode: "soft"}
			want, err := ValuesWithRecognition(name, hint, rules)
			if err != nil {
				t.Fatal(err)
			}
			got := ValuesWithRecognizedRelease(name, hint, original)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("补充规格必须等价于同一词表重新识别：新=%+v，旧=%+v", got, want)
			}
			if !reflect.DeepEqual(original, before) {
				t.Fatal("补充规格修改了首轮已确认的文件事实")
			}
		})
	}
}

func TestEmptyRecognitionKeepsTechnicalFactsWithoutBuiltInMatches(t *testing.T) {
	rules, err := recognition.Compile("", "")
	if err != nil {
		t.Fatal(err)
	}
	values, err := ValuesWithRecognition("[LoliHouse] Show.S01E01.NF.WEB-DL.1080p.HEVC.mkv", releaseparse.Spec{}, rules)
	if err != nil || values[naming.ReleaseGroup] != "" || values[naming.Platform] != "" || values[naming.Source] != "WEB-DL" || values[naming.Resolution] != "1080p" || values[naming.VideoCodec] != "HEVC" {
		t.Fatal("空词表只能关闭组名和平台识别，不能影响其他文件规格", values, err)
	}
}
