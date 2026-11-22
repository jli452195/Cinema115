package service

import (
	"testing"

	"strm115/backend/internal/recognition"
	"strm115/backend/internal/utils/torrentmeta"
)

func TestPrepareDownloadNamingNumberingBoundaries(t *testing.T) {
	const (
		seasonDir = "Show - 2nd Season/"
		first     = "Show 13 - The Return (BD, 720p, AAC).mkv"
		second    = "Show 14v2 - The New Journey (BD, 720p, AAC).mkv"
	)
	type expectation struct {
		path    string
		season  int
		episode int
	}
	for _, tc := range []struct {
		name  string
		files []expectation
	}{
		{
			name: "分部和冲突季标记",
			files: []expectation{
				{"Show - Part 2/" + first, 1, 0},
				{"Show - Part 2/" + second, 1, 0},
				{"Show - 2nd Season & Season 3/" + first, 1, 0},
				{"Show - 2nd Season & Season 3/" + second, 1, 0},
				{"Show - 2nd Season - Extras/" + first, 1, 0},
				{"Show - 2nd Season - Extras/" + second, 1, 0},
			},
		},
		{
			name: "同集不同版本不足以确认编号格式",
			files: []expectation{
				{seasonDir + first, 1, 0},
				{seasonDir + "Show 13v2 - The Return (BD, 720p, AAC).mkv", 1, 0},
			},
		},
		{
			name: "不同目录独立确认且只检查直接父目录",
			files: []expectation{
				{"Disc A/" + seasonDir + first, 1, 0},
				{"Disc B/" + seasonDir + second, 1, 0},
				{seasonDir + "Disc 1/" + first, 1, 0},
				{seasonDir + "Disc 1/" + second, 1, 0},
			},
		},
		{
			name: "确认格式后仍保留明确坐标和待定项",
			files: []expectation{
				{seasonDir + first, 2, 13},
				{seasonDir + second, 2, 14},
				{seasonDir + "Show S03E06 (BD, 720p, AAC).mkv", 3, 6},
				{seasonDir + "Show 15 - Conflicting [S02E03] [S03E06] (BD, 720p, AAC).mkv", 1, 0},
				{seasonDir + "Show 12,5 - The Side Story (BD, 720p, AAC).mkv", 1, 0},
				{seasonDir + "Show 16 - NCOP (BD, 720p, AAC).mkv", 1, 0},
				{seasonDir + "Show 17 - The Final Act.mkv", 1, 0},
				{seasonDir + "Other Show 18 - The Final Act (BD, 720p, AAC).mkv", 1, 0},
			},
		},
		{
			name: "文件季标确定季号且保留发布集",
			files: []expectation{
				{"[Group][Show season2][13][1080p].mkv", 2, 13},
			},
		},
		{
			name: "目录季标仅补同作品的直接子文件",
			files: []expectation{
				{"Show season2/Show [03][720p].mkv", 2, 3},
				{"[Group][Show season2][BDRip]/[Group][Show][04][720p].mkv", 2, 4},
				{"Show season2/Disc 1/Show [04][720p].mkv", 1, 4},
				{"Show season2/Other Show [05][720p].mkv", 1, 5},
				{"Show S1+S2+OVA+Fan Disc/Show [06][720p].mkv", 1, 6},
				{"Show season1-2/Show [07][720p].mkv", 1, 7},
				{"Show Season 2 - 08 (BD, 720p).mkv", 2, 8},
			},
		},
		{
			name: "季标冲突保留集号并提示确认季号",
			files: []expectation{
				{"Show season2/Show Season 3 [01][720p].mkv", -1, 1},
				{"Show Season 2 Season 3 [02][720p].mkv", -1, 2},
				{"Show season2/Show S03E06 [720p].mkv", 3, 6},
			},
		},
		{
			name: "OVA 和特典不套用普通季号",
			files: []expectation{
				{"[Group][Show season2][OVA]/[Group][Show season2][04][720p].mkv", 1, 4},
				{"Show season2/Extra/[Group][Show season2][05][720p].mkv", 1, 0},
				{"Show season2/[Group][Show season2][OVA][06][720p].mkv", 1, 0},
				{"Show season2/Fan Disc/Disc 1/[Group][Show season2][07][720p].mkv", 1, 7},
				{"Show season2/[Group][Show season2][Fan Disc][08][720p].mkv", 1, 8},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := make([]torrentmeta.File, 0, len(tc.files))
			for _, want := range tc.files {
				files = append(files, torrentmeta.File{Path: want.path, Size: 100})
			}
			out, err := organizeResourceFiles(gardenTestHash, files)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareDownloadNaming(&out, DownloadResourceInput{}, recognition.Snapshot{}); err != nil {
				t.Fatal(err)
			}
			byPath := make(map[string]OrganizeFile, len(out.Files))
			for _, file := range out.Files {
				byPath[file.ID] = file
			}
			for _, want := range tc.files {
				got := byPath[want.path]
				if got.Season != want.season || got.Episode != want.episode || got.EndEpisode != 0 {
					t.Errorf("%s 的季集 = %d/%d–%d，期望 %d/%d", want.path, got.Season, got.Episode, got.EndEpisode, want.season, want.episode)
				}
			}
		})
	}
}
