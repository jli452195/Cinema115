package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/naming"
)

type mediaNamingTemplates struct {
	record naming.Snapshot
	calls  int
}

func shareFile(id, name string) p115.ShareFile { return p115.ShareFile{ID: id, Name: name} }

func (s *mediaNamingTemplates) NamingTemplate(ctx context.Context, _ naming.Kind) (naming.Snapshot, error) {
	s.calls++
	return s.record, ctx.Err()
}

type mediaNamingMetadata struct{ calls int }

func (s *mediaNamingMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	s.calls++
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "作品.名称", OriginalTitle: "Original", Date: "2026-01-01"}}, ctx.Err()
}

func TestMediaNamingFileFacts(t *testing.T) {
	for _, tc := range []struct {
		name string
		want map[naming.Field]string
	}{
		{"Movie.2160p.NF.WEB-DL.HEVC.10bit.DV.HDR10.DDP5.1.Atmos.23.976fps.mkv", map[naming.Field]string{naming.Resolution: "2160p", naming.Platform: "NF", naming.Source: "WEB-DL", naming.VideoCodec: "HEVC", naming.BitDepth: "10bit", naming.DynamicRange: "DV HDR10", naming.AudioCodec: "DDP", naming.Channels: "5.1", naming.AudioEffect: "Atmos", naming.FrameRate: "23.976fps"}},
		{"Show.1080p.720p.H264.HEVC.SDR.HDR10.AAC.FLAC.mkv", map[naming.Field]string{naming.Resolution: "", naming.VideoCodec: "", naming.DynamicRange: "", naming.AudioCodec: ""}},
		{"Simply.Unknown.mkv", map[naming.Field]string{naming.Resolution: "", naming.VideoCodec: "", naming.BitDepth: "", naming.DynamicRange: "", naming.ReleaseGroup: ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mediaFileValues(tc.name, DownloadFileSpec{})
			for field, want := range tc.want {
				if got[field] != want {
					t.Errorf("%s = %q, want %q", field, got[field], want)
				}
			}
		})
	}
}

func TestMediaNamingPreviewSharesRendererAndIsolatesRows(t *testing.T) {
	template, _ := naming.Default(naming.TV)
	template.Directories = nil
	template.Filename = []naming.Block{{Field: naming.OriginalTitle}, {Field: naming.SeasonEpisode, Separator: "."}, {Field: naming.Resolution, Separator: "."}, {Field: naming.Custom, Separator: "."}}
	templates := &mediaNamingTemplates{record: naming.Snapshot{Revision: 7, Template: template}}
	metadata := &mediaNamingMetadata{}
	s := MediaNaming{Templates: templates, Metadata: metadata}
	input := MediaNamingInput{Kind: "tv", TMDBID: 42, Files: []MediaNamingFile{
		{OrganizeSelection: OrganizeSelection{ID: "one", Season: 1, Episode: 13, EndEpisode: 14, Suffix: "v3"}, Name: "Show.720p.MKV"},
		{OrganizeSelection: OrganizeSelection{ID: "two", Season: 0, Episode: 2}, Name: "Show.1080p.mp4"},
		{OrganizeSelection: OrganizeSelection{ID: "bad", Season: 1}, Name: "Unknown.mkv"},
	}, Subtitles: []OrganizeSubtitle{{File: OrganizeFile{ShareFile: shareFile("sub", "Show.zh-TW.default.ASS")}, VideoID: "one", Tail: ".zh-TW.default"}}}
	preview, err := s.Preview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if templates.calls != 1 || metadata.calls != 1 || len(preview.Items) != 4 || preview.Items[0].Result.Name != "Original.S01E13-E14.720p.v3.mkv" || preview.Items[1].Result.Name != "Original.S00E02.1080p.mp4" || preview.Items[2].Error == "" || preview.Items[3].Result.Name != "Original.S01E13-E14.720p.v3.zh-TW.default.ass" {
		t.Fatalf("preview: %+v; reads %d/%d", preview.Items, templates.calls, metadata.calls)
	}
	// No TMDB episode-count ceiling and no cloud dependency in this service.
	engine := &OrganizeEngine{templates: templates, metadata: metadata}
	files := make([]OrganizeFile, 0, 2)
	selections := make([]OrganizeSelection, 0, 2)
	for _, file := range input.Files[:2] {
		files = append(files, OrganizeFile{ShareFile: shareFile(file.ID, file.Name), MediaType: "video"})
		selections = append(selections, file.OrganizeSelection)
	}
	plan, err := engine.buildPlan(context.Background(), OrganizeScan{Files: files}, OrganizePreviewInput{Kind: "tv", TMDBID: 42, Files: selections})
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range plan.Items {
		if item.Name != preview.Items[i].Result.Name || item.Directory != "" {
			t.Fatalf("actual plan differs: %+v", item)
		}
	}
	// A provider changing its template must not mutate a prepared snapshot.
	templates.record.Template.Filename[0].Prefix = "changed"
	if preview.Naming.Template.Filename[0].Prefix != "" || plan.Naming.Template.Filename[0].Prefix != "" {
		t.Fatal("snapshot aliased template storage")
	}
}

func TestMediaNamingRevisionAndValidation(t *testing.T) {
	template, _ := naming.Default(naming.TV)
	templates := &mediaNamingTemplates{record: naming.Snapshot{Revision: 4, Template: template}}
	metadata := &mediaNamingMetadata{}
	s := MediaNaming{Templates: templates, Metadata: metadata}
	old := int64(3)
	if _, err := s.prepare(context.Background(), "tv", 42, &old); !errors.Is(err, ErrOrganizeConflict) || metadata.calls != 0 {
		t.Fatal("stale template reached metadata", err)
	}
	for _, input := range []MediaNamingInput{
		{Kind: "tv", TMDBID: 42},
		{Kind: "other", TMDBID: 42, Files: []MediaNamingFile{{}}},
		{Kind: "tv", TMDBID: 42, Files: []MediaNamingFile{{OrganizeSelection: OrganizeSelection{ID: "same"}}, {OrganizeSelection: OrganizeSelection{ID: "same"}}}},
	} {
		if _, err := s.Preview(context.Background(), input); !errors.Is(err, ErrOrganizeInput) {
			t.Fatal("invalid input accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Preview(ctx, MediaNamingInput{Kind: "tv", TMDBID: 42, Files: []MediaNamingFile{{OrganizeSelection: OrganizeSelection{ID: "one"}}}}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestManualNamingSnapshotRootAndRestart(t *testing.T) {
	s, drive, account, store := newOrganizeTest(t)
	template, _ := naming.Default(naming.TV)
	template.Directories = nil
	templates := &mediaNamingTemplates{record: naming.Snapshot{Revision: 2, Template: template}}
	s.templates = templates
	plan := organizeTestPreview(t, s, account, []OrganizeSelection{{ID: "101", Season: 1, Episode: 13, EndEpisode: 14, Suffix: "v3"}})
	want := plan.Items[0].Name
	plan.Naming.Template.Filename[0].Prefix = "client-tamper"
	templates.record.Revision++
	templates.record.Template.Filename[0].Prefix = "new-template"
	if _, err := s.Start(account, plan.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewOrganizeEngine(store, s.accounts, drive, s.metadata, templates)
	if err != nil {
		t.Fatal(err)
	}
	manual := NewManualOrganize(restarted)
	job, err := manual.Resume(plan.ID)
	if err != nil || job.Naming.Revision != 2 || job.Naming.Template.Filename[0].Prefix != "" || job.Items[0].Name != want || job.Items[0].Directory != "" {
		t.Fatal("restart changed original intent", job, err)
	}
	if err := restarted.runJob(context.Background(), &restarted.pending.job, restarted.pending.cookie); err != nil {
		t.Fatal(err)
	}
	finished := manual.History()[0]
	if finished.Status != "completed" || finished.Items[0].TargetCID != "20" || drive.creates != 0 || templates.calls != 1 || len(drive.dirs["20"]) != 2 {
		t.Fatal("root task did not complete with frozen naming", finished, templates.calls)
	}
	if reflect.DeepEqual(finished.Naming.Template, templates.record.Template) {
		t.Fatal("executed latest template")
	}
}

func TestDownloadNamingBatchSnapshotAndStaleRevision(t *testing.T) {
	s, drive, input, reads := downloadBatchFixture(t, 2)
	template, _ := naming.Default(naming.TV)
	templates := &mediaNamingTemplates{record: naming.Snapshot{Revision: 5, Template: template}}
	s.Organize.templates = templates
	old := int64(4)
	input.NamingRevision = &old
	if _, err := s.PreviewBatch(context.Background(), input); !errors.Is(err, ErrOrganizeConflict) || len(drive.listCalls) != 0 || reads.Load() != 2 {
		t.Fatal("stale template did extra cloud or torrent reads", err, drive.listCalls)
	}
	current := int64(5)
	input.NamingRevision = &current
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if templates.calls != 2 || len(preview.Jobs) != 2 {
		t.Fatal("batch loaded template per resource")
	}
	templates.record.Revision++
	templates.record.Template.Filename[0].Prefix = "changed"
	preview.Jobs[0].Naming.Template.Filename[0].Prefix = "client-change"
	accepted, err := s.StartBatch(preview.ID)
	if err != nil || accepted.Jobs[0].Naming.Revision != 5 || accepted.Jobs[0].Naming.Template.Filename[0].Prefix != "" {
		t.Fatal("queued batch lost its snapshot", accepted, err)
	}
}

func TestDownloadNamingKnownSourceGroupFillsMissingFileFacts(t *testing.T) {
	s, _, _, _, transport, input := gardenFixture(t)
	transport.names = []string{"show/Show [01].mkv", "show/[文件字幕组] Show [02].mkv"}
	input.Fansub = "ANi"
	files, err := s.Files(context.Background(), input.DownloadResourceInput)
	if err != nil {
		t.Fatal(err)
	}
	groups := make(map[string]string, len(files.Files))
	for _, file := range files.Files {
		groups[file.Name] = file.NamingValues[naming.ReleaseGroup]
	}
	if groups["Show [01].mkv"] != "ANi" || groups["[文件字幕组] Show [02].mkv"] != "文件字幕组" {
		t.Fatal("source group replaced per-file facts or was lost", groups)
	}
	input.ManifestID = files.ManifestID
	input.Files = []OrganizeSelection{{ID: "show/Show [01].mkv", Season: 1, Episode: 13}}
	plan, err := s.Preview(context.Background(), input)
	if err != nil || !strings.Contains(plan.Items[0].Name, "-ANi.mkv") || len(transport.paths) != 1 {
		t.Fatal("final plan lost source group or refetched manifest", plan, err, transport.paths)
	}
}
