package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/utils/torrentmeta"
)

func titleResource(source, title string) DownloadResourceInput {
	in := DownloadResourceInput{Source: source, Title: title, Magnet: "magnet:?xt=urn:btih:" + gardenTestHash, Provider: "dmhy", ProviderID: "688777"}
	if source == "mikan" {
		in.Provider, in.ProviderID = "", strings.Repeat("a", 40)
		in.TorrentURL = "https://mikanani.me/Download/20261024/" + in.ProviderID + ".torrent"
	}
	return in
}

func TestDownloadTitleClassificationSkipsOnlyClearSingles(t *testing.T) {
	for _, source := range []string{"mikan", "garden"} {
		t.Run(source, func(t *testing.T) {
			s, drive, adder, _, _, _ := gardenFixture(t)
			reads := 0
			s.Resources = downloadReaderFunc(func(context.Context, DownloadResourceInput) (DownloadFiles, error) {
				reads++
				return organizeResourceFiles(gardenTestHash, []torrentmeta.File{{Path: "Show [01].mkv"}})
			})
			for _, tc := range []struct {
				title           string
				episode, season int
			}{
				{"【动漫国字幕组】 ★10月新番[刺客守则][12 END][1080P][简体][MP4]", 12, 1},
				{"[Group] Show 第06话 [720p]", 6, 1},
				{"[Group] Show S02E06 [1080p]", 6, 2},
				{"[Group] Show [01v3][1080p]", 1, 1},
			} {
				files, err := s.Files(context.Background(), titleResource(source, tc.title))
				if err != nil || len(files.Files) != 1 || !files.Files[0].TitleOnly || files.Files[0].Episode != tc.episode || files.Files[0].Season != tc.season || files.ManifestID == "" || files.Hash != gardenTestHash || reads != 0 {
					t.Fatal("clear single made an upstream request or lost numbering", tc.title, files, reads, err)
				}
			}
			for _, title := range []string{"Show [06-12]", "Show [01-12 END](合集修正)", "Show 全集", "Show [06][07]", "Show [SP01]", "Show [12.5]", "Show [00]", "Show [01-02]", "Show", "Show [12][720p][1080p]"} {
				before := reads
				files, err := s.Files(context.Background(), titleResource(source, title))
				if err != nil || files.Files[0].TitleOnly || reads != before+1 {
					t.Fatal("ambiguous release skipped its file manifest", title, files, reads, err)
				}
			}
			if len(drive.listCalls) != 0 || drive.creates != 0 || adder.url != "" {
				t.Fatal("file preparation accessed 115")
			}
		})
	}
}

func TestDownloadTitleValidatesSourceAndCancellationWithoutReading(t *testing.T) {
	s, _, _, _, _, _ := gardenFixture(t)
	s.Resources = downloadReaderFunc(func(context.Context, DownloadResourceInput) (DownloadFiles, error) {
		t.Fatal("clear title unexpectedly requested a manifest")
		return DownloadFiles{}, nil
	})
	for _, source := range []string{"mikan", "garden"} {
		for _, mutate := range []func(*DownloadResourceInput){
			func(in *DownloadResourceInput) { in.Source = "unknown" },
			func(in *DownloadResourceInput) { in.Magnet = "not a magnet" },
			func(in *DownloadResourceInput) { in.ProviderID = "../../private" },
			func(in *DownloadResourceInput) { in.TorrentURL = "http://127.0.0.1/private" },
		} {
			in := titleResource(source, "Show [12 END]")
			mutate(&in)
			if _, err := s.Files(context.Background(), in); !errors.Is(err, ErrOrganizeInput) {
				t.Fatal("invalid title-only source accepted", in, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Files(ctx, titleResource("garden", "Show [12 END]")); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestDownloadTitleWorkerUsesActualExtensionSubtitlesAndFrozenMapping(t *testing.T) {
	for _, source := range []string{"mikan", "garden"} {
		t.Run(source, func(t *testing.T) {
			s, drive, adder, tracker, _, in := gardenFixture(t)
			s.Resources = downloadReaderFunc(func(context.Context, DownloadResourceInput) (DownloadFiles, error) {
				t.Fatal("title-only preview/worker fetched a remote manifest")
				return DownloadFiles{}, nil
			})
			in.DownloadResourceInput = titleResource(source, "[字幕组] Show [12 END][1080P][简体][MP4]")
			files, err := s.Files(context.Background(), in.DownloadResourceInput)
			if err != nil {
				t.Fatal(err)
			}
			in.ManifestID, in.Files = files.ManifestID, []OrganizeSelection{{ID: "title", Season: 2, Episode: 32}}
			plan, err := s.Preview(context.Background(), in)
			if err != nil || len(plan.Items) != 1 || plan.Items[0].Name != "测试作品.S02E32.1080p.[简体]-字幕组" {
				t.Fatal("title-only plan guessed an extension or discarded mapping", plan, err)
			}
			job, err := s.Start(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if source == "garden" {
				engine, err := NewOrganizeEngine(s.Organize.store, s.Organize.accounts, drive, s.Organize.metadata, s.Organize.templates)
				if err != nil {
					t.Fatal(err)
				}
				s = NewDownloadOrganize(engine, s.Resources, s.Downloads, s.Settings, tracker)
				job, err = s.Resume(job.ID)
				if err != nil || job.Status != "queued" || !job.Items[0].File.TitleOnly {
					t.Fatal("restart lost the title intent or could not resume unstarted work", job, err)
				}
			}
			pending := s.live[job.ID]
			tracker.resultID = "801"
			tracker.beforeRead = func() {
				drive.dirs[pending.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
				drive.dirs["801"] = []p115.ShareFile{
					{ID: "901", Name: "Show [12].MKV", Size: 1200},
					{ID: "902", Name: "Show [12].zh-CN.ASS", Size: 30},
					{ID: "903", Name: "unrelated.srt", Size: 10},
				}
			}
			if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "completed" {
				t.Fatal("title-only chain failed", done, pending.job, err)
			}
			if len(pending.job.Items) != 2 || pending.job.Items[0].File.TitleOnly || pending.job.Items[0].Name != plan.Items[0].Name+".mkv" || pending.job.Items[1].Name != plan.Items[0].Name+".zh-CN.ass" || len(drive.dirs["801"]) != 1 || drive.dirs["801"][0].ID != "903" {
				t.Fatal("real extension/subtitle association/leftovers lost", pending.job.Items, drive.dirs["801"])
			}
			if tracker.calls != 1 || adder.url != in.Magnet || drive.renames != 1 || drive.moves != 1 || drive.listCalls["801"] != 3 {
				t.Fatal("extra polling, reads or mutations", tracker.calls, drive.listCalls, drive.renames, drive.moves)
			}
			if _, err := s.Start(plan.ID); err != nil || len(s.History()) != 1 {
				t.Fatal("title-only receipt lost idempotency", err)
			}
		})
	}
}

func TestDownloadTitleBindingRejectsAmbiguityWithoutChangingPlan(t *testing.T) {
	for _, names := range [][]string{
		{"Show [12].mkv", "Show [13].mkv"}, {"Show [13].mkv"}, {"Show [11-12].mkv"},
		{"Show [12.5].mkv"}, {"Show [SP12].mkv"}, {"Show S02E12.mkv"}, {"disc.iso"},
		{"extras/Show.mkv"}, {"Show [00].mkv"},
	} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			job := OrganizeJob{Config: OrganizeConfig{SourcePath: "/staging", TargetPath: "/target"}, Items: []OrganizeItem{{File: OrganizeFile{ShareFile: p115.ShareFile{Name: "Show S01E12"}, TitleOnly: true, Season: 1, Episode: 12}, Name: "Show - S01E12", Directory: "Show/Season 01"}}}
			files := make([]OrganizeFile, len(names))
			for i, name := range names {
				files[i] = OrganizeFile{ShareFile: p115.ShareFile{ID: fmt.Sprint(i + 1), Name: name[strings.LastIndex(name, "/")+1:]}, SourcePath: name, MediaType: organizeMedia(name)}
			}
			if err := bindDownloadFiles(&job, files, ""); !errors.Is(err, errDownloadTitleReview) || len(job.Items) != 1 || !job.Items[0].File.TitleOnly || job.Items[0].Name != "Show - S01E12" {
				t.Fatal("ambiguous content was bound or damaged the saved intent", job.Items, err)
			}
		})
	}
	job := OrganizeJob{Config: OrganizeConfig{SourcePath: "/staging", TargetPath: "/target"}, Items: []OrganizeItem{{File: OrganizeFile{TitleOnly: true, Season: 1, Episode: 12}, Name: "Show - S01E12"}}}
	if err := bindDownloadFiles(&job, []OrganizeFile{{ShareFile: p115.ShareFile{ID: "901", Name: "video.mp4"}, SourcePath: "video.mp4", MediaType: "video"}}, ""); err != nil || job.Items[0].Name != "Show - S01E12.mp4" {
		t.Fatal("single unnumbered video cannot use the release title", job, err)
	}
	job.Items = []OrganizeItem{{File: OrganizeFile{ShareFile: p115.ShareFile{Name: "Show [12]"}, TitleOnly: true, Season: 1, Episode: 12}, Name: "Show - S02E12"}}
	if err := bindDownloadFiles(&job, []OrganizeFile{{ShareFile: p115.ShareFile{ID: "901", Name: "Show S02E12.mp4"}, SourcePath: "Show S02E12.mp4", MediaType: "video"}}, ""); err != nil || job.Items[0].Name != "Show - S02E12.mp4" {
		t.Fatal("a default preview season was mistaken for explicit publisher evidence", job, err)
	}
}

func TestDownloadTitleAmbiguityStopsOnlyItsOwnJobWithoutRetryReads(t *testing.T) {
	s, drive, _, tracker, _, in := gardenFixture(t)
	s.Resources = downloadReaderFunc(func(context.Context, DownloadResourceInput) (DownloadFiles, error) {
		t.Fatal("clear singles read remote manifests")
		return DownloadFiles{}, nil
	})
	resources := make([]DownloadOrganizeInput, 2)
	for i := range resources {
		resources[i] = DownloadOrganizeInput{DownloadResourceInput: titleResource("garden", fmt.Sprintf("Show [%02d END]", i+12)), Files: []OrganizeSelection{{ID: "title", Season: 1, Episode: i + 12}}}
		resources[i].Magnet = fmt.Sprintf("magnet:?xt=urn:btih:%040x", i+1)
	}
	preview, err := s.PreviewBatch(context.Background(), DownloadBatchInput{DownloadOrganizeInput: in, Resources: resources})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartBatch(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, job := range batch.Jobs {
		pending := s.live[job.ID]
		tracker.beforeRead = func() {
			tracker.resultID = pending.job.Download.DirectoryID
			drive.dirs[pending.job.Download.DirectoryID] = []p115.ShareFile{{ID: fmt.Sprint(901 + i*2), Name: fmt.Sprintf("Show [%02d].mkv", i+12), Size: 1200}}
			if i == 0 {
				drive.dirs[pending.job.Download.DirectoryID] = append(drive.dirs[pending.job.Download.DirectoryID], p115.ShareFile{ID: "902", Name: "Show [13].mkv", Size: 1200})
			}
		}
		done, err := s.advance(context.Background(), pending)
		want := "completed"
		if i == 0 {
			want = "blocked"
		}
		if err != nil || !done || pending.job.Status != want {
			t.Fatal("one ambiguous job blocked the whole batch", pending.job, done, err)
		}
		if i == 0 && (drive.listCalls[pending.job.Download.DirectoryID] != 1 || drive.renames != 0 || drive.moves != 0) {
			t.Fatal("definite ambiguity caused repeated reads or a file mutation", drive.listCalls)
		}
	}
	if drive.renames != 1 || drive.moves != 1 || tracker.calls != 2 {
		t.Fatal("other job did not finish independently", drive.renames, drive.moves, tracker.calls)
	}
}

func TestDownloadTitleConflictsWithKnownExtensionsAndQueuedNames(t *testing.T) {
	s, drive, _, _, _, in := gardenFixture(t)
	in.DownloadResourceInput = titleResource("garden", "Show [12]")
	in.Files = []OrganizeSelection{{ID: "title", Season: 1, Episode: 12}}
	first, err := s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(first.ID); err != nil {
		t.Fatal(err)
	}
	// A subsequent real-manifest resource with the same stem cannot bypass the
	// reservation just because its extension is known before downloading.
	other := in
	other.Files = append([]OrganizeSelection{}, in.Files...)
	other.Title, other.Magnet = "Show 合集", "magnet:?xt=urn:btih:"+strings.Repeat("b", 40)
	s.Resources = downloadReaderFunc(func(context.Context, DownloadResourceInput) (DownloadFiles, error) {
		return organizeResourceFiles(strings.Repeat("b", 40), []torrentmeta.File{{Path: "Show [12].mp4"}})
	})
	other.Files[0].ID = "Show [12].mp4"
	second, err := s.Preview(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(second.ID); !errors.Is(err, ErrOrganizeConflict) {
		t.Fatal("queued stem collision accepted", err)
	}
	batch, err := s.PreviewBatch(context.Background(), DownloadBatchInput{DownloadOrganizeInput: in, Resources: []DownloadOrganizeInput{in, other}})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Jobs[0].Items[0].Conflict == "" || batch.Jobs[1].Items[0].Conflict == "" {
		t.Fatal("mixed preview collision not shown", batch)
	}
	// Existing .mkv files are visible to title-only previews before the actual
	// extension is known, without any extra directory read.
	in.Files[0].ID = "title"
	drive.dirs["20"] = []p115.ShareFile{{ID: "21", Name: strings.Split(first.Items[0].Directory, "/")[0], Directory: true}}
	drive.dirs["21"] = []p115.ShareFile{{ID: "22", Name: "Season 01", Directory: true}}
	drive.dirs["22"] = []p115.ShareFile{{ID: "23", Name: first.Items[0].Name + ".mkv"}}
	plan, err := s.Preview(context.Background(), in)
	if err != nil || plan.Items[0].Conflict == "" {
		t.Fatal("existing target extension was ignored", plan, err)
	}
}
