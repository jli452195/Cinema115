package mediafacts

import (
	"strings"
	"testing"
)

func TestParseStandardCoordinatesMergesEquivalentDeclarations(t *testing.T) {
	for _, tc := range []struct {
		stem, first string
		want        Numbering
	}{
		{"Show - S02E10 - S02E10", "S02E10", Numbering{2, 10, 0}},
		{"Show.S02E10.s02e10.S02E10", "S02E10", Numbering{2, 10, 0}},
		{"Show - s002e00010 - S02E10", "s002e00010", Numbering{2, 10, 0}},
		{"Show - S02E10v2 - s002e00010v3", "S02E10v2", Numbering{2, 10, 0}},
		{"Show.S02E10-S02E10", "S02E10", Numbering{2, 10, 0}},
		{"Show - [S02E10] - [S02E10]", "S02E10", Numbering{2, 10, 0}},
		{"Show - S02E10-E12 - s002e00010~00012", "S02E10-E12", Numbering{2, 10, 12}},
		{"Show - S02E10+E11 - S2E10&11", "S02E10+E11", Numbering{2, 10, 11}},
		{"作品 (2024) - s002e00010～E00012 - S02E10至12 - 分集标题 2017", "s002e00010～E00012", Numbering{2, 10, 12}},
		{"Show - S02E10 - 1920x1080 - S02E10 - 7³ACG", "S02E10", Numbering{2, 10, 0}},
		{"作品 - S00E01 - S00E01 - OVA#13", "S00E01", Numbering{0, 1, 0}},
		{"Show - S02E10 - S02E10 - Example", "S02E10", Numbering{2, 10, 0}},
	} {
		t.Run(tc.stem, func(t *testing.T) {
			got := ParseStandardCoordinates(tc.stem)
			if !got.Found || !got.Valid || got.Code != "NUMBERING_EXPLICIT" || got.Numbering != tc.want {
				t.Fatalf("相同完整坐标应合并: got %+v; want %+v", got, tc.want)
			}
			if got.Start != strings.Index(tc.stem, tc.first) || tc.stem[got.Start:got.End] != tc.first {
				t.Fatalf("必须保留第一份声明的原始字节区间: %+v", got)
			}
		})
	}
}

func TestParseStandardCoordinatesRejectsCompetingRepeatedDeclarations(t *testing.T) {
	for _, stem := range []string{
		"Show S02E10 - S03E10",
		"Show S02E10 - S02E11",
		"Show S02E10-E12 - S02E10-E13",
		"Show S02E10 - S02E10-E12",
		"Show S02E10-E12 - S02E10",
		"Show S02E10 - S02E11 - S02E10",
		"Show S02E10 - S02E10 - S02E11",
		"Show S02E00 - S02E10",
		"Show S02E10 - S02E00",
		"Show S02E10 - S02E00 - S02E10",
		"Show S02E00 - S02E00",
		"Show S00002E10 - S02E10",
		"Show S02E10 - S00002E10",
		"Show S1001E10 - S02E10",
		"Show S02E10 - S1001E10",
		"Show S02E10001 - S02E10",
		"Show S02E10 - S02E10001",
		"Show S02E000010 - S02E10",
		"Show S02E10 - S02E000010",
		"Show S02E - S02E10",
		"Show S02E10 - S02E",
		"Show S02E10v1234 - S02E10",
		"Show S02E10 - S02E10v1234",
		"Show S02E10A - S02E10",
		"Show S02E10 - S02E10.5",
		"Show S02E10-E09 - S02E10",
		"Show S02E10 - S02E10-E09",
		"Show S02E10-E12 - S02E10+E12",
		"Show S02E10-E12 - S02E10-E12-E13",
		"Show E03 - S02E10 - S02E10",
		"Show S02E10 - E03 - S02E10",
		"Show S02E10 - S02E10 - E03",
		"Show S02E10 - [03] - S02E10",
		"Show S02E10 - S02E10 - [03]",
		"Show S02E10 - OVA - 03 - S02E10",
		"Show S02E10 - S02E10 - OVA - 03",
		"Show S02E10/E03 - S02E10",
		"Show S02E10 - S02E10/E03",
		"Show S02E10-E - S02E10",
		"Show S02E10 - S02E10-E",
		"Show S02E10-EP - S02E10",
		"Show S02E10 - S02E10-EP",
		"Show S02E10-E12-E - S02E10-E12",
		"Show S02E10-E12 - S02E10-E12-EP",
		"Show S02E10-E",
		"Show S02E10-EP",
	} {
		t.Run(stem, func(t *testing.T) {
			got := ParseStandardCoordinates(stem)
			if !got.Found || got.Valid || got.Numbering != (Numbering{}) || got.Code != "NUMBERING_INVALID" && got.Code != "NUMBERING_CONFLICT" {
				t.Fatalf("重复声明不能掩盖冲突或非法编号: %+v", got)
			}
		})
	}
}

func TestParseFilenameKeepsIdentityBeforeFirstRepeatedCoordinate(t *testing.T) {
	for _, tc := range []struct {
		name, title, year string
		want              Numbering
	}{
		{"作品 (2024) - S02E10 - 分集标题 (2017) - S02E10.mkv", "作品", "2024", Numbering{2, 10, 0}},
		{"作品 - S00E01 - S00E01 - OVA#13.mkv", "作品", "", Numbering{0, 1, 0}},
		{"S02E10 - S02E10 - 另一作品 (2026).mkv", "", "", Numbering{2, 10, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseFilename(tc.name)
			if got.Identity.Name != tc.title || got.Identity.Year != tc.year || got.Identity.Kind != "tv" || got.Identity.Season != tc.want.Season || !got.Identity.SeasonExplicit || !got.Coordinate.Valid || got.Coordinate.Numbering != tc.want {
				t.Fatalf("合并不得扩大作品身份截取范围: %+v", got)
			}
		})
	}

	got := ParseFilename("Show - S02E10 - S02E10 - {tmdb-42} [动画].mkv")
	if !got.Coordinate.Valid || got.Identity.TMDBID != 42 || got.Identity.ContentType != "animation" {
		t.Fatalf("全文件作品声明仍应生效: %+v", got)
	}
	got = ParseFilename("Show {tmdb-41} - S02E10 - S02E10 - {tmdb-42}.mkv")
	if !got.Coordinate.Valid || got.Identity.Conflict != "TMDB_ID_CONFLICT" {
		t.Fatalf("坐标合并不能消除作品声明冲突: %+v", got)
	}
}

func TestRepeatedCoordinatesLeaveLegacyStandardNumberingUnchanged(t *testing.T) {
	got, found := StandardNumbering("Show.S02E10 - S02E10")
	if !found || got != (Numbering{}) {
		t.Fatalf("旧入口不能跟随新坐标策略放宽: got %+v, found %v", got, found)
	}
}
