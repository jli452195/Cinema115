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

func TestOrganizeFileNumberingSharedByGardenAndManualScan(t *testing.T) {
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
		{"Show/[Group] Show [13].mkv", 1, 13, 0},
		{"Show/[Group] Show [01v2].mkv", 1, 1, 0},
		{"Show/Animation-02.WMV", 1, 2, 0},
		{"Show/Animation-03v2.mkv", 1, 3, 0},
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
		{"Show/Show [SP01].mkv", 1, 0, 0},
		{"Show/SP/Show E01-E02.mkv", 1, 0, 0},
		{"Show/NCOP.E01-E02.mkv", 1, 0, 0},
	}
	for episode := 1; episode <= 52; episode++ {
		cases = append(cases, example{fmt.Sprintf("陰陽大戰記/陰陽大戰記-%02d.wmv", episode), 1, episode, 0})
	}
	s, drive, _, _, transport, in := gardenFixture(t)
	transport.names = make([]string, len(cases))
	drive.dirs["30"] = make([]p115.ShareFile, len(cases))
	for i, tc := range cases {
		transport.names[i] = tc.name
		drive.dirs["30"][i] = p115.ShareFile{ID: fmt.Sprint(i + 100), Name: path.Base(tc.name), Size: 100}
		if path.Base(path.Dir(tc.name)) == "SP" {
			drive.dirs["30"][i].Directory = true
			drive.dirs["30"][i].Name = "SP"
			drive.dirs[fmt.Sprint(i+100)] = []p115.ShareFile{{ID: "900", Name: path.Base(tc.name), Size: 100}}
		}
	}
	gardenFiles, err := s.Resources.Files(context.Background(), in.DownloadResourceInput)
	if err != nil {
		t.Fatal(err)
	}
	manual := NewManualOrganize(s.Organize)
	scan, err := manual.Scan(context.Background(), OrganizeScanInput{AccountID: in.AccountID, ParentID: "10", ParentPath: "/来源", EntryID: "30"})
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]OrganizeFile, len(scan.Files))
	for _, file := range scan.Files {
		byID[file.ID] = file
	}
	for i, tc := range cases {
		var fromGarden OrganizeFile
		for _, file := range gardenFiles.Files {
			if file.ID == tc.name {
				fromGarden = file
				break
			}
		}
		id := fmt.Sprint(i + 100)
		if path.Base(path.Dir(tc.name)) == "SP" {
			id = "900"
		}
		for entry, file := range map[string]OrganizeFile{"garden": fromGarden, "manual": byID[id]} {
			if file.ID == "" || file.Season != tc.season || file.Episode != tc.episode || file.EndEpisode != tc.end {
				t.Errorf("%s %q: got S%dE%d-%d; want S%dE%d-%d", entry, tc.name, file.Season, file.Episode, file.EndEpisode, tc.season, tc.episode, tc.end)
			}
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

func TestOrganizePreviewsAcceptManualNumberingWithoutSeasonRequests(t *testing.T) {
	s, drive, adder, tracker, _, in := gardenFixture(t)
	metadata := &organizeReferenceMetadata{}
	s.Organize.metadata = metadata
	manual := NewManualOrganize(s.Organize)
	for _, selection := range []OrganizeSelection{{ID: "101", Season: 1, Episode: 13}, {ID: "101", Season: 1, Episode: 12, EndEpisode: 13}, {ID: "101", Season: 9, Episode: 99}} {
		job := organizeTestPreview(t, manual, in.AccountID, []OrganizeSelection{selection})
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
