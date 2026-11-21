package mediafacts

import (
	"testing"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
)

func TestVideoSubtitleNamingReadsCanonicalFields(t *testing.T) {
	for _, tt := range []struct{ name, language, mode string }{
		{"Show.S01E02.1080p.AAC.[zh-TW+zh-CN].softsub-Group.mkv", "zh-CN+zh-TW", "softsub"},
		{"Show.S01E02.1080p.zh-CN.zh-HK+zh-TW.hardsub.mkv", "zh-CN+zh-TW+zh-HK", "hardsub"},
		{"Show.S01E02.1080p.en.external.mkv", "en", "external"},
		{"Show.[zh-SG].external.mkv", "zh-SG", "external"},
		{"Show.S01E02.1080p.[zh-en].mkv", "zh+en", ""},
		{"Show.S01E02.1080p.AAC[CHS][CHT].mkv", "zh-TW", ""},
		{"Show.S01E02.1080p.[简繁内封].hardsub.mkv", "zh-CN+zh-TW", ""},
		{"An.External.Cause.S01E02.1080p.mkv", "", ""},
		{"Show.S01E02.1080p.zh-CNish.externalized.mkv", "", ""},
		{"Show.S01E02.1080p.zh-CN.bilingual.effects.ass.mkv", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := Values(tt.name, releaseparse.Spec{}, "")
			if got := values[naming.Languages]; got != tt.language {
				t.Errorf("字幕语言 = %q，期望 %q", got, tt.language)
			}
			if got := values[naming.SubtitleMode]; got != tt.mode {
				t.Errorf("字幕形式 = %q，期望 %q", got, tt.mode)
			}
		})
	}
}

func TestNormalizeVideoSubtitleValuesReadsLegacySnapshot(t *testing.T) {
	values := map[naming.Field]string{naming.Languages: "简繁英", naming.SubtitleMode: "内封"}
	for range 2 {
		NormalizeVideoSubtitleValues(values)
		if values[naming.Languages] != "zh-CN+zh-TW+en" || values[naming.SubtitleMode] != "softsub" {
			t.Fatalf("字幕字段归一化错误：%v", values)
		}
	}
}
