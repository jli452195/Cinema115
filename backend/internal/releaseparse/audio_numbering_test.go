package releaseparse

import "testing"

func TestTitleAudioChannelsDoNotBecomeEpisodes(t *testing.T) {
	for _, tc := range []struct {
		title, raw string
		episode    int
	}{
		{"花朵盛开 (2026).S01E26.1080p.Hami Video.WEB-DL.H264.AAC 2.0.mkv", "26", 26},
		{"极恶老大 (2020).S03E07.1080p.Amazon.WEB-DL.H264.DDP 5.1.mkv", "07", 7},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got, err := Title(tc.title)
			if err != nil || got.Kind != "episode" || got.Episode == nil || *got.Episode != tc.episode || got.RawEpisode != tc.raw {
				t.Fatalf("声道数字污染了标准集号: %+v, err=%v", got, err)
			}
		})
	}
	for _, audio := range []string{
		"AAC 2.0", "aac 2.1", "HE-AAC 2.0", "DDP 5.1", "DD+ 5.1", "DD 5.1",
		"AC3 5.1", "AC-3 5.1", "EAC3 5.1", "E-AC-3 5.1", "DTS 5.1",
		"DTS-HD MA 7.1", "DTS-X 7.1", "TrueHD Atmos 7.1.4", "FLAC 2.0", "OPUS 2.0", "PCM 2.0",
		"AAC.2.0", "AAC_2.0", "AAC-2.0", "DDP5.1", "[AAC 2.0]", "(DDP 5.1)",
	} {
		t.Run(audio, func(t *testing.T) {
			got, err := Title("Show.S03E07.1080p.WEB-DL.H264." + audio)
			if err != nil || got.Kind != "episode" || got.Episode == nil || *got.Episode != 7 || got.RawEpisode != "07" {
				t.Fatalf("音频规格不能成为额外集号: %+v, err=%v", got, err)
			}
			got, err = Title("Show 1080p " + audio)
			if err != nil || got.Kind != "unknown" || got.Episode != nil || got.RawEpisode != "" {
				t.Fatalf("仅有音频规格不能推断集号: %+v, err=%v", got, err)
			}
		})
	}
}

func TestTitleAudioMetadataPreservesOtherNumbering(t *testing.T) {
	for _, tc := range []struct {
		title, kind, raw string
	}{
		{"Show.S03E07 DDP 5.1 [08]", "composite", "07 / 08"},
		{"Show.S03E07 DDP 5.1 E08", "composite", "07 / 08"},
		{"Show.S03E07 DDP 5.1 8.5", "special", "07 / 8.5"},
		{"Show.S03E07 DDP 5.1 + 08", "batch", "07 / 5.1 + 08"},
		{"Show.S03E07 AAC 2.0-08", "batch", "07 / 2.0-08"},
		{"Show.S03E07 AAC 2.0,08", "batch", "07 / 2.0,08"},
		{"Show.S03E07 5.1", "special", "07 / 5.1"},
		{"Show.S03E07 NOTAAC 2.0", "special", "07 / 2.0"},
		{"Show.S03E07 AAC 2.01", "special", "07 / 2.01"},
		{"Show.S03E07 AAC 2.0v2", "special", "07 / 2.0v2"},
		{"Show.S03E07 AAC [2.0]", "special", "07 / 2.0"},
		{"Show.S03E07 AAC 12.5", "special", "07 / 12.5"},
		{"Show.S01E12.5 AAC 2.0", "special", "12.5"},
		{"Show.S01E01-03+05 AAC 2.0", "batch", "01-03+05"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got, err := Title(tc.title)
			if err != nil || got.Kind != tc.kind || got.Episode != nil || got.RawEpisode != tc.raw {
				t.Fatalf("音频元数据掩盖了额外编号: %+v, err=%v", got, err)
			}
		})
	}
}
