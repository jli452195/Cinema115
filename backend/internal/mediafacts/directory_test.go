package mediafacts

import (
	"strings"
	"testing"
)

func TestResolveIdentityUsesDirectoryRolesAndAllAncestors(t *testing.T) {
	ctx, err := ParseDirectoryContext("/Cinema115/待整理目录", "剧集/日番/作品 (2024) {tmdb-42}/1080p HEVC/Season 02/[CHS]/S02E03 - 第 3 话.mkv")
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveIdentity("S02E03 - 第 3 话.mkv", ctx)
	if got.Name != "作品" || got.Year != "2024" || got.TMDBID != 42 || got.Kind != "tv" || got.ContentType != "animation" || got.Season != 2 || !got.YearFromParent {
		t.Fatalf("目录应逐字段补缺: %+v", got)
	}
	if got.NameSource != "parent:4" || got.YearSource != "parent:4" || got.TMDBIDSource != "parent:4" || got.SeasonSource != "filename" || ctx.WorkPath != "/Cinema115/待整理目录/剧集/日番/作品 (2024) {tmdb-42}" {
		t.Fatalf("继承位置不正确: %+v, %+v", got, ctx)
	}
	if ctx.Ancestors[0].Role != DirectoryTechnical || ctx.Ancestors[1].Role != DirectorySeason || ctx.Ancestors[2].Role != DirectoryTechnical || ctx.Ancestors[3].Role != DirectoryWork || ctx.Ancestors[4].Role != DirectoryCategory {
		t.Fatalf("目录角色不正确: %+v", ctx.Ancestors)
	}
}

func TestDirectoryContextIncludesRootWorkAndAllowsMissingMetadata(t *testing.T) {
	for _, tc := range []struct {
		root, file, name, year string
		id                     int
	}{
		{"/library/Show (2024) {tmdb-42}", "Season 01/S01E02.mkv", "Show", "2024", 42},
		{"/library/擅长捉弄的高木同学", "Season 01/S01E02.mkv", "擅长捉弄的高木同学", "", 0},
		{"/library/Show", "S01E02.mkv", "Show", "", 0},
	} {
		ctx, err := ParseDirectoryContext(tc.root, tc.file)
		if err != nil {
			t.Fatal(err)
		}
		got := ResolveIdentity(tc.file[strings.LastIndex(tc.file, "/")+1:], ctx)
		if got.Name != tc.name || got.Year != tc.year || got.TMDBID != tc.id || ctx.WorkPath != tc.root {
			t.Fatalf("规则根的作品声明应可使用: %+v, %+v", got, ctx)
		}
	}
}

func TestDirectoryContextCannotTurnCollectionsOrCategoriesIntoWork(t *testing.T) {
	for _, relative := range []string{
		"剧集/日番/1080p/S01E02.mkv",
		"合集/Season 01/S01E02.mkv",
		"七大罪.2015-2022.TV+剧场版+OAD 大合集/Season 01/S01E02.mkv",
		"Show (2024) {tmdb-42}/作品合集/Season 01/S01E02.mkv",
	} {
		ctx, err := ParseDirectoryContext("/Cinema115/待整理目录", relative)
		if err != nil {
			t.Fatal(err)
		}
		got := ResolveIdentity("S01E02.mkv", ctx)
		if got.Name != "" || got.Year != "" || got.TMDBID != 0 || ctx.WorkPath != "" {
			t.Fatalf("%q 不应提供作品身份: %+v, %+v", relative, got, ctx)
		}
	}
	ctx, err := ParseDirectoryContext("/Cinema115/待整理目录", "七大罪.2015-2022.TV+剧场版合集/七大罪 (2017) {tmdb-42}/Season 01/S01E02.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveIdentity("S01E02.mkv", ctx); got.Name != "七大罪" || got.Year != "2017" || got.TMDBID != 42 {
		t.Fatalf("合集内独立子作品仍应可用: %+v", got)
	}
}

func TestResolveIdentityPreservesFilenameAndConflicts(t *testing.T) {
	ctx, err := ParseDirectoryContext("/root", "父目录作品 (2024) {tmdb-42} [动画]/Season 02/Other.S03E04.mkv")
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveIdentity("Other (2025) [真人版] {tmdb-43} - S03E04.mkv", ctx)
	if got.Name != "Other" || got.ParentName != "父目录作品" || got.Year != "2025" || got.YearFromParent || got.TMDBID != 43 || got.Season != 3 || got.Conflict != "CONTENT_TYPE_CONFLICT,TMDB_ID_CONFLICT" {
		t.Fatalf("文件声明优先，明确冲突应可见: %+v", got)
	}
	ctx, err = ParseDirectoryContext("/root", "Show (2024)/Specials/Show.S01E01 - OVA13.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if got = ResolveIdentity("Show.S01E01 - OVA13.mkv", ctx); got.Season != 1 || got.SeasonSource != "filename" {
		t.Fatalf("特殊目录不应否定文件明确季号: %+v", got)
	}
	if got = ResolveIdentity("Show OVA13.mkv", ctx); got.Season != 0 || !got.SeasonExplicit {
		t.Fatalf("特殊目录只提供季零提示，不补造具体集号: %+v", got)
	}
}

func TestDirectoryContextPreservesBareYearAndNumericFilmAmbiguity(t *testing.T) {
	for _, title := range []string{"Blade Runner 2049", "Planet 51"} {
		ctx, err := ParseDirectoryContext("/root", title+"/"+title+".mkv")
		if err != nil {
			t.Fatal(err)
		}
		got := ResolveIdentity(title+".mkv", ctx)
		if got.Name != title || got.Year != "" || got.Kind != "" || got.SeasonExplicit {
			t.Fatalf("重复目录名不是独立年份或集数证据: %+v", got)
		}
		if title == "Blade Runner 2049" && (got.AlternativeName != "Blade Runner" || got.AlternativeYear != "2049") {
			t.Fatalf("裸年份的另一种解释应保留: %+v", got)
		}
	}
	ctx, err := ParseDirectoryContext("/root", "噬/Season 01/S01E01.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveIdentity("S01E01.mkv", ctx); got.Name != "" {
		t.Fatalf("单字简写不能补造完整作品: %+v", got)
	}
	if got := ResolveIdentity("噬血狂袭.S01E01.mkv", ctx); got.Name != "噬血狂袭" || got.ParentName != "噬" {
		t.Fatalf("完整文件名优先，简写只留作目录候选: %+v", got)
	}
}

func TestDirectoryEpisodeCountKeepsExistingReleaseYearContext(t *testing.T) {
	const filename = "[BeanSub][Nanatsu_no_Taizai_Fundo_no_Shinpan][01][GB][1080P][x264_AAC].mp4"
	for _, tc := range []struct {
		directory, title, year string
	}{
		{"七大罪 愤怒的审判 2021.全24集.1080P.日语中字", "七大罪 愤怒的审判", "2021"},
		{"七大罪.2017.全24集.1080P.日语内封中字", "七大罪", "2017"},
	} {
		t.Run(tc.directory, func(t *testing.T) {
			ctx, err := ParseDirectoryContext("/root", tc.directory+"/"+filename)
			if err != nil {
				t.Fatal(err)
			}
			got := ResolveIdentity(filename, ctx)
			if got.Name != "Nanatsu no Taizai Fundo no Shinpan" || got.ParentName != tc.title || got.Year != tc.year || !got.YearFromParent || got.YearSource != "parent:1" {
				t.Fatalf("目录明确总集数的年份上下文不能丢失: %+v", got)
			}
			if ctx.Identity.Name != tc.title || ctx.Identity.AlternativeName != "" || ctx.Identity.AlternativeYear != "" {
				t.Fatalf("年份应独立于目录作品名: %+v", ctx.Identity)
			}
		})
	}
}

func TestDirectoryContextEnforcesRootAndDepth(t *testing.T) {
	for _, tc := range [][2]string{
		{"/root", "../outside/Show.mkv"},
		{"/root", "work/../Show.mkv"},
		{"/root", "/root-other/Show.mkv"},
		{"/root", "/other/Show.mkv"},
		{"/root", "/root"},
		{".", "/outside/Show.mkv"},
		{"D:/source", "E:/source/Show.mkv"},
		{"D:/source", "D:Show.mkv"},
		{"/root", strings.Repeat("layer/", 17) + "S01E01.mkv"},
	} {
		if _, err := ParseDirectoryContext(tc[0], tc[1]); err == nil {
			t.Fatalf("应拒绝越界或超限路径: %q, %q", tc[0], tc[1])
		}
	}
	ctx, err := ParseDirectoryContext(`D:\SOURCE\Show (2024)`, `d:\source\Show (2024)\Season 01\S01E02.mkv`)
	if err != nil || ctx.Identity.Name != "Show" || ctx.Identity.Year != "2024" {
		t.Fatalf("Windows 绝对路径应在边界内正确继承: %+v, %v", ctx, err)
	}
	ctx, err = ParseDirectoryContext(`D:\`, `d:\Show (2024)\S01E02.mkv`)
	if err != nil || ctx.Identity.Name != "Show" {
		t.Fatalf("Windows 盘符根应保留路径边界: %+v, %v", ctx, err)
	}
}

func TestDirectoryTechnicalLayersDoNotSupplyWorkIdentity(t *testing.T) {
	for _, name := range []string{"BD 1920x1080 HEVC-10bit OPUS ASSx2", "[1080p][x264][AAC]", "DTS-HD MA 7.1", "VCB-Studio", "Nix-Raws", "LoliHouse"} {
		if fact := directoryFact(name, "/root/"+name, "parent:1"); fact.Role != DirectoryTechnical || fact.Identity.Name != "" {
			t.Fatalf("技术包装不能成为作品: %+v", fact)
		}
	}
	if fact := directoryFact("Cleo Show (2024)", "/root/Cleo Show (2024)", "parent:1"); fact.Role != DirectoryWork || fact.Identity.Name != "Cleo Show" {
		t.Fatalf("发布组同名开头不应删除真正的作品目录: %+v", fact)
	}
}
