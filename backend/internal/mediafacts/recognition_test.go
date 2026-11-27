package mediafacts

import (
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

func TestRecognizedPlatformDistinguishesMissingFromConflict(t *testing.T) {
	rules, err := recognition.Compile("", "NF ~= NF|Netflix\nIQ ~= iQIYI|爱奇艺")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		present    bool
	}{
		{"Show.S01E01.NF.WEB-DL.mkv", "NF", true},
		{"Show.S01E01.NF.iQIYI.WEB-DL.mkv", "", true},
		{"Show.S01E01.WEB-DL.mkv", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ValuesWithRecognition(tc.name, releaseparse.Spec{}, rules)
			if err != nil {
				t.Fatal(err)
			}
			if got, present := values[naming.Platform]; got != tc.want || present != tc.present {
				t.Fatalf("平台值 = %q，已声明 = %v；期望 %q，%v", got, present, tc.want, tc.present)
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
