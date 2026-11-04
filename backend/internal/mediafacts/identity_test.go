package mediafacts

import "testing"

func TestParseIdentity(t *testing.T) {
	for _, tc := range []struct {
		file    string
		parents []string
		want    Identity
	}{
		{"Moana.2.2024.V2.Bluray.2160p.HEVC.mkv", nil, Identity{Name: "Moana 2 2024", AlternativeName: "Moana 2", AlternativeYear: "2024", Season: 1}},
		{"Show.S02E13.1080p.WEB-DL.strm", nil, Identity{Name: "Show", Kind: "tv", Season: 2, SeasonExplicit: true}},
		{"E03.strm", []string{"Season 02", "剧集 (2024) {tmdb-42}"}, Identity{Name: "剧集", Year: "2024", TMDBID: 42, Kind: "tv", Season: 2, YearFromParent: true, SeasonExplicit: true}},
		{"[Nix-Raws] VERTEX FORCE S01E01 [CR WEB-DL 1080p AVC AAC][SC_TC].mkv", nil, Identity{Name: "VERTEX FORCE", Kind: "tv", Season: 1, SeasonExplicit: true}},
		{"[Cleo]Trinity_Blood_-_03_(Dual Audio_10bit_BD1080p_x265).mkv", nil, Identity{Name: "Trinity Blood", Season: 1}},
		{"01.mkv", []string{"第二季", "作品 (2026)"}, Identity{Name: "作品", Year: "2026", Kind: "tv", Season: 2, YearFromParent: true, SeasonExplicit: true}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if got := identityWithoutSources(ParseIdentity(tc.file, tc.parents...)); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestParseIdentityMixedCollection(t *testing.T) {
	const collection = "【动漫】七大罪.2015-2022.TV+剧场版+OAD 大合集.1080P.日语中字"
	for _, tc := range []struct {
		file, parent string
		want         Identity
	}{
		{"Nanatsu no Taizai 01 [BD 1920x1080 HEVC 10bit CHS].mkv", "七大罪.2017.全24集.1080P.日语内封中字", Identity{Name: "Nanatsu no Taizai", ParentName: "七大罪", Year: "2017", YearFromParent: true, Season: 1}},
		{"Nanatsu no Taizai 24 [BD 1920x1080 HEVC 10bit CHS].mkv", "七大罪.2017.全24集.1080P.日语内封中字", Identity{Name: "Nanatsu no Taizai", ParentName: "七大罪", Year: "2017", YearFromParent: true, Season: 1}},
		{"[SAIO-Raws] Nanatsu no Taizai Imashime no Fukkatsu 01 [BD 1920x1080 HEVC-10bit OPUS ASSx2].mkv", "七大罪 戒律的复活+圣战的预兆 2017-2018.1080P.日语内封中字", Identity{Name: "Nanatsu no Taizai Imashime no Fukkatsu", ParentName: "七大罪 戒律的复活+圣战的预兆", Season: 1}},
		{"[BeanSub][Nanatsu_no_Taizai_Fundo_no_Shinpan][01][GB][1080P][x264_AAC].mp4", "七大罪 愤怒的审判 2021.全24集.1080P.日语中字", Identity{Name: "Nanatsu no Taizai Fundo no Shinpan", ParentName: "七大罪 愤怒的审判", Year: "2021", YearFromParent: true, Season: 1}},
		{"[BeanSub][The_Seven_Deadly_Sins-Hikari_ni_Norowareshi_Mono_tachi][MOVIE][GB][1080P][x264_AAC].mp4", "七大罪剧场版 光芒诅咒之人 2021.1080P.日语中字", Identity{Name: "The Seven Deadly Sins-Hikari ni Norowareshi Mono tachi", ParentName: "七大罪剧场版 光芒诅咒之人", Kind: "movie", Year: "2021", YearFromParent: true, Season: 1}},
		{"[DragsterPS] The Seven Deadly Sins the Movie - Prisoners of the Sky [1080p] [Multi-Audio] [Multi-Subs] [6FA5BD15].mkv", "七大罪剧场版 天空的囚人 2018.1080P.内封多国字幕多国音轨", Identity{Name: "The Seven Deadly Sins the Movie - Prisoners of the Sky", ParentName: "七大罪剧场版 天空的囚人", Kind: "movie", Year: "2018", YearFromParent: true, Season: 1}},
		{"[Ohys-Raws] Gekijouban Nanatsu no Taizai Tenkuu no Torawarebito (BD 1920x1080 x264 FLACx2).mkv", "七大罪剧场版 天空的囚人 2018.1080P.内封多国字幕多国音轨", Identity{Name: "Gekijouban Nanatsu no Taizai Tenkuu no Torawarebito", ParentName: "七大罪剧场版 天空的囚人", Kind: "movie", Year: "2018", YearFromParent: true, Season: 1}},
		{"[BeanSub][Nanatsu no Taizai][OAD][01][GB][480P][MP4].mp4", "Season 03", Identity{Name: "Nanatsu no Taizai", Kind: "tv", Season: 1}},
		{"[SAIO-Raws] Nanatsu no Taizai Imashime no Fukkatsu NCOP 01 [BD 1920x1080 HEVC-10bit OPUS].mkv", "Season 03", Identity{Name: "Nanatsu no Taizai Imashime no Fukkatsu", Kind: "tv", Season: 1}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if got := identityWithoutSources(ParseIdentity(tc.file, tc.parent, collection)); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestParseIdentityPreservesTitlesAndExplicitDeclarations(t *testing.T) {
	for _, tc := range []struct {
		file    string
		parents []string
		want    Identity
	}{
		{"1917.2019.1080p.BluRay.mkv", nil, Identity{Name: "1917", Year: "2019", Season: 1}},
		{"2001.A.Space.Odyssey.1968.1080p.BluRay.mkv", nil, Identity{Name: "2001 A Space Odyssey", Year: "1968", Season: 1}},
		{"Blade.Runner.2049.2017.1080p.mkv", nil, Identity{Name: "Blade Runner 2049", Year: "2017", Season: 1}},
		{"Planet 51 [1080p].mkv", nil, Identity{Name: "Planet 51", Season: 1}},
		{"[Cleo]Planet 51 [1080p].mkv", nil, Identity{Name: "Planet 51", Season: 1}},
		{"Moana 2 [BD 1920x1080].mkv", nil, Identity{Name: "Moana 2", Season: 1}},
		{"Movie 43.2013.1080p.mkv", nil, Identity{Name: "Movie 43 2013", AlternativeName: "Movie 43", AlternativeYear: "2013", Kind: "movie", Season: 1}},
		{"Film Stars Don't Die in Liverpool.2017.1080p.mkv", nil, Identity{Name: "Film Stars Don't Die in Liverpool 2017", AlternativeName: "Film Stars Don't Die in Liverpool", AlternativeYear: "2017", Kind: "movie", Season: 1}},
		{"[真正作品标题][01][1080p].mkv", nil, Identity{Name: "真正作品标题", Season: 1}},
		{"[TrinityBlood][01][1080p].mkv", nil, Identity{Name: "TrinityBlood", Season: 1}},
		{"[Submarine][01][1080p].mkv", nil, Identity{Name: "Submarine", Season: 1}},
		{"[Bank][01][1080p].mkv", nil, Identity{Name: "Bank", Season: 1}},
		{"[TrinityBlood] - 01 [1080p].mkv", nil, Identity{Name: "TrinityBlood", Season: 1}},
		{"[Bank] - 01.mkv", nil, Identity{Name: "Bank", Season: 1}},
		{"[REC].2007.1080p.mkv", nil, Identity{Name: "REC 2007", AlternativeName: "REC", AlternativeYear: "2007", Season: 1}},
		{"The Specials (2019).mkv", nil, Identity{Name: "The Specials", Year: "2019", Season: 1}},
		{"Bonus (2019).mkv", nil, Identity{Name: "Bonus", Year: "2019", Season: 1}},
		{"Show [Special 01].mkv", nil, Identity{Name: "Show", Kind: "tv", Season: 1}},
		{"Show.S03E04.2024.1080p.mkv", []string{"Unrelated.2021", "Other.2020"}, Identity{Name: "Show", ParentName: "Unrelated", Kind: "tv", Year: "2024", Season: 3, SeasonExplicit: true}},
		{"Show S01E01 [1920×1080].mkv", nil, Identity{Name: "Show", Kind: "tv", Season: 1, SeasonExplicit: true}},
		{"Show S01E01 [1920X1080].mkv", nil, Identity{Name: "Show", Kind: "tv", Season: 1, SeasonExplicit: true}},
		{"Show.OAD.S00E02.1080p.mkv", []string{"Season 03"}, Identity{Name: "Show", Kind: "tv", Season: 0, SeasonExplicit: true}},
		{"Show.01.1080p.mkv", []string{"Show 2015-2022"}, Identity{Name: "Show 01", ParentName: "Show", Season: 1}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if got := identityWithoutSources(ParseIdentity(tc.file, tc.parents...)); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func identityWithoutSources(value Identity) Identity {
	value.NameSource, value.YearSource, value.KindSource = "", "", ""
	value.TMDBIDSource, value.SeasonSource, value.ContentTypeSource = "", "", ""
	return value
}

func TestParseIdentityAmbiguousYearsAndBracketTitles(t *testing.T) {
	for _, tc := range []struct {
		file string
		want Identity
	}{
		{"[TrinityBlood] - S01E01.mkv", Identity{Name: "TrinityBlood", Kind: "tv", Season: 1, SeasonExplicit: true}},
		{"[TrinityBlood] - S00E01 - OVA#1.mkv", Identity{Name: "TrinityBlood", Kind: "tv", Season: 0, SeasonExplicit: true}},
		{"[Bank] - S02E01.mkv", Identity{Name: "Bank", Kind: "tv", Season: 2, SeasonExplicit: true}},
		{"Planet 51 [1080p].mkv", Identity{Name: "Planet 51", Season: 1}},
		{"Movie 43 [1080p].mkv", Identity{Name: "Movie 43", Kind: "movie", Season: 1}},
		{"Blade Runner 2049.mkv", Identity{Name: "Blade Runner 2049", AlternativeName: "Blade Runner", AlternativeYear: "2049", Season: 1}},
		{"Blade.Runner.2049.1080p.mkv", Identity{Name: "Blade Runner 2049", AlternativeName: "Blade Runner", AlternativeYear: "2049", Season: 1}},
		{"Known.Movie.2024.1080p.mkv", Identity{Name: "Known Movie 2024", AlternativeName: "Known Movie", AlternativeYear: "2024", Kind: "movie", Season: 1}},
		{"Known Movie (2024).1080p.mkv", Identity{Name: "Known Movie", Year: "2024", Kind: "movie", Season: 1}},
		{"Known Movie [2024].1080p.mkv", Identity{Name: "Known Movie", Year: "2024", Kind: "movie", Season: 1}},
		{"1917.2019.mkv", Identity{Name: "1917", Year: "2019", Season: 1}},
		{"2001.A.Space.Odyssey.1968.mkv", Identity{Name: "2001 A Space Odyssey", Year: "1968", Season: 1}},
		{"Blade.Runner.2049.2017.mkv", Identity{Name: "Blade Runner 2049", Year: "2017", Season: 1}},
		{"Show.2024.S01E01.mkv", Identity{Name: "Show", Year: "2024", Kind: "tv", Season: 1, SeasonExplicit: true}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if got := identityWithoutSources(ParseIdentity(tc.file)); got != tc.want {
				t.Fatalf("got %+v; want %+v", got, tc.want)
			}
		})
	}
}

func TestParseIdentityDeclarationSources(t *testing.T) {
	got := ParseIdentity("E03.strm", "Season 02", "剧集 (2024) {tmdb-42}")
	want := Identity{Name: "剧集", Year: "2024", TMDBID: 42, Kind: "tv", Season: 2, YearFromParent: true, SeasonExplicit: true,
		NameSource: "parent:2", YearSource: "parent:2", KindSource: "parent:1", TMDBIDSource: "parent:2", SeasonSource: "parent:1"}
	if got != want {
		t.Fatalf("got %+v; want %+v", got, want)
	}
	got = ParseIdentity("進擊的巨人 [真人版] (2024) S01E01 {tmdb-42}.mkv")
	if got.Name != "進擊的巨人" || got.ContentType != "live_action" || got.NameSource != "filename" || got.YearSource != "filename" || got.KindSource != "filename" || got.SeasonSource != "filename" || got.TMDBIDSource != "filename" || got.ContentTypeSource != "filename" {
		t.Fatalf("文件声明应保留原始标题及各字段来源：%+v", got)
	}
	// 作为分组键的身份必须始终可比较，不能加入切片或映射字段。
	if identities := map[Identity]bool{got: true}; !identities[got] {
		t.Fatal("身份不能作为分组键")
	}
}

func TestParseIdentityExplicitContentTypesAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		file, parent, content, conflict string
	}{
		{"堀与宫村 [动画] S01E01.mkv", "", "animation", ""},
		{"堀與宮村【動畫版】S01E01.mkv", "", "animation", ""},
		{"[ANIME] Show S01E01.mkv", "", "animation", ""},
		{"[Live-Action] Show S01E01.mkv", "", "live_action", ""},
		{"［ＬＩＶＥ－ＡＣＴＩＯＮ］ Show S01E01.mkv", "", "live_action", ""},
		{"Show S01E01.mkv", "真人版", "live_action", ""},
		{"Show S01E01.mkv", "动漫", "animation", ""},
		{"[BeanSub] Show S01E01.mkv", "动画人生 (2024)", "", ""},
		{"Animation Life S01E01.mkv", "", "", ""},
		{"Show [动画] [真人版] S01E01.mkv", "", "animation", "CONTENT_TYPE_CONFLICT"},
		{"Show [动画] S01E01.mkv", "[真人版]", "animation", "CONTENT_TYPE_CONFLICT"},
		{"Show S01E01 {tmdb-42} {tmdbid=43}.mkv", "", "", "TMDB_ID_CONFLICT"},
		{"Show S01E01 {tmdb-42}.mkv", "Show {tmdb-43}", "", "TMDB_ID_CONFLICT"},
		{"Show S01E01 {tmdb-42}.mkv", "Show {tmdb-42}", "", ""},
	} {
		t.Run(tc.file+"/"+tc.parent, func(t *testing.T) {
			got := ParseIdentity(tc.file, tc.parent)
			if got.ContentType != tc.content || got.Conflict != tc.conflict {
				t.Fatalf("got %+v; want content=%q conflict=%q", got, tc.content, tc.conflict)
			}
		})
	}
}
