package mediafacts

import "testing"

func TestParseStandardCoordinatesPreservesDeclarations(t *testing.T) {
	for _, tc := range []struct {
		stem string
		want Numbering
	}{
		{"绯弹的亚里亚AA - S01E02 - 2弹 危险的关系", Numbering{1, 2, 0}},
		{"绯弹的亚里亚AA - S01E02 - 3弹 危险的关系", Numbering{1, 2, 0}},
		{"魔法老师！ - S01E09 - 第 9 集", Numbering{1, 9, 0}},
		{"魔法老师！？ - S01E09 - 第 9 话 “听说要去露营。”", Numbering{1, 9, 0}},
		{"恶魔高校D×D - S00E16 - 恶魔高校D×D BorN OAD 第13话 无法苏醒的不死鸟", Numbering{0, 16, 0}},
		{"出包王女 - S00E01 - OVA#1「梨斗，变成女生」", Numbering{0, 1, 0}},
		{"七人魔法使 - S00E01 - OVA 七大罪与七位魔法使", Numbering{0, 1, 0}},
		{"传颂之物 - S00E13 - OVA Utawarerumono Picture Drama 2", Numbering{0, 13, 0}},
		{"精灵使的剑舞 - S00E06 - 特典小型OVA Vol.6-精灵使的休息", Numbering{0, 6, 0}},
		{"Show.S01E01 - OVA 第02集", Numbering{1, 1, 0}},
		{"Show.S01E1218", Numbering{1, 1218, 0}},
		{"Show.S01E02-E03 - 两话合一", Numbering{1, 2, 3}},
		{"Show.S01E02-E03集", Numbering{1, 2, 3}},
		{"Show.S01E02-03", Numbering{1, 2, 3}},
		{"Show.S01E02 - 03", Numbering{1, 2, 3}},
		{"Show.S01E02+E03", Numbering{1, 2, 3}},
		{"Show.s01e02v2-E03v3", Numbering{1, 2, 3}},
		{"Show.S02E04.2026.1080p", Numbering{2, 4, 0}},
		{"Show.S02E04 - 1920x1080", Numbering{2, 4, 0}},
		{"Show.S02E04-4K", Numbering{2, 4, 0}},
		{"Show.S02E04.420P", Numbering{2, 4, 0}},
		{"冰海战记 - S02E08 - 7³ACG", Numbering{2, 8, 0}},
		{"Show.S02E04 - 7ACG - 8XYZ", Numbering{2, 4, 0}},
		{"Show.S1000E10000", Numbering{1000, 10000, 0}},
		{"蓝色星球3 (2026) - S01E02 - 1080p.BBC iPlayer.WEB-DL.H264.AAC 2.0(1)", Numbering{1, 2, 0}},
	} {
		t.Run(tc.stem, func(t *testing.T) {
			got := ParseStandardCoordinates(tc.stem)
			if !got.Found || !got.Valid || got.Code != "NUMBERING_EXPLICIT" || got.Numbering != tc.want {
				t.Fatalf("got %+v; want %+v", got, tc.want)
			}
			span := tc.stem[got.Start:got.End]
			if len(span) == 0 || span[0] != 'S' && span[0] != 's' {
				t.Fatalf("原文位置无效: %+v", got)
			}
		})
	}
}

func TestParseStandardCoordinatesRejectsInvalidOrCompetingCoordinates(t *testing.T) {
	for _, stem := range []string{
		"Show.S01E02 S02E03", "Show.S01E02 E03", "Show E03 S01E02", "Show.S01E02.5", "Show.S01E00",
		"Show.S01E02-01", "Show.S01E02-02", "Show.S01E02-E03+05", "Show.S01E02+04", "Show.S01E02/E03",
		"Show.S01E02A", "Show.S01E02foo", "Show.S01E02-03foo", "Show.S01E", "Show.S01E10001", "Show.S1001E02",
		"Show.S00001E02", "Show.S01E000002", "Show.S01E02 [03]", "Show.S01E02 - OVA [03.5]", "Show.S01E02 - OVA [03-04]",
		"Show.S01E02 - OVA E03", "Show.S01E02 - OVA - 03", "Show.S01E02 - 7ACG-08", "Show.S01E02 - 7ACG.5",
		"Show.S01E02-1080pixels", "Show.S01E02.420Pixels", "Show.S01E02 - 03v1234", "Show.S01E02 - 03Av2",
		"Show.S01E02(1)", "Show.S01E02 - OVA (1)", "Show.S01E02 - OVA(02)",
	} {
		t.Run(stem, func(t *testing.T) {
			got := ParseStandardCoordinates(stem)
			if !got.Found || got.Valid || got.Numbering != (Numbering{}) || got.Code != "NUMBERING_INVALID" && got.Code != "NUMBERING_CONFLICT" {
				t.Fatalf("无效或冲突声明不能回落为弱编号: %+v", got)
			}
		})
	}
}

func TestParseStandardCoordinatesMissing(t *testing.T) {
	for _, stem := range []string{"Show - 01", "Show OVA13", "Show.XS01E02", "Planet 51", "Show Season 01 E02"} {
		if got := ParseStandardCoordinates(stem); got.Found || got.Valid || got.Code != "NUMBERING_MISSING" {
			t.Fatalf("%q: %+v", stem, got)
		}
	}
}

func TestParseFilenameUsesCoordinateSpanForIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, title, year string
		season            int
	}{
		{"绯弹的亚里亚AA - S01E02 - 2弹 危险的关系.mkv", "绯弹的亚里亚AA", "", 1},
		{"作品 (2024) - S00E16 - OAD 第13话 无法苏醒的不死鸟 2014.mkv", "作品", "2024", 0},
		{"[TrinityBlood] - S01E01 - 分集标题 2014.mkv", "TrinityBlood", "", 1},
		{"S01E02 - 另一作品 (2026).mkv", "", "", 1},
		{"Show.2024.S01E02.Title.2017.mkv", "Show", "2024", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := ParseFilename(tc.name)
			if facts.Identity.Name != tc.title || facts.Identity.Year != tc.year || facts.Identity.Kind != "tv" || facts.Identity.Season != tc.season || !facts.Identity.SeasonExplicit || !facts.Coordinate.Valid {
				t.Fatalf("作品应来自标准坐标之前: %+v", facts)
			}
		})
	}
	facts := ParseFilename("Show - S01E02 - 分集标题 {tmdb-42} [动画].mkv")
	if facts.Identity.TMDBID != 42 || facts.Identity.ContentType != "animation" {
		t.Fatalf("明确作品标签仍应独立提取: %+v", facts)
	}
	facts = ParseFilename("Show {tmdb-41} - S01E02 - 分集标题 {tmdb-42}.mkv")
	if facts.Identity.Conflict != "TMDB_ID_CONFLICT" {
		t.Fatalf("明确作品标签冲突不能消失: %+v", facts)
	}
}
