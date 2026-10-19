package animeparse

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTitleOrdinaryNumbering(t *testing.T) {
	for _, tc := range []struct {
		title, raw string
		episode    int
		version    string
	}{
		{"[字幕组] 测试 第30话 1080p", "30", 30, ""},
		{"[组] 测试 第 0001 集 [720P]", "0001", 1, ""},
		{"[Group] Show [01][1080p][HEVC x265 10bit]", "01", 1, ""},
		{"[Group] Show - 06 [1080p]", "06", 6, ""},
		{"[Group] Show S02E001 [1080p]", "001", 1, ""},
		{"[Group] Show EP 26 [720p]", "26", 26, ""},
		{"[Group] Show Episode 100 [4K]", "100", 100, ""},
		{"[Group] Show E99999 [1080p]", "99999", 99999, ""},
		{"[Group] Show [01v2][1080p]", "01v2", 1, "v2"},
		{"[Group] Show [01 V3][1080p]", "01 V3", 1, "V3"},
		{"[Group] Show [13 END] [1080p]", "13", 13, ""},
		{"[Group] Show 【25】[1080p] 修正版", "25", 25, "修正版"},
		{"[Group] Show 26 GB 1080p", "26", 26, ""},
		{"[Group] Show 18 [1080p]", "18", 18, ""},
		{"[Group] Show 第2026话 [1080p]", "2026", 2026, ""},
		{"[Group] Show [2026] [01][1080p]", "01", 1, ""},
		{"[Group] Show S01E01.mkv", "01", 1, ""},
		{"[ANi] Tantei wa Mō Shindeiru / 侦探已经，死了。 Season 2 - 02 [1080P][Baha][WEB-DL][AAC AVC][CHT][MP4]", "02", 2, ""},
		{"[ANi] 侦探已经，死了。 Season 2 - 01 [1080P][Baha][WEB-DL][AAC AVC][CHT][MP4]", "01", 1, ""},
		{"[Group] Show season 02 - 03 [1080p]", "03", 3, ""},
		{"[Group] Show Season 12 - 01v2 [1080p]", "01v2", 1, "v2"},
		{"[Group] Show [Season 2] - 04 [1080p]", "04", 4, ""},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got, err := Title(tc.title)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != "episode" || got.Episode == nil || *got.Episode != tc.episode || got.RawEpisode != tc.raw || got.VersionLabel != tc.version {
				t.Fatalf("unexpected parse: %+v", got)
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTitleUnsupportedAndAmbiguousNumbering(t *testing.T) {
	for _, tc := range []struct{ title, kind, raw string }{
		{"[组] 动画 [12.5] [1080p]", "special", "12.5"},
		{"[组] 动画 - 07A [1080p]", "special", "07A"},
		{"[组] 动画 [06Bv2] [1080p]", "special", "06Bv2"},
		{"[组] 动画 [06B V2] [1080p]", "special", "06B V2"},
		{"[组] 动画 [SP01] [1080p]", "special", "SP01"},
		{"[组] 动画 [OVA 2] [1080p]", "special", "OVA 2"},
		{"[组] 动画 [SPECIAL01] [1080p]", "special", "SPECIAL01"},
		{"[组] 动画 [01] 样片 [1080p]", "special", "01 / 样片"},
		{"[Group] Show特典 [01] [1080p]", "special", "特典 / 01"},
		{"[组] 动画 [01-12] [1080p]", "batch", "01-12"},
		{"[组] 动画 第01～12话 [1080p]", "batch", "01～12"},
		{"[组] 动画 第01-第03话 [1080p]", "batch", "01-第03"},
		{"[组] 动画 EP01-EP02 [1080p]", "batch", "01-EP02"},
		{"[组] 动画 S02E01-E03 [1080p]", "batch", "01-E03"},
		{"[组] 动画 [01+02] [1080p]", "batch", "01+02"},
		{"[组] 动画 [01/02] [1080p]", "batch", "01/02"},
		{"[组] 动画 [01][全集][1080p]", "batch", "01 / 全集"},
		{"[组] 动画 [59][3rd - 09][1080p]", "composite", "59 / 3rd - 09"},
		{"[组] 动画 [01][02][1080p]", "composite", "01 / 02"},
		{"[组] 动画 第1话 [01] [1080p]", "composite", "1 / 01"},
		{"[组] 动画 [00][1080p]", "unknown", "00"},
		{"[组] 动画 [100000][1080p]", "unknown", "100000"},
		{"[组] 动画 第零话 [1080p]", "unknown", ""},
		{"[Group] Show Season 2 - 01-12 [1080p]", "batch", "01-12"},
		{"[Group] Show Season 2 - 01+02 [1080p]", "batch", "01+02"},
		{"[Group] Show Season 2 [01][02][1080p]", "composite", "01 / 02"},
		{"[Group] Show [59][Season 3 - 09][1080p]", "composite", "59 / 09"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got, err := Title(tc.title)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.kind || got.Episode != nil || got.RawEpisode != tc.raw {
				t.Fatalf("unexpected parse: %+v", got)
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTitleDoesNotGuessFromMetadata(t *testing.T) {
	for _, title := range []string{
		"[组] Show [2026] [1080p] [x265] [10bit] [2.5GB] [12345678]",
		"[组] Show S02 1080p HEVC AAC",
		"[组] Show 2nd Season [1080p]",
		"[组] Show [720] [1080] [2160] [264] [265]",
		"[组] Show [1920x1080] [AV1]",
		"[组] Show [V2] [1080p]",
		"[组] Show HEVC-10bit [1080p]",
		"[组] Show [01XYZ] [1080p]",
		"[组] Show [EP01xyz] [1080p]",
		"[组] Show v2 2026",
		"[组] Show - 1080p",
		"[组] Show - 07C [1080p]",
		"[Group] Show Season 02 [1080p]",
		"[Group] Show Season 2 [1080p]",
	} {
		got, err := Title(title)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind == "episode" || got.Episode != nil {
			t.Fatalf("metadata guessed as an episode: %q => %+v", title, got)
		}
	}
}

func TestTitleResolution(t *testing.T) {
	for _, tc := range []struct{ labels, want string }{
		{"[720P]", "720p"}, {"[1080p]", "1080p"}, {"[2160p]", "2160p"},
		{"[4K]", "2160p"}, {"[4K/2160p]", "2160p"},
		{"[1080p/720p]", "unknown"}, {"[720p][2160p]", "unknown"},
		{"[1080i]", "unknown"}, {"[1080p/1080i]", "unknown"},
		{"[480p/1080p]", "unknown"}, {"[8K/2160p]", "unknown"},
		{"[1920x1080]", "1080p"}, {"[x1080p]", "unknown"}, {"[AV1]", "unknown"},
	} {
		got, err := Title("[组] 测试 [01] " + tc.labels)
		if err != nil {
			t.Fatal(err)
		}
		if got.Resolution == nil || *got.Resolution != tc.want {
			t.Fatalf("%s => %+v", tc.labels, got)
		}
	}
}

func TestTitleVersionsAreIndependentReleases(t *testing.T) {
	for _, title := range []string{"[组] Show [01] [AV1]", "[组] Show [01v2] [AV1]", "[组] Show [01] [rev3] 重发"} {
		got, err := Title(title)
		if err != nil {
			t.Fatal(err)
		}
		if got.Episode == nil || *got.Episode != 1 {
			t.Fatalf("version replaced episode: %+v", got)
		}
		if strings.Contains(got.VersionLabel, "V1") {
			t.Fatal("codec misread as a release version")
		}
	}
}

func TestTitleRealMikanSamples(t *testing.T) {
	data, err := os.ReadFile("testdata/mikan_titles.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []struct {
		Label      string `json:"label"`
		Title      string `json:"title"`
		Kind       string `json:"kind"`
		Episode    int    `json:"episode"`
		Resolution string `json:"resolution"`
	}
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) < 10 {
		t.Fatal("missing representative samples")
	}
	for _, tc := range samples {
		t.Run(tc.Label, func(t *testing.T) {
			got, err := Title(tc.Title)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.Kind || got.Resolution == nil || *got.Resolution != tc.Resolution || (tc.Episode == 0) != (got.Episode == nil) || got.Episode != nil && *got.Episode != tc.Episode {
				t.Fatalf("unexpected parse for %q: %+v", tc.Title, got)
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTitleInputAndEvidenceLimits(t *testing.T) {
	for _, input := range []string{"", "   ", "bad\x00[01]", "bad\n[01]", string([]byte{0xff}), strings.Repeat("字", 1025)} {
		if _, err := Title(input); !errors.Is(err, ErrInput) {
			t.Fatalf("invalid title accepted: %q, %v", input, err)
		}
	}
	got, err := Title(strings.Repeat("[01]", 100))
	if err != nil || got.Kind != "unknown" || got.Episode != nil || got.RawEpisode != "" {
		t.Fatalf("oversized ambiguous evidence: %+v %v", got, err)
	}
}

func FuzzTitleInvariants(f *testing.F) {
	for _, seed := range []string{"[组] 动画 [01v2][1080p]", "[59][3rd - 09]", "[26-38+SP26-38(前半全集)]", "第01话", "S01E02-E03", "[2026] [AV1]"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, title string) {
		got, err := Title(title)
		if err != nil {
			if !errors.Is(err, ErrInput) {
				t.Fatal(err)
			}
			return
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("invalid parser output: %+v: %v", got, err)
		}
		if (got.Kind == "episode") != (got.Episode != nil) {
			t.Fatalf("unsafe episode result: %+v", got)
		}
	})
}
