package releaseparse

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSpecificationPublisherMetadata(t *testing.T) {
	tests := []struct {
		title string
		want  Spec
	}{
		{"[绿茶字幕组] 每日动画 [01][1080p][简体内嵌][HEVC 10bit][MKV]", Spec{Resolution: "1080p", Languages: []string{"zh-Hans"}, SubtitleMode: "hard", Codec: "h265", BitDepth: "10", Container: "mkv"}},
		{"[绿茶字幕组] 每日动画 [01][1080p][繁體內嵌][H.264][MP4]", Spec{Resolution: "1080p", Languages: []string{"zh-Hant"}, SubtitleMode: "hard", Codec: "h264", Container: "mp4"}},
		{"[绿茶字幕组] 每日动画 [01] [1920x1080] 简繁日内封 HEVC 10bit MKV", Spec{Resolution: "1080p", Languages: []string{"ja", "zh-Hans", "zh-Hant"}, SubtitleMode: "soft", Codec: "h265", BitDepth: "10", Container: "mkv"}},
		{"[组] 每日动画 [01][CHS&CHT][x265][12bit]", Spec{Languages: []string{"zh-Hans", "zh-Hant"}, Codec: "h265", BitDepth: "12"}},
		{"[每日动画][01][1080p]", Spec{Resolution: "1080p"}},
		{"[简繁字幕组] 动画 [01]", Spec{}},
		{"[组] 动画 [01][AVC HEVC][MP4 MKV][内嵌 外挂][1080p 720p]", Spec{}},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := Specification(tt.title)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v want %+v", got, tt.want)
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSpecificationStableKeyDoesNotMutateInput(t *testing.T) {
	a := Spec{Languages: []string{"zh-Hans", "ja"}}
	b := Spec{Languages: []string{"ja", "zh-Hans"}}
	if SpecKey(a) != SpecKey(b) || a.Languages[0] != "zh-Hans" {
		t.Fatal("normalization mutated caller or changed semantic key")
	}
}

func TestSpecificationSilverBulletRealTitles(t *testing.T) {
	data, err := os.ReadFile("testdata/silver_bullet_titles.json")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	if err = json.Unmarshal(data, &titles); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	ambiguous := 0
	for _, title := range titles {
		p, err := Title(title)
		if err != nil {
			t.Fatal(err)
		}
		if p.SpecAmbiguous {
			ambiguous++
			continue
		}
		counts[SpecLabel(p.Spec)]++
	}
	want := map[string]int{"1080p 简繁日 MKV": 34, "1080p 繁日 MP4": 30, "1080p 简日 MP4": 30}
	if !reflect.DeepEqual(counts, want) || ambiguous != 6 {
		t.Fatalf("profiles=%v ambiguous=%d", counts, ambiguous)
	}
}

func TestSpecificationChineseLanguageMarkersAreBounded(t *testing.T) {
	for _, title := range []string{"[组][日常的简繁生活][01][1080p]", "[组][每日字幕故事][01][1080p]", "[组][第01集 简单的日语故事][1080p]"} {
		if got := Specification(title); len(got.Languages) > 0 {
			t.Fatalf("series/episode name interpreted as language: %s %+v", title, got)
		}
	}
	for _, tt := range []struct {
		title     string
		languages []string
	}{
		{"[组] 动画 [01][繁體][MP4]", []string{"zh-Hant"}},
		{"[组] 动画 [01][繁日雙語MP4]", []string{"ja", "zh-Hant"}},
	} {
		got := Specification(tt.title)
		if got.Container != "mp4" || !reflect.DeepEqual(got.Languages, tt.languages) {
			t.Fatal(got)
		}
	}
}
