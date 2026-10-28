package mediafacts

import "testing"

func TestParseIdentity(t *testing.T) {
	for _, tc := range []struct {
		file    string
		parents []string
		want    Identity
	}{
		{"Moana.2.2024.V2.Bluray.2160p.HEVC.mkv", nil, Identity{Name: "Moana 2", Year: "2024", Season: 1}},
		{"Show.S02E13.1080p.WEB-DL.strm", nil, Identity{Name: "Show", Kind: "tv", Season: 2}},
		{"E03.strm", []string{"Season 02", "剧集 (2024) {tmdb-42}"}, Identity{Name: "剧集", Year: "2024", TMDBID: 42, Kind: "tv", Season: 2}},
		{"[Nix-Raws] VERTEX FORCE S01E01 [CR WEB-DL 1080p AVC AAC][SC_TC].mkv", nil, Identity{Name: "VERTEX FORCE", Kind: "tv", Season: 1}},
		{"[Cleo]Trinity_Blood_-_03_(Dual Audio_10bit_BD1080p_x265).mkv", nil, Identity{Name: "Trinity Blood", Season: 1}},
		{"01.mkv", []string{"第二季", "作品 (2026)"}, Identity{Name: "作品", Year: "2026", Kind: "tv", Season: 2}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if got := ParseIdentity(tc.file, tc.parents...); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
