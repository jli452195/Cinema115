package mediafacts

import "testing"

func TestStandardNumbering(t *testing.T) {
	for _, tc := range []struct {
		stem  string
		want  Numbering
		found bool
	}{
		{"魔法骑士雷阿斯 (2026) - S01E04 - 1080p.Crunchyroll.WEB-DL.H264.AAC", Numbering{1, 4, 0}, true},
		{"Show.S02E04-720p", Numbering{2, 4, 0}, true},
		{"Show.S02E04 - 2160p", Numbering{2, 4, 0}, true},
		{"Show.S02E04 - 1920x1080", Numbering{2, 4, 0}, true},
		{"Show.S02E04-4K", Numbering{2, 4, 0}, true},
		{"Show.S02E04.1080p", Numbering{2, 4, 0}, true},
		{"Show.S02E04.2026.1080p", Numbering{2, 4, 0}, true},
		{"Planet 51.S02E04 - 1080p", Numbering{2, 4, 0}, true},
		{"Show.s02e04v2 - 1080p", Numbering{2, 4, 0}, true},
		{"Show.S00E01", Numbering{0, 1, 0}, true},
		{"Show.S02E04-E05 - 1080p", Numbering{2, 4, 5}, true},
		{"Show.S02E04-05.2026.1080p", Numbering{2, 4, 5}, true},
		{"Show.S02E04+E05", Numbering{2, 4, 5}, true},
		{"Show.S02E04v2-E05v3", Numbering{2, 4, 5}, true},
		{"Show.S100E10000", Numbering{100, 10000, 0}, true},
		{"Show.S1000E01", Numbering{1000, 1, 0}, true},
		{"冰海战记 - S02E01 - 7³ACG", Numbering{2, 1, 0}, true},
		{"冰海战记 - S02E03 - 7³ACG", Numbering{2, 3, 0}, true},
		{"冰海战记 - S02E07 - 7³ACG", Numbering{2, 7, 0}, true},
		{"冰海战记 - S02E08 - 7³ACG", Numbering{2, 8, 0}, true},
		{"冰海战记 - S02E09 - 7³ACG", Numbering{2, 9, 0}, true},
		{"冰海战记 - S02E10 - 7³ACG", Numbering{2, 10, 0}, true},
		{"冰海战记 - S02E11 - 7³ACG", Numbering{2, 11, 0}, true},
		{"冰海战记 - S02E12 - 7³ACG", Numbering{2, 12, 0}, true},
		{"冰海战记 - S02E13 - 7³ACG", Numbering{2, 13, 0}, true},
		{"冰海战记 - S02E14 - 7³ACG", Numbering{2, 14, 0}, true},
		{"Show.S02E04-05 - 7³ACG", Numbering{2, 4, 5}, true},
		{"Show.S02E04 - 7ACG", Numbering{2, 4, 0}, true},
		{"Show.S02E04 - 7A\u0301CG", Numbering{2, 4, 0}, true},
		{"Show.S02E04 - 7³ACG [1080p]", Numbering{2, 4, 0}, true},
		{"Show.S02E04 - 7ACG - 8XYZ", Numbering{2, 4, 0}, true},
		{"Show.S02E04 - [7³ACG]", Numbering{2, 4, 0}, true},
		{"Show.S02E04 - 05v2", Numbering{2, 4, 5}, true},
		{"Show.S02E04 - 7³ACG - 05", Numbering{}, true},
		{"Show.S02E04 - 7³ACG 05", Numbering{}, true},
		{"Show.S02E04 - 7³ACG EP05", Numbering{}, true},
		{"Show.S02E04 - 7³ACG S03E05", Numbering{}, true},
		{"Show.S02E04 - 7³ACG [05]", Numbering{}, true},
		{"Show.S02E04 - 7ACG-08", Numbering{}, true},
		{"Show.S02E04 - 7ACG.5", Numbering{}, true},
		{"Show.S02E04 - 05A", Numbering{}, true},
		{"Show.S02E04 - 05Av2", Numbering{}, true},
		{"Show.S02E04 - 05v", Numbering{}, true},
		{"Show.S02E04 - 05v1234", Numbering{}, true},
		{"Show.S02E04 - 05集", Numbering{2, 4, 5}, true},
		{"Show.S02E04 - 05集v2", Numbering{2, 4, 5}, true},
		{"Show.S02E04 - 05话CHS", Numbering{2, 4, 5}, true},
		{"Show.S02E04 - 03集", Numbering{}, true},
		{"Show.S02E04 - 03集v2", Numbering{}, true},
		{"Show [04][1080p]", Numbering{}, false},
		{"Show - 04", Numbering{}, false},
		{"Show Season 02 E04", Numbering{}, false},
		{"Show.XS01E04", Numbering{}, false},
		{"Show.S02E04 - 1080p [05]", Numbering{}, true},
		{"Show.S02E04 E05 - 1080p", Numbering{}, true},
		{"Show.S02E04 S03E04", Numbering{}, true},
		{"Show.S02E04-05+07", Numbering{}, true},
		{"Show.S02E04/E05", Numbering{}, true},
		{"Show.S02E04+06", Numbering{}, true},
		{"Show.S02E04-03", Numbering{}, true},
		{"Show.S02E04-04", Numbering{}, true},
		{"Show.S02E04.5.1080p", Numbering{}, true},
		{"Show.S02E04.201.1080p", Numbering{}, true},
		{"Show.S02E04.2026 [05]", Numbering{}, true},
		{"Show.S02E04A", Numbering{}, true},
		{"Show.S02E04foo", Numbering{}, true},
		{"Show.S02E04-1080pixels", Numbering{}, true},
		{"Show.S02E04-05foo", Numbering{}, true},
		{"Show.S01E00", Numbering{}, true},
		{"Show.S1001E01", Numbering{}, true},
		{"Show.S01E10001", Numbering{}, true},
	} {
		t.Run(tc.stem, func(t *testing.T) {
			got, found := StandardNumbering(tc.stem)
			if got != tc.want || found != tc.found {
				t.Fatalf("got %+v, found=%t; want %+v, found=%t", got, found, tc.want, tc.found)
			}
		})
	}
}
