package mediacategory_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"strm115/backend/internal/mediacategory"
)

const simpleYAML = `movie: {fallback: 其他电影, rules: []}
tv:
  fallback: 其他剧集
  rules:
    - name: 动漫
      match: {genres: [16]}
`

func TestRejectInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct{ name, old, replacement, field string }{
		{"missing section", "movie: {fallback: 其他电影, rules: []}\n", "", "$.movie"},
		{"unknown root field", "movie:", "extra: true\nmovie:", "$.extra"},
		{"duplicate root key", "movie:", "movie: {fallback: 其他电影, rules: []}\nmovie:", "$.movie"},
		{"null section", "movie: {fallback: 其他电影, rules: []}", "movie: null", "movie"},
		{"missing fallback", "movie: {fallback: 其他电影, rules: []}", "movie: {rules: []}", "movie.fallback"},
		{"empty fallback", "fallback: 其他电影", "fallback: ''", "movie.fallback"},
		{"numeric fallback", "fallback: 其他电影", "fallback: 123", "movie.fallback"},
		{"missing rules", "movie: {fallback: 其他电影, rules: []}", "movie: {fallback: 其他电影}", "movie.rules"},
		{"null rules", "rules: []", "rules: null", "movie.rules"},
		{"mapping rules", "rules: []", "rules: {}", "movie.rules"},
		{"unknown section field", "fallback: 其他电影, rules: []", "fallback: 其他电影, rules: [], enabled: true", "movie.enabled"},
		{"duplicate fallback", "fallback: 其他电影, rules: []", "fallback: 其他电影, fallback: 其他, rules: []", "movie.fallback"},
		{"missing name", "name: 动漫", "other: 动漫", "tv.rules[0].other"},
		{"empty name", "name: 动漫", "name: ' '", "tv.rules[0].name"},
		{"numeric name", "name: 动漫", "name: 123", "tv.rules[0].name"},
		{"removed path", "name: 动漫", "name: 动漫\n      path: 剧集/动漫", "tv.rules[0].path"},
		{"missing match", "      match: {genres: [16]}\n", "", "tv.rules[0].match"},
		{"null match", "match: {genres: [16]}", "match: null", "tv.rules[0].match"},
		{"empty match", "match: {genres: [16]}", "match: {}", "tv.rules[0].match"},
		{"condition typo", "genres: [16]", "genre: [16]", "tv.rules[0].match.genre"},
		{"unsupported condition", "genres: [16]", "year: [2026]", "tv.rules[0].match.year"},
		{"removed origin condition", "genres: [16]", "origin_countries: [JP]", "tv.rules[0].match.origin_countries"},
		{"removed production condition", "genres: [16]", "production_countries: [JP]", "tv.rules[0].match.production_countries"},
		{"empty condition", "genres: [16]", "genres: []", "tv.rules[0].match.genres"},
		{"null condition", "genres: [16]", "genres: null", "tv.rules[0].match.genres"},
		{"scalar condition", "genres: [16]", "genres: 16", "tv.rules[0].match.genres"},
		{"duplicate condition", "genres: [16]", "genres: [16], genres: [99]", "tv.rules[0].match.genres"},
		{"duplicate genre", "genres: [16]", "genres: [16, 16]", "tv.rules[0].match.genres[1]"},
		{"zero genre", "genres: [16]", "genres: [0]", "tv.rules[0].match.genres[0]"},
		{"negative genre", "genres: [16]", "genres: [-16]", "tv.rules[0].match.genres[0]"},
		{"overflow genre", "genres: [16]", "genres: [9999999999999999999999999]", "tv.rules[0].match.genres[0]"},
		{"string genre", "genres: [16]", "genres: ['16']", "tv.rules[0].match.genres[0]"},
		{"fractional genre", "genres: [16]", "genres: [16.5]", "tv.rules[0].match.genres[0]"},
		{"boolean genre", "genres: [16]", "genres: [true]", "tv.rules[0].match.genres[0]"},
		{"null genre", "genres: [16]", "genres: [null]", "tv.rules[0].match.genres[0]"},
		{"duplicate country", "genres: [16]", "countries: [JP, ' jp ']", "tv.rules[0].match.countries[1]"},
		{"duplicate language", "genres: [16]", "languages: [ja, JA]", "tv.rules[0].match.languages[1]"},
		{"empty code", "genres: [16]", "countries: ['']", "tv.rules[0].match.countries[0]"},
		{"long code", "genres: [16]", "countries: [JPN]", "tv.rules[0].match.countries[0]"},
		{"numeric code", "genres: [16]", "countries: [12]", "tv.rules[0].match.countries[0]"},
		{"invalid code", "genres: [16]", "countries: [J1]", "tv.rules[0].match.countries[0]"},
		{"unicode code", "genres: [16]", "countries: [日本]", "tv.rules[0].match.countries[0]"},
		{"nested code list", "genres: [16]", "languages: [[ja]]", "tv.rules[0].match.languages[0]"},
		{"anchored scalar", "name: 动漫", "name: &name 动漫", "tv.rules[0].name"},
		{"anchored list", "genres: [16]", "genres: &genres [16]", "tv.rules[0].match.genres"},
		{"custom scalar tag", "name: 动漫", "name: !rule 动漫", "tv.rules[0].name"},
		{"custom mapping tag", "match: {genres: [16]}", "match: !conditions {genres: [16]}", "tv.rules[0].match"},
		{"custom sequence tag", "genres: [16]", "genres: !genres [16]", "tv.rules[0].match.genres"},
		{"merge key", "match: {genres: [16]}", "match: {<<: {genres: [16]}}", "tv.rules[0].match"},
		{"non-text key", "genres: [16]", "16: [16]", "tv.rules[0].match"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(simpleYAML, tt.old) {
				t.Fatal("invalid fixture replacement", tt.old)
			}
			source := strings.Replace(simpleYAML, tt.old, tt.replacement, 1)
			classifier, err := mediacategory.Compile(source)
			if classifier != nil || !errors.Is(err, mediacategory.ErrConfig) {
				t.Fatalf("invalid YAML was accepted: %v, %v", classifier, err)
			}
			var location *mediacategory.ConfigError
			if !errors.As(err, &location) || location.Field != tt.field || location.Line < 1 || location.Column < 1 || location.Message == "" {
				t.Fatalf("missing field diagnostics for %s: %+v, %v", tt.field, location, err)
			}
		})
	}
}

func TestRejectInvalidDocuments(t *testing.T) {
	for _, source := range []string{
		"", "# comments only", "null", "[]", "[unterminated", "movie: [1",
		"movie: {}\n\x00", "movie: {}\n\xff",
		"&config\n" + simpleYAML,
		simpleYAML + "---\n" + simpleYAML,
		simpleYAML + "---\n",
		simpleYAML + "---\n[invalid",
		"movie: {fallback: &path 电影/其他电影, rules: []}\ntv: {fallback: *path, rules: []}",
		strings.Replace(simpleYAML, "    - name: 动漫", "    - null\n    - name: 动漫", 1),
		simpleYAML + "    - name: ' 动漫 '\n      match: {genres: [99]}\n",
		strings.Repeat(" ", mediacategory.MaxYAMLBytes+1),
	} {
		classifier, err := mediacategory.Compile(source)
		if classifier != nil || !errors.Is(err, mediacategory.ErrConfig) {
			t.Fatalf("invalid document was accepted (%d bytes): %v, %v", len(source), classifier, err)
		}
	}
}

func TestRejectUnsafeCategoryDirectories(t *testing.T) {
	for _, path := range []string{"", " ", "/绝对路径", "C:/media", `C:\media`, `\\server\media`, `电影\动画`, ".", "..", "电影/../其他", "./电影", "电影//动画", "电影/", "电影/ 动画", "电影/动画.", "电影/CON", "电影/LPT¹", "电影/a:b", "电影/a?b", "电影/a\nb", strings.Repeat("影", 81)} {
		for _, target := range []string{"name: 动漫", "fallback: 其他电影"} {
			key, _, _ := strings.Cut(target, ":")
			source := strings.Replace(simpleYAML, target, key+": "+strconv.Quote(path), 1)
			classifier, err := mediacategory.Compile(source)
			if classifier != nil || !errors.Is(err, mediacategory.ErrConfig) {
				t.Fatalf("unsafe %s %q accepted: %v, %v", key, path, classifier, err)
			}
		}
	}
}

func TestCommentsAndExplicitDocuments(t *testing.T) {
	source := "# global comment\n---\n" + strings.Replace(simpleYAML, "name: 动漫", "name: 动漫 # rule comment", 1) + "...\n"
	result, err := compile(t, source).Classify(mediacategory.Facts{Kind: "tv", Genres: []int{16}})
	if err != nil || result.Path != "剧集/动漫" {
		t.Fatal("comments or document markers affected classification", result, err)
	}
}
