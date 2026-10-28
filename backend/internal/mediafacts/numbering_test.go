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
