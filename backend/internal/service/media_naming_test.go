package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
	"strm115/backend/internal/utils/torrentmeta"
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

type mediaNamingEnglishMetadata struct {
	mediaNamingMetadata
	englishCalls int
	value        string
	err          error
}

func (s *mediaNamingEnglishMetadata) EnglishTitle(ctx context.Context, _ string, _ int) (string, error) {
	s.englishCalls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 4*time.Second {
		return "", errors.New("optional enrichment needs a bounded deadline")
	}
	return s.value, s.err
}

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
		{"[沸班亚马制作组] 青之箱 第二季 - 03 [BILIBILI WebRip 2160p H265 Veryslow EAC3][简繁内封字幕].mkv", map[naming.Field]string{naming.Platform: "BILIBILI", naming.Source: "WEBRip", naming.Resolution: "2160p", naming.VideoCodec: "HEVC", naming.AudioCodec: "DDP", naming.Languages: "简繁", naming.SubtitleMode: "内封"}},
		{"Show.bilibili.WEB-DL.mkv", map[naming.Field]string{naming.Platform: "BILIBILI", naming.Source: "WEB-DL"}},
		{"Show.[BiLiBiLi][BILIBILI].mkv", map[naming.Field]string{naming.Platform: "BILIBILI"}},
		{"Show.NotBILIBILI.BILIBILIPlus.BILIBILI2.mkv", map[naming.Field]string{naming.Platform: ""}},
		{"Show.BILIBILI.NF.mkv", map[naming.Field]string{naming.Platform: ""}},
		{"Show.Netflix.NF.mkv", map[naming.Field]string{naming.Platform: "NF"}},
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

func TestMediaNamingSubtitleTags(t *testing.T) {
	for _, tc := range []struct {
		name, languages, mode string
	}{
		{"[Nix-Raws] VERTEX FORCE S01E01 [CR WEB-DL 1080p AVC AAC][SC_TC].mkv", "简繁", ""},
		{"xxxx - [简体] GB .mkv", "简体", ""},
		{"Show - 01 GB.mkv", "简体", ""},
		{"Show [01][GB][CHS][SC][简体].mkv", "简体", ""},
		{"Show [01][BIG5][CHT][TC][繁體].mkv", "繁体", ""},
		{"Show [01][简繁日内封].mkv", "简繁日", "内封"},
		{"Show [01][简日双语外挂].mkv", "简日", "外挂"},
		{"Show [01][CHS&CHT][ENG].mkv", "简繁英", ""},
		{"Show [01][繁体内嵌].mp4", "繁体", "内嵌"},
		{"Movie [ENG].mkv", "英语", ""},
		{"Movie [JPN].mkv", "日语", ""},
		{"海洋奇缘2[国英多音轨+简繁英字幕].2024.mkv", "简繁英", ""},
		{"海洋奇缘2[国英多音轨].2024.mkv", "", ""},
		{"Movie.2024.[20GB].mkv", "", ""},
		{"Movie.2024.[20 GB].mkv", "", ""},
		{"Show [01][20 GB][CHT].mkv", "繁体", ""},
		{"[简繁字幕组] 每日字幕故事 [01][1080p].mkv", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mediaFileValues(tc.name, DownloadFileSpec{})
			if got[naming.Languages] != tc.languages || got[naming.SubtitleMode] != tc.mode {
				t.Fatalf("subtitle facts = %q / %q, want %q / %q", got[naming.Languages], got[naming.SubtitleMode], tc.languages, tc.mode)
			}
		})
	}
	// Standard codes stay independent of display order and duplicate aliases.
	hint := DownloadFileSpec{Spec: releaseparse.Spec{Languages: []string{"en", "zh-Hant", "zh-Hans", "zh-Hans"}, SubtitleMode: "soft"}}
	if got := mediaFileValues("Movie.mkv", hint); got[naming.Languages] != "简繁英" {
		t.Fatal("duplicate/reordered languages changed the label", got)
	}
	if got := mediaFileValues("Movie [简体内嵌].mkv", hint); got[naming.Languages] != "简体" || got[naming.SubtitleMode] != "内嵌" {
		t.Fatal("source hints overrode explicit file tags", got)
	}
}

func TestMediaNamingDirectorySubtitleScope(t *testing.T) {
	for _, tc := range []struct {
		name, directory, filename, languages, mode string
		peer                                       bool
	}{
		{"single film", "/海洋奇缘2[国英多音轨+简繁英字幕]", "Moana.2.2024.mkv", "简繁英", "", false},
		{"explicit directory mode", "/Movie[简繁英内封]", "Movie.mkv", "简繁英", "内封", false},
		{"mixed files", "/Movie[简繁英内封]", "Movie.mkv", "", "", true},
		{"file language wins", "/Movie[简繁英内封]", "Movie[CHS].mkv", "简体", "", false},
		{"file mode wins", "/Movie[简繁内封]", "Movie[内嵌].mp4", "简繁", "内嵌", false},
		{"no ancestor inheritance", "/Movie[简繁英内封]/1080p", "Movie.mkv", "", "", false},
		{"audio only", "/Movie[国英多音轨]", "Movie.mkv", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := OrganizeFile{ShareFile: shareFile("one", tc.filename), ParentID: "parent", SourcePath: path.Join(tc.directory, tc.filename), MediaType: "video"}
			files := []OrganizeFile{file, {ShareFile: shareFile("sub", "Movie.ass"), ParentID: "parent", MediaType: "subtitle"}}
			if tc.peer {
				files = append(files, OrganizeFile{ShareFile: shareFile("two", "Other.mkv"), ParentID: "parent", MediaType: "video"})
			}
			mediaDirectorySubtitleValues(files)
			got := files[0].NamingValues
			if got[naming.Languages] != tc.languages || got[naming.SubtitleMode] != tc.mode {
				t.Fatalf("directory fallback = %+v, want %q / %q", got, tc.languages, tc.mode)
			}
		})
	}
}

func TestManualNamingSubtitlePreviewMatchesExecution(t *testing.T) {
	s, drive, account, _ := newOrganizeTest(t)
	drive.dirs["10"][0].Name = "海洋奇缘2[HDR+杜比视界双版本][国英多音轨+简繁英字幕]"
	drive.dirs["30"] = []p115.ShareFile{{ID: "101", Name: "Moana.2.2024.V2.Bluray.2160p.Atmos.TrueHD7.1.DoVi.HDR.x265.10bit-DreamHD.mkv", Size: 100}}
	scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: account, ParentID: "10", ParentPath: "/来源", EntryID: "30"})
	if err != nil || len(scan.Files) != 1 || drive.listCalls["10"] != 1 || drive.listCalls["30"] != 1 {
		t.Fatal("directory tags added drive reads", scan, err, drive.listCalls)
	}
	file := scan.Files[0]
	selection := OrganizeSelection{ID: file.ID}
	preview, err := (MediaNaming{Templates: s.templates, Metadata: s.metadata}).Preview(context.Background(), MediaNamingInput{Kind: "movie", TMDBID: 42, Files: []MediaNamingFile{{OrganizeSelection: selection, Name: file.Name, NamingValues: file.NamingValues}}})
	if err != nil || len(preview.Items) != 1 || preview.Items[0].Result == nil {
		t.Fatal("naming preview", preview, err)
	}
	want := "测试作品 (2024).2160p.BluRay.HEVC.10bit.DV HDR.TRUEHD.7.1.Atmos.[简繁英].mkv"
	plan, err := s.Preview(context.Background(), account, OrganizePreviewInput{ScanID: scan.ID, Kind: "movie", TMDBID: 42, Files: []OrganizeSelection{selection}})
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Name != want || preview.Items[0].Result.Name != want {
		t.Fatal("subtitle naming differs between preview and plan", preview, plan, err)
	}
	finished := organizeTestRun(t, s, account, plan)
	if finished.Status != "completed" || finished.Items[0].Name != want {
		t.Fatal("execution changed the agreed subtitle label", finished)
	}
}

func TestManualNamingSingleSelectionUsesAllSiblings(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		s, drive, account, _ := newOrganizeTest(t)
		drive.dirs["30"] = []p115.ShareFile{{ID: "101", Name: "Movie.mkv", Size: 100}}
		want := "简繁英"
		if mixed {
			drive.dirs["30"] = append(drive.dirs["30"], p115.ShareFile{ID: "103", Name: "Other.mkv", Size: 100})
			want = ""
		}
		scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: account, ParentID: "30", ParentPath: "/来源/Movie[简繁英字幕]", EntryID: "101"})
		if err != nil || len(scan.Files) != 1 || scan.Files[0].NamingValues[naming.Languages] != want || drive.listCalls["30"] != 1 {
			t.Fatal("selecting one file misrepresented its directory or added reads", mixed, scan, err, drive.listCalls)
		}
	}
}

func TestMediaNamingEnglishTitleEnrichment(t *testing.T) {
	for _, tc := range []struct {
		name, english, want string
		failure             bool
	}{
		{"English available", "Rising Impact", "Rising Impact", false},
		{"missing translation", "", "作品.名称", false},
		{"upstream unavailable", "", "作品.名称", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			template, _ := naming.Default(naming.TV)
			template.Directories[0][0].Field = naming.EnglishTitle
			template.Filename[0].Field = naming.EnglishTitle
			templates := &mediaNamingTemplates{record: naming.Snapshot{Template: template}}
			metadata := &mediaNamingEnglishMetadata{value: tc.english}
			if tc.failure {
				metadata.err = errors.New("network unavailable")
			}
			s := MediaNaming{Templates: templates, Metadata: metadata}
			input := MediaNamingInput{Kind: "tv", TMDBID: 42, Files: []MediaNamingFile{
				{OrganizeSelection: OrganizeSelection{ID: "one", Season: 1, Episode: 1}, Name: "Show.S01E01.mkv", NamingValues: map[naming.Field]string{naming.EnglishTitle: "untrusted"}},
				{OrganizeSelection: OrganizeSelection{ID: "two", Season: 1, Episode: 13}, Name: "Show.S01E13.mkv"},
			}}
			preview, err := s.Preview(context.Background(), input)
			if err != nil || metadata.englishCalls != 1 || metadata.calls != 1 {
				t.Fatalf("batch reads: %d/%d, %v", metadata.calls, metadata.englishCalls, err)
			}
			for _, item := range preview.Items {
				if item.Result == nil || !strings.HasPrefix(item.Result.Name, tc.want+".S01E") || !strings.HasPrefix(item.Result.Directory, tc.want+" (2026)") {
					t.Fatalf("wrong title: %+v", item)
				}
			}
			// The default template must not make an English metadata request.
			templates.record.Template, _ = naming.Default(naming.TV)
			if _, err := s.Preview(context.Background(), input); err != nil || metadata.englishCalls != 1 {
				t.Fatal("unused English title caused a request", err)
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
		{OrganizeSelection: OrganizeSelection{ID: "one", Season: 1, Episode: 13}, Name: "Show.S01E13-E14.720p.MKV"},
		{OrganizeSelection: OrganizeSelection{ID: "two", Season: 0, Episode: 2}, Name: "Show.1080p.mp4"},
		{OrganizeSelection: OrganizeSelection{ID: "bad", Season: 1}, Name: "Unknown.mkv"},
	}, Subtitles: []OrganizeSubtitle{{File: OrganizeFile{ShareFile: shareFile("sub", "Show.zh-TW.default.ASS")}, VideoID: "one", Tail: ".zh-TW.default"}}}
	preview, err := s.Preview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if templates.calls != 1 || metadata.calls != 1 || len(preview.Items) != 4 || preview.Items[0].Result.Name != "Original.S01E13.720p.mkv" || preview.Items[1].Result.Name != "Original.S00E02.1080p.mp4" || preview.Items[2].Error == "" || preview.Items[3].Result.Name != "Original.S01E13.720p.zh-TW.default.ass" {
		t.Fatalf("preview: %+v; reads %d/%d", preview.Items, templates.calls, metadata.calls)
	}
	// No TMDB episode-count ceiling and no cloud dependency in this service.
	engine := &OrganizeEngine{templates: templates, metadata: metadata}
	files := make([]OrganizeFile, 0, 2)
	selections := make([]OrganizeSelection, 0, 2)
	for _, file := range input.Files[:2] {
		files = append(files, OrganizeFile{ShareFile: shareFile(file.ID, file.Name), MediaType: "video", Episode: 13, EndEpisode: 14})
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
	template.Filename = []naming.Block{{Field: naming.SeasonEpisode}}
	templates := &mediaNamingTemplates{record: naming.Snapshot{Revision: 2, Template: template}}
	s.templates = templates
	plan := organizeTestPreview(t, s, account, []OrganizeSelection{{ID: "101", Season: 1, Episode: 13}})
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

func TestTitlelessNamingStillRejectsTargetCollisions(t *testing.T) {
	s, drive, account, _ := newOrganizeTest(t)
	s.templates = &mediaNamingTemplates{record: naming.Snapshot{Template: naming.Template{Kind: naming.TV, Filename: []naming.Block{{Field: naming.SeasonEpisode}}}}}
	drive.dirs["20"] = []p115.ShareFile{{ID: "existing", Name: "S01E01.mkv", Size: 100}}
	plan := organizeTestPreview(t, s, account, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}, {ID: "103", Season: 1, Episode: 1}})
	for _, item := range plan.Items {
		if item.File.MediaType == "video" && item.Conflict == "" {
			t.Fatal("same-name target accepted", item)
		}
	}
	if _, err := s.Start(account, plan.ID); err == nil || drive.renames+drive.moves+drive.creates != 0 {
		t.Fatal("conflicting titleless plan executed", err)
	}
}

// Public DBD-Raws filenames sampled from the real Frieren torrent. This isolated
// regression runs normalization through both preview and plan without cloud I/O.
func TestNamingPackWithExtrasAndCompanionSubtitles(t *testing.T) {
	root := "[DBD-Raws][葬送的芙莉莲][01-28TV全集+特典映像][1080P][BDRip][HEVC-10bit][简繁日双语外挂][FLAC][MKV]"
	files := make([]torrentmeta.File, 0, 86)
	for episode := 1; episode <= 28; episode++ {
		stem := fmt.Sprintf("[DBD-Raws][Sousou no Frieren][%02d][1080P][BDRip][HEVC-10bit][FLAC]", episode)
		for _, ext := range []string{".mkv", ".scjp.ass", ".tcjp.ass"} {
			files = append(files, torrentmeta.File{Path: path.Join(root, stem+ext), Size: 100})
		}
	}
	for _, category := range []string{"menu", "Mini anime"} {
		files = append(files, torrentmeta.File{Path: path.Join(root, category, "Show [01].mkv"), Size: 100})
	}
	resources, err := organizeResourceFiles(strings.Repeat("a", 40), files)
	if err != nil {
		t.Fatal(err)
	}
	template, _ := naming.Default(naming.TV)
	template.Filename[0].Field = naming.EnglishTitle
	templates := &mediaNamingTemplates{record: naming.Snapshot{Template: template}}
	metadata := &mediaNamingEnglishMetadata{value: "Frieren: Beyond Journey's End"}
	input := MediaNamingInput{Kind: "tv", TMDBID: 209867, Subtitles: resources.Subtitles}
	scan := OrganizeScan{Files: append([]OrganizeFile{}, resources.Files...)}
	selections := make([]OrganizeSelection, 0, 28)
	for _, file := range resources.Files {
		if file.Episode == 0 {
			continue
		}
		selection := OrganizeSelection{ID: file.ID, Season: file.Season, Episode: file.Episode}
		selections = append(selections, selection)
		input.Files = append(input.Files, MediaNamingFile{OrganizeSelection: selection, Name: file.Name})
	}
	for _, sub := range resources.Subtitles {
		scan.Files = append(scan.Files, sub.File)
	}
	preview, err := (MediaNaming{Templates: templates, Metadata: metadata}).Preview(context.Background(), input)
	if err != nil || len(selections) != 28 || len(preview.Items) != 84 {
		t.Fatalf("selection/preview: %d/%d, %v", len(selections), len(preview.Items), err)
	}
	plan, err := (&OrganizeEngine{templates: templates, metadata: metadata}).buildPlan(context.Background(), scan, OrganizePreviewInput{Kind: input.Kind, TMDBID: input.TMDBID, Files: selections, Subtitles: true})
	if err != nil || len(plan.Items) != 84 {
		t.Fatal("plan", len(plan.Items), err)
	}
	byID := make(map[string]naming.Result, len(preview.Items))
	for _, item := range preview.Items {
		if item.Result == nil {
			t.Fatal(item.Error)
		}
		byID[item.ID] = *item.Result
	}
	for _, item := range plan.Items {
		if item.Name != byID[item.File.ID].Name || item.Directory != byID[item.File.ID].Directory {
			t.Fatal("preview and plan differ", item)
		}
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
