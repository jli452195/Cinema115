package organize

import (
	"strings"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
)

func TestAnimeOrganizationBracketLanguageSubtitles(t *testing.T) {
	job := planJob("[Kono Oto Tomare][02][1080P].mkv", "[Kono Oto Tomare][02][CHT].ass", "[Kono Oto Tomare][02][CHS].ass")
	job.Snapshot.Naming.Sources[0].Segments[0].First = 1
	job.Snapshot.Naming.Sources[0].Segments[0].SourceBase = 1
	plan, err := Build(job)
	if err != nil || plan.Ready != 3 || plan.NeedsReview != 0 {
		t.Fatal(plan, err)
	}
	stem := strings.TrimSuffix(plan.Items[0].TargetPath, ".mkv")
	for i, tail := range []string{".zh-Hant.ass", ".zh-Hans.ass"} {
		item := plan.Items[i+1]
		if item.VideoID != plan.Items[0].ID || item.TargetPath != stem+tail {
			t.Fatal("subtitle failed to follow video name", item)
		}
	}
}

func TestAnimeOrganizationSubtitleFallbackDoesNotHideUnselectedBatchVideos(t *testing.T) {
	job := planJob("[Show][25][1080P].mkv", "[Show][25][720P].mkv", "[Show][25][CHT].ass")
	parsed, err := releaseparse.Title(job.Files.Entries[1].Name)
	if err != nil {
		t.Fatal(err)
	}
	job.Snapshot.Release.Parse = releaseparse.Result{Kind: "batch"}
	job.Snapshot.BatchFiles = []model.AnimeBatchFile{{Path: job.Files.Entries[1].Name, Size: 1024, Parse: parsed}}
	plan, err := Build(job)
	if err != nil || plan.Items[2].VideoID != "" || plan.Items[2].TargetPath != "" || plan.Items[2].Status != "needs_review" || plan.NeedsReview != 1 {
		t.Fatal("unselected edition disappeared during subtitle matching", plan, err)
	}
	job.Files.Entries[3].Name = "[Show][25][720P][CHT].ass"
	plan, err = Build(job)
	if err != nil || plan.Items[2].VideoID != plan.Items[1].ID || plan.Items[2].Status != "preserved" || plan.NeedsReview != 0 {
		t.Fatal("subtitle belonging to an unselected video blocked the batch", plan, err)
	}
	job.Files.Entries[3].Name = "[Show][25][1080P][CHT].ass"
	plan, err = Build(job)
	if err != nil || plan.Items[2].VideoID != plan.Items[0].ID || plan.Ready != 2 || plan.NeedsReview != 0 {
		t.Fatal("qualified subtitle did not follow the selected video", plan, err)
	}
	// Existing exact stems take precedence over the metadata fallback.
	job = planJob("[Show][25].mkv", "[Show][25][1080P].mkv", "[Show][25].chs.ass")
	plan, err = Build(job)
	if err != nil || plan.Items[2].VideoID != plan.Items[0].ID {
		t.Fatal("exact association was replaced by fallback", plan, err)
	}
}
