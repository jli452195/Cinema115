package service

import (
	"context"
	"reflect"
	"testing"

	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/mikan"
)

func TestResourceSearchSpecsRealPublisherTitles(t *testing.T) {
	for _, tc := range []struct {
		title, quality, mode string
		languages            []string
	}{
		{"[星空字幕组][星合之空 / Hoshiai no Sora / 星合の空][01-12修正合集][简繁日外挂][1080p][WEBrip][元旦快乐]", "1080p", "external", []string{"ja", "zh-Hans", "zh-Hant"}},
		{"[星空字幕组&LoliHouse] 弦音 -联系的一箭- / Tsurune S2 - 13 [WebRip 1080p HEVC-10bit AAC][简繁日内封字幕][END]", "1080p", "soft", []string{"ja", "zh-Hans", "zh-Hant"}},
		{"[DBD-Raws][夏日重现/Summer Time Render][01-25TV全集][1080P][BDRip][HEVC-10bit][简繁日双语外挂][FLAC][MKV]", "1080p", "external", []string{"ja", "zh-Hans", "zh-Hant"}},
		{"[诸神字幕组][夏日重现][25][简日双语字幕][720P][CHS HEVC MP4]", "720p", "", []string{"ja", "zh-Hans"}},
		{"【喵萌奶茶屋】[夏日重现][01-25][BDRip][1080p][繁日双语]", "1080p", "", []string{"ja", "zh-Hant"}},
		{"【幻樱字幕组】【夏日重现】【1-25】【GB_MP4】【1920X1080】【合集】", "1080p", "", []string{"zh-Hans"}},
		{"【極影字幕社】【弦音】【04】BIG5 MP4_720P", "720p", "", []string{"zh-Hant"}},
		{"[Yaku-Raws]Tsurune -Tsunagari no Issha- 04 (BS11 1440X1080i MPEG2 AAC) 弦音 -联系的一箭- 04 ts档", "1080i", "", nil},
		{"[Yaku-Raws]Tsurune -Tsunagari no Issha- 04 (BS11 1280x720 x264 AAC) 弦音 -联系的一箭- 04", "720p", "", nil},
		{"[北宇治字幕组] 药屋少女的呢喃 [48][WebRip][HEVC_AAC][简繁日内封]", "", "soft", []string{"ja", "zh-Hans", "zh-Hant"}},
		{"[組] 動畫 [01][繁日雙語外掛字幕][1080P]", "1080p", "external", []string{"ja", "zh-Hant"}},
	} {
		spec := resourceSearchSpec(tc.title)
		if spec.Resolution != tc.quality || spec.SubtitleMode != tc.mode || !reflect.DeepEqual(spec.Languages, tc.languages) {
			t.Errorf("%s: got %+v, want quality=%s mode=%s languages=%v", tc.title, spec, tc.quality, tc.mode, tc.languages)
		}
	}
	for title, want := range map[string]string{
		"[Show][1080p/1080i]": "", "[Show][1080p 720p]": "", "[Show][1920x1080 720p]": "",
		"[Show][AI2160p]": "", "[Show][1080pFake]": "", "[Show][2160p 4k]": "2160p",
		"[Show][1080P_x264]": "1080p", "[Show][1920×1080i]": "1080i", "[Show][8K]": "4320p",
	} {
		if got := resourceSearchSpec(title).Resolution; got != want {
			t.Errorf("%s: quality=%q want %q", title, got, want)
		}
	}
	for _, title := range []string{"[简繁字幕组] 每日字幕故事 [01][1080p]", "[组] 每日动画 [01][ASS内封][MKV]", "[组] 动画 [01][MP4]"} {
		got := resourceSearchSpec(title)
		if len(got.Languages) != 0 || got.SubtitleMode == "external" {
			t.Fatal("inferred language or external subtitles from a name/container/format", title, got)
		}
	}
}

func TestResourceSearchSpecsDoNotAddUpstreamRequests(t *testing.T) {
	calls := 0
	s := NewMikanService(mikanReaderFunc(func(context.Context, string, string) (mikan.Result, error) {
		calls++
		return mikan.Result{Items: []mikan.Item{{ID: "one", Title: "[Show][01][1280x720][繁日双语外挂]"}}, Groups: []mikan.Group{}}, nil
	}), nil)
	for i := 0; i < 2; i++ {
		result, err := s.Search(context.Background(), MikanInput{Query: "Show"})
		if err != nil || result.Specs["one"].Resolution != "720p" || result.Specs["one"].SubtitleMode != "external" || len(result.Specs["one"].Languages) != 2 || calls != 1 {
			t.Fatal("search hints triggered new reads or lost cached metadata", err, result.Specs, calls)
		}
		result.Specs["one"] = ResourceSearchSpec{}
	}
	g := NewGardenService(&gardenSearchFake{})
	g.spacing = 0
	result, err := g.SearchResources(context.Background(), GardenSearchInput{})
	if err == nil {
		t.Fatal("empty search unexpectedly succeeded", result)
	}
	result, err = g.SearchResources(context.Background(), GardenSearchInput{SearchOptions: garden.SearchOptions{Query: "Show"}})
	if err != nil || result.Specs["dmhy:123"].Resolution != "1080p" {
		t.Fatal("Garden search did not return local hints", err, result.Specs)
	}
}
