package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

func TestOrganizeFileNumberingSharedByGardenAndCloudFiles(t *testing.T) {
	type example struct {
		name                 string
		season, episode, end int
	}
	cases := []example{
		{"Show/01-02.mkv", 1, 1, 2},
		{"Show/03-04.mkv", 1, 3, 4},
		{"Show/[Group] Show [01-02][1080p].mkv", 1, 1, 2},
		{"Show/Show - 03-04 [1080p].mkv", 1, 3, 4},
		{"Show/Show.S02E01-E02.1080p.mkv", 2, 1, 2},
		{"Show/Show.E027-E28.BDrip.mkv", 1, 27, 28},
		{"Kaleido Star (BD 720p AVC AAC)/Kaleido Star - 27-28 (BD 960x720 AVC AACx2).mp4", 1, 27, 28},
		{"[親愛一族DEARS]/[親愛一族DEARS][13].rmvb", 1, 13, 0},
		{"Show/Show.E01+E02.mkv", 1, 1, 2},
		{"Show/Show [03～04].mkv", 1, 3, 4},
		{"Show/Show 第03至04话.mp4", 1, 3, 4},
		{"Show/Show.S00E01.mkv", 0, 1, 0},
		{"Show/Show.S02E03.mkv", 2, 3, 0},
		{"Show/魔法骑士雷阿斯 (2026) - S01E04 - 1080p.Crunchyroll.WEB-DL.H264.AAC.mkv", 1, 4, 0},
		{"Show/Show.S02E04 - 720p.mkv", 2, 4, 0},
		{"Show/Show.S02E04-2160p.mkv", 2, 4, 0},
		{"Show/Show.S02E04 - 4K.mkv", 2, 4, 0},
		{"Show/Show.S02E04-E05 - 1080p.mkv", 2, 4, 5},
		{"Show/Show.S02E04-05 - 2160p.mkv", 2, 4, 5},
		{"Show/Show.S02E04 - 1080p [05].mkv", 1, 0, 0},
		{"Show/Show.S02E04 E05 - 1080p.mkv", 1, 0, 0},
		{"Show/Show.S02E04 S03E04.mkv", 1, 0, 0},
		{"Show/Show.S02E04-05+07.mkv", 1, 0, 0},
		{"Show/Show.S02E04foo - 1080p.mkv", 1, 0, 0},
		{"Show/Planet 51.S02E04 - 1080p.mkv", 2, 4, 0},
		{"Show/Show.S1000E01 - 1080p.mkv", 1000, 1, 0},
		{"Show/吸血鬼生活.What.We.Do.in.the.Shadows.S01E01.2019.1080p.DSNP.WEB-DL.DDP5.1.H264-ZeroTV.mkv", 1, 1, 0},
		{"Show/Show.S02E03.2026.1080p.mkv", 2, 3, 0},
		{"Show/Show.S02E03-E04.2026.WEB-DL.mkv", 2, 3, 4},
		{"Show/Show.S02E03.2026.mkv", 2, 3, 0},
		{"Show/Show.S02E03.5.1080p.mkv", 1, 0, 0},
		{"Show/Show.S02E03.201.1080p.mkv", 1, 0, 0},
		{"Show/Show.S02E03.2026 [05].mkv", 1, 0, 0},
		{"Show/[Group] Show [13].mkv", 1, 13, 0},
		{"Show/[Group] Show [01v2].mkv", 1, 1, 0},
		{"Show/Animation-02.WMV", 1, 2, 0},
		{"Show/Animation-03v2.mkv", 1, 3, 0},
		{"Show/[Cleo]Trinity_Blood_-_01_(Dual Audio_10bit_BD1080p_x265).mkv", 1, 1, 0},
		{"Show/[Cleo]Trinity_Blood_-_02_(Dual Audio_10bit_BD1080p_x265).mkv", 1, 2, 0},
		{"Show/[Cleo]Trinity_Blood_-_03_(Dual Audio_10bit_BD1080p_x265).mkv", 1, 3, 0},
		{"Show/Cleo]Trinity_Blood_-_03_(Dual Audio_10bit_BD1080p_x265).mkv", 1, 3, 0},
		{"Show/Trinity_Blood_-_03-04_(Dual Audio_10bit_BD1080p_x265).mkv", 1, 3, 4},
		{"Show/Trinity_Blood_-_03.5_(Dual Audio_10bit_BD1080p_x265).mkv", 1, 0, 0},
		{"Show/Trinity_Blood_[03]_-_04_(Dual Audio_10bit_BD1080p_x265).mkv", 1, 0, 0},
		{"Show/Animation-03-04.mkv", 1, 3, 4},
		{"Show/Animation-2024.mkv", 1, 0, 0},
		{"Show/Animation-1080.mkv", 1, 0, 0},
		{"Show/Animation-x265.mkv", 1, 0, 0},
		{"Show/Animation-HEVC-10bit.mkv", 1, 0, 0},
		{"Show/Animation-NCOP-01.mkv", 1, 0, 0},
		{"Show/Animation-12.5.mkv", 1, 0, 0},
		{"Show/Animation [03] Name-01.mkv", 1, 0, 0},
		{"Show/Animation-2024-01.mkv", 1, 0, 0},
		{"Show [01-12]/unknown.mkv", 1, 0, 0},
		{"Show/Show [01-02+04].mkv", 1, 0, 0},
		{"Show/Show [01+03].mkv", 1, 0, 0},
		{"Show/Show [03-01].mkv", 1, 0, 0},
		{"Show/Show [00-01].mkv", 1, 0, 0},
		{"Show/Show [2024-2025].mkv", 1, 0, 0},
		{"Show/Show [1080p][x265].mkv", 1, 0, 0},
		{"Show/Show [01-02] [25].mkv", 1, 0, 0},
		{"Show/Show [SP01].mkv", 0, 1, 0},
		{"Show/[DBD制作组][尸鬼][SP][01][1080P][BDRip][AVC][简体内嵌][FLAC].mkv", 0, 1, 0},
		{"Show/SP/Show E01-E02.mkv", 0, 1, 2},
		{"Show/NCOP.E01-E02.mkv", 1, 0, 0},
		{"Show/[Show][menu][01][1080P].mkv", 1, 0, 0},
		{"Show/[Show][Mini anime][01][1080P].mkv", 1, 0, 0},
	}
	for episode := 1; episode <= 52; episode++ {
		cases = append(cases, example{fmt.Sprintf("陰陽大戰記/陰陽大戰記-%02d.wmv", episode), 1, episode, 0})
	}
	s, _, _, _, transport, in := gardenFixture(t)
	transport.names = make([]string, len(cases))
	for i, tc := range cases {
		transport.names[i] = tc.name
	}
	gardenFiles, err := s.Resources.Files(context.Background(), in.DownloadResourceInput)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]OrganizeFile, len(gardenFiles.Files))
	for _, file := range gardenFiles.Files {
		byID[file.ID] = file
	}
	for i, tc := range cases {
		cloud := OrganizeFile{ShareFile: p115.ShareFile{ID: fmt.Sprint(i + 100), Name: path.Base(tc.name), Size: 100}, SourcePath: tc.name, MediaType: "video", Season: 1}
		organizeFileNumbering(&cloud)
		for entry, file := range map[string]OrganizeFile{"garden": byID[tc.name], "cloud": cloud} {
			if file.ID == "" || file.Season != tc.season || file.Episode != tc.episode || file.EndEpisode != tc.end {
				t.Errorf("%s %q: got S%dE%d-%d; want S%dE%d-%d", entry, tc.name, file.Season, file.Episode, file.EndEpisode, tc.season, tc.episode, tc.end)
			}
		}
	}
}

func TestOrganizeSpecialNumbering(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		season, episode, end int
	}{
		{"Show SP01.mkv", 0, 1, 0},
		{"Show sp02.mkv", 0, 2, 0},
		{"Show [sP03].mkv", 0, 3, 0},
		{"Show SP1.mkv", 0, 1, 0},
		{"Show SP-04v2.mkv", 0, 4, 0},
		{"Show [SP][05].mkv", 0, 5, 0},
		{"Show [SP] [06v3][1080p].mkv", 0, 6, 0},
		{"Show 【SP】【07】.mkv", 0, 7, 0},
		{"Show SP01-SP02.mkv", 0, 1, 2},
		{"SP/Show [01].mkv", 0, 1, 0},
		{"[sp]/Show - 02.mkv", 0, 2, 0},
		{"SP/Show.S00E03.mkv", 0, 3, 0},
		{"SP/Show.S01E03.mkv", 1, 0, 0},
		{"Show.S01E03 [SP].mkv", 1, 0, 0},
		{"Show [SP].mkv", 1, 0, 0},
		{"SP/Show.mkv", 1, 0, 0},
		{"Show [SP][1080p][x265].mkv", 1, 0, 0},
		{"Show SP01.5.mkv", 1, 0, 0},
		{"Show [SP01A].mkv", 1, 0, 0},
		{"Show [SP01][02].mkv", 1, 0, 0},
		{"Show [SP][01][02].mkv", 1, 0, 0},
		{"Show SP00.mkv", 1, 0, 0},
		{"Show SP10001.mkv", 1, 0, 0},
		{"Show SP01-03+05.mkv", 1, 0, 0},
		{"NCOP/Show SP01.mkv", 1, 0, 0},
		{"SP/Show NCED01.mkv", 1, 0, 0},
		{"Show SP01 NCOP.mkv", 1, 0, 0},
		{"Show OVA01.mkv", 1, 0, 0},
		{"Show [menu][01].mkv", 1, 0, 0},
		{"Show 【menu】【SP01】.mkv", 1, 0, 0},
		{"Show （NCOP）（SP01）.mkv", 1, 0, 0},
		{"[01-22TV全集+SP]/Show [22].mkv", 1, 22, 0},
		{"SPY x FAMILY [01].mkv", 1, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := OrganizeFile{ShareFile: p115.ShareFile{Name: path.Base(tc.name)}, SourcePath: tc.name, MediaType: "video", Season: 1}
			organizeFileNumbering(&file)
			if file.Season != tc.season || file.Episode != tc.episode || file.EndEpisode != tc.end {
				t.Fatalf("got S%dE%d-%d; want S%dE%d-%d", file.Season, file.Episode, file.EndEpisode, tc.season, tc.episode, tc.end)
			}
		})
	}
}

func TestOrganizeUnderscoreNamesReachNamingPreviewAndPlan(t *testing.T) {
	s, drive, account, _ := newOrganizeTest(t)
	drive.dirs["30"] = nil
	for episode := 1; episode <= 3; episode++ {
		drive.dirs["30"] = append(drive.dirs["30"], p115.ShareFile{ID: fmt.Sprint(episode + 100), Name: fmt.Sprintf("[Cleo]Trinity_Blood_-_%02d_(Dual Audio_10bit_BD1080p_x265).mkv", episode), Size: 100})
	}
	ctx := context.Background()
	scan := organizeTestSource(t, s, account)
	if len(scan.Files) != 3 {
		t.Fatal(scan)
	}
	input := OrganizePreviewInput{Kind: "tv", TMDBID: 42}
	namingInput := MediaNamingInput{Kind: input.Kind, TMDBID: input.TMDBID}
	for _, file := range scan.Files {
		selection := OrganizeSelection{ID: file.ID, Season: file.Season, Episode: file.Episode}
		input.Files = append(input.Files, selection)
		namingInput.Files = append(namingInput.Files, MediaNamingFile{OrganizeSelection: selection, Name: file.Name, NamingValues: file.NamingValues})
	}
	preview, err := (MediaNaming{Templates: s.templates, Metadata: s.metadata}).Preview(ctx, namingInput)
	if err != nil || len(preview.Items) != 3 {
		t.Fatal(preview, err)
	}
	plan, err := s.buildPlan(ctx, scan, input)
	if err != nil || len(plan.Items) != 3 {
		t.Fatal(plan, err)
	}
	for i, item := range preview.Items {
		if item.Error != "" || item.Result == nil || !strings.Contains(item.Result.Name, fmt.Sprintf(".S01E%02d.", i+1)) || plan.Items[i].Name != item.Result.Name {
			t.Fatal("inferred episode did not reach consistent naming preview and plan", item, plan.Items[i])
		}
	}
}

func TestOrganizeSpecialPlanPreviewAndExecution(t *testing.T) {
	s, drive, account, _ := newOrganizeTest(t)
	metadata := &organizeReferenceMetadata{}
	s.metadata = metadata
	const stem = "[DBD制作组][尸鬼][SP][01][1080P][BDRip][AVC][简体内嵌][FLAC]"
	drive.dirs["30"] = []p115.ShareFile{{ID: "101", Name: "Show [22].mkv", Size: 100}, {ID: "90", Name: "SP", Directory: true}}
	drive.dirs["90"] = []p115.ShareFile{
		{ID: "201", Name: stem + ".mkv", Size: 100},
		{ID: "202", Name: stem + ".chs.ass", Size: 10},
		{ID: "203", Name: "Show sp02.mkv", Size: 100},
	}
	ctx := context.Background()
	scan := organizeTestSource(t, s, account)
	scan.Files = append(scan.Files, organizeTestFiles(drive.dirs["90"], "90", "/来源/待整理/SP")...)
	if len(scan.Files) != 4 {
		t.Fatal("SP source facts lost files", scan)
	}
	input := OrganizePreviewInput{Kind: "tv", TMDBID: 36581, Subtitles: true}
	namingInput := MediaNamingInput{Kind: input.Kind, TMDBID: input.TMDBID, Subtitles: organizeSubtitles(scan.Files)}
	want := map[string]string{"101": "S01E22", "201": "S00E01", "202": "S00E01", "203": "S00E02"}
	for _, file := range scan.Files {
		if file.MediaType != "video" {
			continue
		}
		if fmt.Sprintf("S%02dE%02d", file.Season, file.Episode) != want[file.ID] {
			t.Fatal("source lost its explicit numbering", file)
		}
		selection := OrganizeSelection{ID: file.ID, Season: file.Season, Episode: file.Episode}
		input.Files = append(input.Files, selection)
		namingInput.Files = append(namingInput.Files, MediaNamingFile{OrganizeSelection: selection, Name: file.Name, NamingValues: file.NamingValues})
	}
	preview, err := (MediaNaming{Templates: s.templates, Metadata: s.metadata}).Preview(ctx, namingInput)
	if err != nil || len(preview.Items) != 4 {
		t.Fatal("SP naming preview", preview, err)
	}
	plan, err := s.buildPlan(ctx, scan, input)
	if err != nil || len(plan.Items) != 4 {
		t.Fatal("SP organize plan", plan, err)
	}
	plan, err = s.rememberPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]string, len(preview.Items))
	for _, item := range preview.Items {
		if item.Result == nil || !strings.Contains(item.Result.Name, "."+want[item.ID]+".") {
			t.Fatal("SP preview or subtitle numbering", item)
		}
		names[item.ID] = item.Result.Name
	}
	for _, item := range plan.Items {
		season := "Season 00"
		if item.File.ID == "101" {
			season = "Season 01"
		}
		if item.Name != names[item.File.ID] || path.Base(item.Directory) != season {
			t.Fatal("preview and SP plan differ", item)
		}
	}
	// The inferred season is editable and does not require TMDB season details.
	input.Files[1].Season, input.Files[1].Episode = 2, 13
	override, err := s.buildPlan(ctx, scan, input)
	if err != nil || !strings.Contains(override.Items[1].Name, ".S02E13.") || metadata.seasonCalls != 0 {
		t.Fatal("manual SP correction was blocked", override, err, metadata.seasonCalls)
	}
	finished := organizeTestRun(t, s, account, plan)
	if finished.Status != "completed" || len(finished.Items) != 4 {
		t.Fatal("SP execution", finished)
	}
	for _, item := range finished.Items {
		if item.Name != names[item.File.ID] {
			t.Fatal("execution changed SP or companion naming", item)
		}
	}
}

func TestOrganizeNumberingPackTitlesDoNotMarkAllChildrenAsExtras(t *testing.T) {
	for _, tc := range []struct {
		parent, name string
		episode      int
	}{
		{"[DBD-Raws][葬送的芙莉莲][01-28TV全集+特典映像]", "[DBD-Raws][Sousou no Frieren][01][1080P][BDRip][HEVC-10bit][FLACx2].mkv", 1},
		{"Show + 特典", "Show.S01E02.mkv", 2},
		{"SP", "Show.S01E02.mkv", 0},
		{"[SP]", "Show.S01E02.mkv", 0},
		{"特典映像", "Show [02].mkv", 0},
		{"menu", "Show [02].mkv", 0},
		{"Mini anime", "Show [02].mkv", 0},
	} {
		file := OrganizeFile{ShareFile: p115.ShareFile{Name: tc.name}, MediaType: "video", SourcePath: path.Join(tc.parent, tc.name), Season: 1}
		organizeFileNumbering(&file)
		if file.Episode != tc.episode {
			t.Errorf("%s/%s: got %d, want %d", tc.parent, tc.name, file.Episode, tc.episode)
		}
	}
}

type organizeReferenceMetadata struct {
	organizeFakeMetadata
	seasonCalls int
}

func (m *organizeReferenceMetadata) Season(context.Context, int, int) (*tmdb.SeasonDetail, error) {
	m.seasonCalls++
	return nil, fmt.Errorf("season is unavailable")
}

func TestOrganizePreviewsAcceptExplicitNumberingWithoutSeasonRequests(t *testing.T) {
	s, drive, adder, tracker, _, in := gardenFixture(t)
	metadata := &organizeReferenceMetadata{}
	s.Organize.metadata = metadata
	for _, selection := range []OrganizeSelection{{ID: "101", Season: 1, Episode: 13}, {ID: "101", Season: 1, Episode: 12}, {ID: "101", Season: 9, Episode: 99}} {
		job := organizeTestPreview(t, s.Organize, in.AccountID, []OrganizeSelection{selection})
		if !strings.Contains(job.Items[0].Name, fmt.Sprintf("S%02dE%02d", selection.Season, selection.Episode)) {
			t.Fatal("manual numbering changed", job)
		}
		selection.ID = in.Files[0].ID
		in.Files = []OrganizeSelection{selection}
		job, err := s.Preview(context.Background(), in)
		if err != nil || len(job.Items) != 1 || !strings.Contains(job.Items[0].Name, fmt.Sprintf("S%02dE%02d", selection.Season, selection.Episode)) {
			t.Fatal("garden numbering rejected", job, err)
		}
	}
	if metadata.seasonCalls != 0 || adder.url != "" || tracker.calls != 0 || drive.renames+drive.moves+drive.creates != 0 {
		t.Fatal("preview queried seasons or performed writes", metadata.seasonCalls)
	}
}
