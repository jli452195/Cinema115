package naming_test

import (
	"encoding/json"
	"errors"
	"path"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"strm115/backend/internal/naming"
)

func integer(value int) *int { return &value }

func defaultTemplate(t testing.TB, kind naming.Kind) naming.Template {
	t.Helper()
	template, err := naming.Default(kind)
	if err != nil {
		t.Fatal(err)
	}
	return template
}

func compile(t testing.TB, template naming.Template) *naming.Renderer {
	t.Helper()
	renderer, err := naming.Compile(template)
	if err != nil {
		t.Fatal(err)
	}
	return renderer
}

func TestDefaultNames(t *testing.T) {
	for _, example := range []struct {
		name string
		kind naming.Kind
		data naming.Data
		want string
	}{
		{"movie with known specifications", naming.Movie, naming.Data{Values: map[naming.Field]string{
			naming.Title: "电影", naming.Year: "2026", naming.TMDBID: "123", naming.Resolution: "2160p",
			naming.Source: "WEB-DL", naming.Platform: "NF", naming.VideoCodec: "HEVC", naming.BitDepth: "10bit",
			naming.DynamicRange: "DV HDR10", naming.FrameRate: "24fps", naming.AudioCodec: "TrueHD",
			naming.Channels: "7.1", naming.AudioEffect: "Atmos", naming.Languages: "简繁", naming.SubtitleMode: "外挂",
			naming.Edition: "导演剪辑版", naming.ReleaseGroup: "Group", naming.Custom: "Remux",
		}, OriginalName: "original.MKV"}, "电影 (2026) {tmdb-123}/电影 (2026).2160p.WEB-DL.NF.HEVC.10bit.DV HDR10.24fps.TrueHD.7.1.Atmos.[简繁].外挂.导演剪辑版-Group-Remux.mkv"},
		{"movie without metadata or specifications", naming.Movie, naming.Data{Values: map[naming.Field]string{
			naming.Title: "电影",
		}, OriginalName: "original.mp4"}, "电影/电影.mp4"},
		{"episode beyond metadata count", naming.TV, naming.Data{Values: map[naming.Field]string{
			naming.Title: "剧集", naming.Year: "2026", naming.TMDBID: "456", naming.Resolution: "1080p", naming.ReleaseGroup: "字幕组",
		}, Season: integer(1), Episode: integer(13), OriginalName: "original.MP4"}, "剧集 (2026) {tmdb-456}/Season 01/剧集.S01E13.1080p-字幕组.mp4"},
		{"combined episodes", naming.TV, naming.Data{Values: map[naming.Field]string{
			naming.Title: "剧集",
		}, Season: integer(2), Episode: integer(1), EndEpisode: integer(2), OriginalName: "original.mkv"}, "剧集/Season 02/剧集.S02E01-E02.mkv"},
		{"zero episode and special season", naming.TV, naming.Data{Values: map[naming.Field]string{
			naming.Title: "特典",
		}, Season: integer(0), Episode: integer(0), EndEpisode: integer(0), OriginalName: "special.mkv"}, "特典/Season 00/特典.S00E00.mkv"},
		{"large numbers are not truncated", naming.TV, naming.Data{Values: map[naming.Field]string{
			naming.Title: "长篇",
		}, Season: integer(123), Episode: integer(1500), OriginalName: "original.mkv"}, "长篇/Season 123/长篇.S123E1500.mkv"},
		{"title-only preview never guesses an extension", naming.TV, naming.Data{Values: map[naming.Field]string{
			naming.Title: "剧集", naming.Resolution: "1080p",
		}, Season: integer(1), Episode: integer(3), OriginalName: "release.HEVC.1080p", TitleOnly: true}, "剧集/Season 01/剧集.S01E03.1080p"},
	} {
		t.Run(example.name, func(t *testing.T) {
			got, err := compile(t, defaultTemplate(t, example.kind)).Render(example.data)
			if err != nil || got.RelativePath != example.want {
				t.Fatalf("got %+v, %v; want %s", got, err, example.want)
			}
			if got.Name != got.Stem+got.Extension || got.RelativePath != path.Join(got.Directory, got.Name) {
				t.Fatalf("inconsistent name parts: %+v", got)
			}
		})
	}
}

func TestOptionalDecorationsAndOrdering(t *testing.T) {
	template := naming.Template{Kind: naming.Movie, Directories: [][]naming.Block{
		{{Field: naming.Platform, Prefix: "[", Suffix: "]"}},
		{{Field: naming.Title}, {Field: naming.Year, Separator: " ", Prefix: "(", Suffix: ")"}},
	}, Filename: []naming.Block{
		{Field: naming.Resolution, Separator: " - ", Prefix: "[", Suffix: "]"},
		{Field: naming.Title, Separator: " - "},
		{Field: naming.DynamicRange, Separator: ".", Prefix: "[", Suffix: "]"},
		{Field: naming.ReleaseGroup, Separator: " - ", Prefix: "[", Suffix: "]"},
	}}
	renderer := compile(t, template)
	data := naming.Data{Values: map[naming.Field]string{naming.Title: "作品", naming.ReleaseGroup: "组"}, OriginalName: "file.mkv"}
	got, err := renderer.Render(data)
	if err != nil || got.RelativePath != "作品/作品 - [组].mkv" {
		t.Fatalf("missing fields left decorations: %+v, %v", got, err)
	}
	data.Values[naming.Resolution] = "1080p"
	data.Values[naming.Platform] = "NF"
	got, err = renderer.Render(data)
	if err != nil || got.RelativePath != "[NF]/作品/[1080p] - 作品 - [组].mkv" {
		t.Fatalf("ordering or first separator: %+v, %v", got, err)
	}
	// No global separator cleanup may alter punctuation inside the title.
	data.Values[naming.Title] = "作品 - - 副标题"
	got, err = renderer.Render(data)
	if err != nil || !strings.Contains(got.Name, "作品 - - 副标题") {
		t.Fatalf("title punctuation was rewritten: %+v, %v", got, err)
	}
}

func TestOriginalNameAndProtectedExtension(t *testing.T) {
	renderer := compile(t, naming.Template{Kind: naming.Movie, Filename: []naming.Block{
		{Field: naming.OriginalName}, {Field: naming.Custom, Separator: "-"},
	}})
	for _, example := range []struct {
		source    string
		titleOnly bool
		want      string
	}{
		{"Movie.2026.1080p.MKV", false, "Movie.2026.1080p-修正.mkv"},
		{"Movie.2026.1080p", true, "Movie.2026.1080p-修正"},
		{"Movie", false, "Movie-修正"},
	} {
		got, err := renderer.Render(naming.Data{OriginalName: example.source, TitleOnly: example.titleOnly, Values: map[naming.Field]string{naming.Custom: "修正"}})
		if err != nil || got.Name != example.want {
			t.Fatalf("%q: %+v, %v; want %s", example.source, got, err, example.want)
		}
	}
}

func TestSafeNamesAndByteLimits(t *testing.T) {
	renderer := compile(t, naming.Template{Kind: naming.Movie, Filename: []naming.Block{{Field: naming.Title}}})
	for _, example := range []struct{ title, want string }{
		{`../作品\名称:测试`, "_作品_名称_测试.mkv"},
		{"A\x00B\nC\u007fD", "A_B_C_D.mkv"},
		{" .作品. ", "作品.mkv"},
		{"CON", "_CON.mkv"}, {"LPT1", "_LPT1.mkv"}, {"com¹", "_com¹.mkv"},
		{"AUX.extra", "_AUX.extra.mkv"}, {"COM10", "COM10.mkv"},
		{strings.Repeat("界", 78) + "ab", strings.Repeat("界", 78) + "ab.mkv"},
	} {
		got, err := renderer.Render(naming.Data{Values: map[naming.Field]string{naming.Title: example.title}, OriginalName: "source.mkv"})
		if err != nil || got.Name != example.want || path.IsAbs(got.RelativePath) || !utf8.ValidString(got.Name) {
			t.Fatalf("%q: %+v, %v; want %s", example.title, got, err, example.want)
		}
	}
	for _, title := range []string{strings.Repeat("界", 79), "..", " . "} {
		if _, err := renderer.Render(naming.Data{Values: map[naming.Field]string{naming.Title: title}, OriginalName: "source.mkv"}); !errors.Is(err, naming.ErrInput) {
			t.Fatalf("must reject empty or oversized name %q: %v", title, err)
		}
	}
	// The same title fits a title-only preview but not the actual extension.
	if _, err := renderer.Render(naming.Data{Values: map[naming.Field]string{naming.Title: strings.Repeat("界", 79)}, TitleOnly: true}); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalTitlesAndTitleFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   naming.Kind
		blocks []naming.Block
		values map[naming.Field]string
		want   string
	}{
		{"episode only", naming.TV, []naming.Block{{Field: naming.SeasonEpisode}}, nil, "S01E13.mkv"},
		{"movie without title", naming.Movie, []naming.Block{{Field: naming.TMDBID, Prefix: "tmdb-"}}, map[naming.Field]string{naming.TMDBID: "42"}, "tmdb-42.mkv"},
		{"English title missing", naming.Movie, []naming.Block{{Field: naming.EnglishTitle}}, map[naming.Field]string{naming.Title: "作品"}, "作品.mkv"},
		{"original title blank", naming.Movie, []naming.Block{{Field: naming.OriginalTitle}}, map[naming.Field]string{naming.Title: "作品", naming.OriginalTitle: "  "}, "作品.mkv"},
		{"English title present", naming.Movie, []naming.Block{{Field: naming.EnglishTitle}}, map[naming.Field]string{naming.Title: "作品", naming.EnglishTitle: "English"}, "English.mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			renderer := compile(t, naming.Template{Kind: tc.kind, Filename: tc.blocks})
			data := naming.Data{Values: tc.values, OriginalName: "original.mkv"}
			if tc.kind == naming.TV {
				data.Season, data.Episode = integer(1), integer(13)
			}
			got, err := renderer.Render(data)
			if err != nil || got.Name != tc.want {
				t.Fatalf("got %+v, %v; want %q", got, err, tc.want)
			}
		})
	}
	// Optional specifications can name a file, but cannot produce an empty stem.
	renderer := compile(t, naming.Template{Kind: naming.Movie, Filename: []naming.Block{{Field: naming.Resolution, Prefix: "[", Suffix: "]"}}})
	if _, err := renderer.Render(naming.Data{OriginalName: "original.mkv"}); !errors.Is(err, naming.ErrInput) {
		t.Fatal("empty name accepted", err)
	}
}

func TestRejectInvalidTemplates(t *testing.T) {
	for _, example := range []struct {
		name   string
		change func(*naming.Template)
	}{
		{"unknown kind", func(p *naming.Template) { p.Kind = "other" }},
		{"empty filename", func(p *naming.Template) { p.Filename = nil }},
		{"empty directory", func(p *naming.Template) { p.Directories[0] = nil }},
		{"too many directories", func(p *naming.Template) { p.Directories = make([][]naming.Block, 5) }},
		{"too many blocks", func(p *naming.Template) { p.Filename = make([]naming.Block, 33) }},
		{"unknown field", func(p *naming.Template) { p.Filename[0].Field = "unsupported" }},
		{"extension is not editable", func(p *naming.Template) { p.Filename[0].Field = "extension" }},
		{"duplicate field", func(p *naming.Template) { p.Filename = append(p.Filename, p.Filename[0]) }},
		{"tv field in movie", func(p *naming.Template) { p.Filename = append(p.Filename, naming.Block{Field: naming.EpisodeTitle}) }},
		{"padding text", func(p *naming.Template) { p.Filename[0].Pad = 2 }},
		{"negative padding", func(p *naming.Template) { p.Filename[0].Pad = -1 }},
		{"traversal prefix", func(p *naming.Template) { p.Filename[0].Prefix = "../" }},
		{"absolute suffix", func(p *naming.Template) { p.Filename[0].Suffix = `\outside` }},
		{"control separator", func(p *naming.Template) { p.Filename[0].Separator = "\n" }},
		{"oversized decoration", func(p *naming.Template) { p.Filename[0].Prefix = strings.Repeat("a", 65) }},
		{"invalid utf8", func(p *naming.Template) { p.Filename[0].Suffix = string([]byte{0xff}) }},
	} {
		t.Run(example.name, func(t *testing.T) {
			template := defaultTemplate(t, naming.Movie)
			example.change(&template)
			if _, err := naming.Compile(template); !errors.Is(err, naming.ErrTemplate) {
				t.Fatalf("got %v; want ErrTemplate", err)
			}
		})
	}
	template := defaultTemplate(t, naming.TV)
	template.Filename = []naming.Block{{Field: naming.Title}, {Field: naming.Season}, {Field: naming.Episode}}
	if _, err := naming.Compile(template); !errors.Is(err, naming.ErrTemplate) {
		t.Fatalf("must preserve combined episode ranges: %v", err)
	}
	template = defaultTemplate(t, naming.TV)
	template.Directories[1][0].Pad = 7
	if _, err := naming.Compile(template); !errors.Is(err, naming.ErrTemplate) {
		t.Fatalf("excessive number padding: %v", err)
	}
}

func TestRejectInvalidData(t *testing.T) {
	renderer := compile(t, defaultTemplate(t, naming.TV))
	for _, example := range []struct {
		name   string
		change func(*naming.Data)
	}{
		{"missing season", func(d *naming.Data) { d.Season = nil }},
		{"missing episode", func(d *naming.Data) { d.Episode = nil }},
		{"negative season", func(d *naming.Data) { d.Season = integer(-1) }},
		{"negative episode", func(d *naming.Data) { d.Episode = integer(-1) }},
		{"reversed range", func(d *naming.Data) { d.EndEpisode = integer(0) }},
		{"unknown field", func(d *naming.Data) { d.Values["guess"] = "SDR" }},
		{"computed field override", func(d *naming.Data) { d.Values[naming.SeasonEpisode] = "S01E99" }},
		{"original name override", func(d *naming.Data) { d.Values[naming.OriginalName] = "fake.mkv" }},
		{"oversized value", func(d *naming.Data) { d.Values[naming.Custom] = strings.Repeat("a", 4097) }},
		{"oversized output directory", func(d *naming.Data) { d.Values[naming.Title] = strings.Repeat("a", 241) }},
		{"invalid utf8", func(d *naming.Data) { d.Values[naming.Title] = string([]byte{0xff}) }},
		{"source is a path", func(d *naming.Data) { d.OriginalName = "../file.mkv" }},
		{"source is windows path", func(d *naming.Data) { d.OriginalName = `C:\file.mkv` }},
		{"invalid source utf8", func(d *naming.Data) { d.OriginalName = string([]byte{0xff}) }},
		{"empty extension", func(d *naming.Data) { d.OriginalName = "file." }},
		{"unsafe extension", func(d *naming.Data) { d.OriginalName = "file.mk v" }},
	} {
		t.Run(example.name, func(t *testing.T) {
			data := naming.Data{Values: map[naming.Field]string{naming.Title: "作品", naming.Resolution: "1080p"}, Season: integer(1), Episode: integer(1), OriginalName: "file.mkv"}
			example.change(&data)
			if _, err := renderer.Render(data); !errors.Is(err, naming.ErrInput) {
				t.Fatalf("got %v; want ErrInput", err)
			}
		})
	}
	var empty naming.Renderer
	for _, renderer := range []*naming.Renderer{nil, &empty} {
		if _, err := renderer.Render(naming.Data{}); !errors.Is(err, naming.ErrTemplate) {
			t.Fatalf("uncompiled renderer: %v", err)
		}
	}
}

func TestCatalogAndTemplateSnapshots(t *testing.T) {
	movie, err := naming.Catalog(naming.Movie)
	if err != nil {
		t.Fatal(err)
	}
	tv, err := naming.Catalog(naming.TV)
	if err != nil || len(tv) <= len(movie) {
		t.Fatalf("catalogs: %v", err)
	}
	for _, field := range movie {
		if field.TVOnly || field.ID == naming.SeasonEpisode {
			t.Fatal("movie exposes episode field")
		}
	}
	movie[0].Label = "changed"
	again, _ := naming.Catalog(naming.Movie)
	if again[0].Label == "changed" {
		t.Fatal("catalog is shared mutable state")
	}
	if _, err := naming.Catalog("unknown"); !errors.Is(err, naming.ErrTemplate) {
		t.Fatal(err)
	}
	if _, err := naming.Default("unknown"); !errors.Is(err, naming.ErrTemplate) {
		t.Fatal(err)
	}

	template := defaultTemplate(t, naming.TV)
	encoded, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	var decoded naming.Template
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(template, decoded) {
		t.Fatal("template JSON lost field formats")
	}
	renderer := compile(t, decoded)
	decoded.Directories[0][0].Prefix = "changed"
	decoded.Filename[0].Prefix = "changed"
	template.Directories[0][0].Prefix = "also changed"
	fresh := defaultTemplate(t, naming.TV)
	if fresh.Directories[0][0].Prefix != "" {
		t.Fatal("defaults are shared mutable state")
	}
	// A task uses the compiled snapshot even if its source configuration changes.
	for i := 1; i <= 16; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			got, err := renderer.Render(naming.Data{Values: map[naming.Field]string{naming.Title: "作品"}, Season: integer(1), Episode: integer(i), OriginalName: "file.mkv"})
			if err != nil || !strings.HasPrefix(got.RelativePath, "作品/Season 01/作品.S01E") {
				t.Fatalf("snapshot changed: %+v, %v", got, err)
			}
		})
	}
}

func FuzzRenderRelativePath(f *testing.F) {
	for _, seed := range []string{"作品", "../escape", `C:\outside`, "CON", " . ", "作品 - - 字幕组", "\x00\n"} {
		f.Add(seed)
	}
	template, _ := naming.Default(naming.TV)
	renderer, err := naming.Compile(template)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, title string) {
		got, err := renderer.Render(naming.Data{Values: map[naming.Field]string{naming.Title: title, naming.ReleaseGroup: title}, Season: integer(0), Episode: integer(13), EndEpisode: integer(14), OriginalName: "original.mkv"})
		if err != nil {
			if !errors.Is(err, naming.ErrInput) {
				t.Fatal(err)
			}
			return
		}
		if !utf8.ValidString(got.RelativePath) || path.IsAbs(got.RelativePath) || strings.Contains(got.RelativePath, `\`) || got.Extension != ".mkv" {
			t.Fatalf("unsafe output: %+v", got)
		}
		for _, part := range strings.Split(got.RelativePath, "/") {
			if part == "" || part == "." || part == ".." || len(part) > 240 || strings.ContainsAny(part, `:*?"<>|`) {
				t.Fatalf("unsafe path component %q", part)
			}
		}
	})
}

func BenchmarkRender(b *testing.B) {
	renderer := compile(b, defaultTemplate(b, naming.TV))
	data := naming.Data{Values: map[naming.Field]string{naming.Title: "剧集名称", naming.Year: "2026", naming.TMDBID: "335790", naming.Resolution: "1080p", naming.VideoCodec: "HEVC", naming.BitDepth: "10bit", naming.ReleaseGroup: "字幕组"}, Season: integer(1), Episode: integer(13), OriginalName: "original.mkv"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := renderer.Render(data); err != nil {
			b.Fatal(err)
		}
	}
}
