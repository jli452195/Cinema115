package organize

import (
	"strings"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
)

func TestBatchOrganizationOnlyMovesSelectedVideosAndTheirSubtitles(t *testing.T) {
	job := planJob("Anime - 25.mkv", "Anime - 25.chs.ass", "Anime - 26.mkv", "Anime - 26.chs.ass", "Anime - 39.mkv", "readme.txt")
	job.Snapshot.Release.Parse = releaseparse.Result{Kind: "batch"}
	for _, index := range []int{1, 5} {
		e := job.Files.Entries[index]
		parsed, err := releaseparse.Title(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		job.Snapshot.BatchFiles = append(job.Snapshot.BatchFiles, model.AnimeBatchFile{Path: e.Name, Size: e.Size, Parse: parsed})
	}
	plan, err := Build(job)
	if err != nil || plan.Ready != 3 || plan.NeedsReview != 0 {
		t.Fatalf("%+v %v", plan, err)
	}
	if !strings.Contains(plan.Items[0].TargetPath, ".S01E01-") || !strings.Contains(plan.Items[2].TargetPath, ".S01E15-") || plan.Items[3].VideoID != plan.Items[0].ID {
		t.Fatal("frozen offset, beyond TMDB count or subtitle pairing", plan.Items)
	}
	for _, index := range []int{1, 4, 5} {
		if plan.Items[index].Status != "preserved" || plan.Items[index].TargetPath != "" {
			t.Fatal("unselected video/attachment was moved", plan.Items[index])
		}
	}
	for _, mode := range []string{"missing", "size", "ambiguous", "invalid parse"} {
		t.Run(mode, func(t *testing.T) {
			j := planJob("Anime - 25.mkv")
			j.Snapshot.Release.Parse = releaseparse.Result{Kind: "batch"}
			parsed, _ := releaseparse.Title("Anime - 25.mkv")
			j.Snapshot.BatchFiles = []model.AnimeBatchFile{{Path: "Anime - 25.mkv", Size: 1024, Parse: parsed}}
			switch mode {
			case "missing":
				j.Snapshot.BatchFiles[0].Path = "Anime - 26.mkv"
			case "size":
				j.Snapshot.BatchFiles[0].Size++
			case "ambiguous":
				j.Files.Entries = append(j.Files.Entries, model.AnimeDriveEntry{ID: "401", ParentCID: "200", Kind: "directory", Name: "other"}, model.AnimeDriveEntry{ID: "402", ParentCID: "401", Kind: "file", Name: "Anime - 25.mkv", Size: 1024})
			case "invalid parse":
				j.Snapshot.BatchFiles[0].Parse.Episode = nil
			}
			p, err := Build(j)
			if mode == "invalid parse" {
				if err == nil {
					t.Fatal("invalid frozen selection accepted")
				}
			} else if err != nil || p.NeedsReview == 0 || p.Ready != 0 {
				t.Fatal("incomplete or ambiguous file authorized", p, err)
			}
		})
	}
}
