package organize

import (
	"fmt"
	"path"
	"strings"
	"testing"
	"unicode/utf8"

	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
)

func planJob(names ...string) model.AnimeDownloadJob {
	cid, result := "200", "200"
	n := 30
	target := model.AnimeDownloadTarget{SchemaVersion: 1, SettingsRevision: 2, AccountID: "main", ParentCID: "100", ParentPath: "/下载"}
	organized := target
	organized.ParentCID, organized.ParentPath = "900", "/整理"
	episodes := make([]model.AnimeNamingEpisode, 12)
	for i := range episodes {
		episodes[i] = model.AnimeNamingEpisode{ID: fmt.Sprint(1000 + i), Number: i + 1}
	}
	job := model.AnimeDownloadJob{UID: "123", ParentCID: "100", DirectoryCID: &cid, ResultObjectID: &result, DirectoryName: "anime-task"}
	job.Snapshot.Target = target
	job.Snapshot.Source = model.AnimeDownloadSource{SourceID: "main", GroupName: "银色子弹"}
	job.Snapshot.Release.Parse = releaseparse.Result{Kind: "episode", Episode: &n}
	template, _ := naming.Default(naming.TV)
	job.Snapshot.Organization = &model.AnimeOrganizationSnapshot{Naming: naming.Snapshot{Template: template}, SchemaVersion: 1, PolicyVersion: 1, PlanRevision: 7, Target: organized}
	job.Snapshot.Naming = &model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "黑暗召唤师在地下恋爱中", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1, Episodes: episodes}}}, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 1, TargetBase: 1}}}}}
	job.Files = &model.AnimeFileManifest{SchemaVersion: 1, UID: job.UID, ResultID: result, CollectedAt: 1, Entries: []model.AnimeDriveEntry{{ID: cid, ParentCID: job.ParentCID, Name: job.DirectoryName, Kind: "directory"}}}
	for i, name := range names {
		job.Files.Entries = append(job.Files.Entries, model.AnimeDriveEntry{ID: fmt.Sprint(300 + i), ParentCID: cid, Name: name, Kind: "file", Size: 1024})
	}
	return job
}

func TestAnimeOrganizationNamesAndUniqueSubtitles(t *testing.T) {
	job := planJob("[组] 测试 - 25.MKV", "[组] 测试 - 25.zh-Hans.forced.ass", "[组] 测试 - 25.readme.srt", "readme.txt")
	p, err := Build(job)
	if err != nil || p.Ready != 2 || p.NeedsReview != 1 || p.CloudConflictChecked || p.Revision != 7 {
		t.Fatalf("%+v %v", p, err)
	}
	want := "/整理/黑暗召唤师在地下恋爱中 (2026) {tmdb-335790}/Season 01/黑暗召唤师在地下恋爱中.S01E01-银色子弹.mkv"
	if p.Items[0].TargetPath != want || p.Items[1].TargetPath != strings.TrimSuffix(want, ".mkv")+".zh-Hans.forced.ass" || p.Items[1].VideoID != "300" || p.Items[2].Reason != "subtitle_unmatched" || p.Items[3].Status != "preserved" {
		t.Fatalf("%+v", p.Items)
	}
	if p.Items[0].OriginalPath != "/下载/anime-task/[组] 测试 - 25.MKV" {
		t.Fatal(p.Items[0])
	}
	job.Snapshot.Source.GroupName = "另一字幕组"
	other, err := Build(job)
	if err != nil || other.Items[0].TargetPath == want {
		t.Fatal("different groups must coexist", err)
	}
}

func TestAnimeOrganizationZeroWithoutTMDBEpisode(t *testing.T) {
	job := planJob("[桜都字幕组] Mushoku Tensei S2 [00][1080p][简体内嵌].mkv", "[桜都字幕组] Mushoku Tensei S2 [00][1080p][简体内嵌].zh-Hans.ass")
	job.Snapshot.Naming.Sources[0].Segments = []model.AnimeMappingSegment{{First: 0, SourceBase: 0, TargetSeason: 2, TargetBase: 0}}
	p, err := Build(job)
	if err != nil || p.Ready != 2 || p.NeedsReview != 0 || !strings.Contains(p.Items[0].TargetPath, "/Season 02/") || !strings.Contains(p.Items[0].TargetPath, ".S02E00.") || p.Items[1].VideoID != p.Items[0].ID {
		t.Fatalf("zero episode or matching subtitle was blocked: %+v %v", p, err)
	}
}

func TestAnimeOrganizationDoesNotGuessMultiVideoOrSpecial(t *testing.T) {
	for _, tc := range []struct {
		names         []string
		ready, review int
	}{{[]string{"video.mkv"}, 1, 0}, {[]string{"video.mkv", "other.mp4"}, 0, 2}, {[]string{"[组] 测试 - 37.mkv"}, 1, 0}, {[]string{"[组] 测试 - SP1.mkv"}, 0, 1}, {[]string{"[组] 测试 [01-12].mkv"}, 0, 1}, {[]string{"[组] 测试 [01][02].mkv"}, 0, 1}} {
		p, err := Build(planJob(tc.names...))
		if err != nil || p.Ready != tc.ready || p.NeedsReview != tc.review {
			t.Fatalf("%v: %+v %v", tc.names, p, err)
		}
	}
}

func TestAnimeOrganizationFileQualityOverridesSingleReleaseHint(t *testing.T) {
	job := planJob("[组] 测试 - 25 [1080p].mkv")
	job.Snapshot.Release.Title = "[组] 测试 - 25 [720p]"
	p, err := Build(job)
	if err != nil || p.Ready != 1 || !strings.Contains(p.Items[0].TargetPath, ".1080p-") || strings.Contains(p.Items[0].TargetPath, "720p") {
		t.Fatalf("release hint overrode file quality: %+v %v", p, err)
	}
	job.Files.Entries[1].Name = "[组] 测试 - 25 [1080p][2160p].mkv"
	p, err = Build(job)
	if err != nil || p.Ready != 1 || strings.Contains(p.Items[0].TargetPath, "p-") {
		t.Fatalf("conflicting file qualities were guessed from release: %+v %v", p, err)
	}
}

func TestAnimeOrganizationDuplicateTargetsBlockVideosAndSubtitles(t *testing.T) {
	p, err := Build(planJob("[组] 测试 - 25.mkv", "[组] 测试 - 25v2.mkv", "[组] 测试 - 25.ass"))
	if err != nil || p.Ready != 0 || p.NeedsReview != 3 || p.Items[0].Status != "conflict" || p.Items[1].Status != "conflict" || p.Items[2].Reason != "video_needs_review" {
		t.Fatalf("%+v %v", p, err)
	}
	// Two extensions with one basename cannot uniquely identify a subtitle.
	p, err = Build(planJob("video.mkv", "video.mp4", "video.ass"))
	if err != nil || p.Items[2].Reason != "subtitle_unmatched" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestAnimeOrganizationSubtitleDirectoryAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		sub, video, suffix string
		ok                 bool
	}{
		{"k.chs", "K", ".chs", true}, {"K.en", "k", ".en", true}, {"k", "K", "", true}, {"video2.en", "video", "", false}, {"video.zip", "video", "", false},
	} {
		suffix, ok := subtitleSuffix(tc.sub, tc.video)
		if ok != tc.ok || suffix != tc.suffix {
			t.Fatalf("%+v: %q %v", tc, suffix, ok)
		}
	}
	job := planJob("video.mkv", "video.ass")
	job.Files.Entries = append(job.Files.Entries, model.AnimeDriveEntry{ID: "400", ParentCID: "200", Kind: "directory", Name: "subs"})
	job.Files.Entries[2].ParentCID = "400"
	p, err := Build(job)
	if err != nil || p.Items[1].Reason != "subtitle_unmatched" || p.Items[1].OriginalPath != "/下载/anime-task/subs/video.ass" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestAnimeOrganizationCleansAndBoundsUTF8Names(t *testing.T) {
	job := planJob("video.mkv", "video.chs.ass")
	job.Snapshot.Naming.Target.Title = strings.Repeat("动", 160) + "/<>:"
	job.Snapshot.Source.GroupName = strings.Repeat("字", 100) + "/<>:"
	p, err := Build(job)
	if err != nil || p.Ready != 0 || p.Items[0].Reason != "naming_invalid" || p.Items[0].Error == "" {
		t.Fatalf("%+v %v", p, err)
	}
	if p.Items[0].TargetPath != "" {
		t.Fatal("oversized names must not be silently truncated")
	}
	job.Snapshot.Naming.Target.Title = "作品/名称"
	job.Snapshot.Source.GroupName = "字幕组"
	p, err = Build(job)
	if err != nil || p.Ready != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, item := range p.Items {
		if !utf8.ValidString(item.TargetPath) || strings.ContainsAny(item.TargetPath, "<>:") || !strings.HasPrefix(item.TargetPath, "/整理/") {
			t.Fatal(item)
		}
		for _, part := range strings.Split(item.TargetPath, "/") {
			if len(part) > 255 {
				t.Fatal(part)
			}
		}
	}
	if path.Ext(p.Items[0].TargetPath) != ".mkv" {
		t.Fatal(p.Items[0])
	}
}

func TestAnimeOrganizationRejectsBrokenSnapshotAndManifest(t *testing.T) {
	for _, change := range []func(*model.AnimeDownloadJob){
		func(j *model.AnimeDownloadJob) { j.Snapshot.Organization.Target.AccountID = "other" },
		func(j *model.AnimeDownloadJob) { j.Snapshot.Organization.Target.ParentCID = j.ParentCID },
		func(j *model.AnimeDownloadJob) { j.Snapshot.Naming = nil },
		func(j *model.AnimeDownloadJob) { j.Snapshot.Organization.Naming.Template.Kind = naming.Movie },
		func(j *model.AnimeDownloadJob) { j.Snapshot.Organization.Naming.Revision = -1 },
		func(j *model.AnimeDownloadJob) { j.Files.Entries[1].ParentCID = "999" },
		func(j *model.AnimeDownloadJob) { j.Files.Entries[1].Name = "../video.mkv" },
	} {
		job := planJob("video.mkv")
		change(&job)
		if _, err := Build(job); err == nil {
			t.Fatal("invalid saved facts accepted")
		}
	}
	job := planJob("video.mkv")
	job.Files = nil
	if p, err := Build(job); err != nil || p.Mode != "standard" || len(p.Items) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	job.Snapshot.Organization = nil
	if p, err := Build(job); err != nil || len(p.Items) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestAnimeOrganizationSplitSeasonsAndExplicitSpecial(t *testing.T) {
	job := planJob("[组] 测试 - 12.mkv", "[组] 测试 - 13.mkv", "[组] 测试 [SP01].mkv", "[组] 测试 [SP01].zh-Hans.ass")
	job.Snapshot.Naming.Target.Seasons = append(job.Snapshot.Naming.Target.Seasons,
		model.AnimeNamingSeason{Number: 2, Episodes: []model.AnimeNamingEpisode{{ID: "2001", Number: 1}}},
		model.AnimeNamingSeason{Number: 0, Episodes: []model.AnimeNamingEpisode{{ID: "9001", Number: 1}}},
	)
	end := 12
	job.Snapshot.Naming.Sources[0].Segments = []model.AnimeMappingSegment{
		{First: 1, Last: &end, SourceBase: 1, TargetSeason: 1, TargetBase: 1},
		{First: 13, SourceBase: 13, TargetSeason: 2, TargetBase: 1},
	}
	job.Snapshot.Naming.Sources[0].Overrides = []model.AnimeMappingOverride{{RawKind: "special", RawNumber: "SP01", Action: "map", TargetSeason: 0, TargetEpisode: 1}}
	p, err := Build(job)
	if err != nil || p.Ready != 4 || p.NeedsReview != 0 {
		t.Fatalf("split season or explicit special failed: %+v %v", p, err)
	}
	for i, number := range []string{"S01E12", "S02E01", "S00E01", "S00E01"} {
		if !strings.Contains(p.Items[i].TargetPath, number) {
			t.Fatal("incorrect target season", p.Items[i])
		}
	}
	if !strings.HasSuffix(p.Items[3].TargetPath, ".zh-Hans.ass") || p.Items[3].VideoID != "302" {
		t.Fatal("special subtitle lost its explicit video", p.Items[3])
	}
	// TMDB episode metadata does not gate a configured naming offset.
	job.Files.Entries[2].Name = "[组] 测试 - 14.mkv"
	p, err = Build(job)
	if err != nil || p.Items[1].Reason != "mapped" || !strings.Contains(p.Items[1].TargetPath, "S02E02") || !strings.HasSuffix(p.Items[1].OriginalPath, "/[组] 测试 - 14.mkv") {
		t.Fatalf("configured offset was blocked: %+v %v", p, err)
	}
}
