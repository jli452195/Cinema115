package mediacategory_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"strm115/backend/internal/mediacategory"
)

func compile(t testing.TB, source string) *mediacategory.Classifier {
	t.Helper()
	classifier, err := mediacategory.Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	return classifier
}

func TestDefaultCategories(t *testing.T) {
	classifier := compile(t, mediacategory.DefaultYAML())
	for _, tt := range []struct {
		name       string
		facts      mediacategory.Facts
		path, rule string
	}{
		{"animation movie before language", mediacategory.Facts{Kind: "movie", Genres: []int{16}, OriginalLanguage: "zh"}, "电影/动画电影", "动画电影"},
		{"Chinese movie", mediacategory.Facts{Kind: "movie", OriginalLanguage: "zh"}, "电影/华语电影", "华语电影"},
		{"Cantonese movie", mediacategory.Facts{Kind: "movie", OriginalLanguage: "cn"}, "电影/华语电影", "华语电影"},
		{"foreign movie", mediacategory.Facts{Kind: "movie", OriginalLanguage: "en"}, "电影/其他电影", ""},
		{"missing movie facts", mediacategory.Facts{Kind: "movie"}, "电影/其他电影", ""},
		{"animation before documentary", mediacategory.Facts{Kind: "tv", Genres: []int{99, 16}, Countries: []string{"JP"}}, "剧集/动漫", "动漫"},
		{"documentary before region", mediacategory.Facts{Kind: "tv", Genres: []int{99}, Countries: []string{"CN"}}, "剧集/纪录片", "纪录片"},
		{"reality", mediacategory.Facts{Kind: "tv", Genres: []int{10764}}, "剧集/综艺", "综艺"},
		{"talk show", mediacategory.Facts{Kind: "tv", Genres: []int{10767}}, "剧集/综艺", "综艺"},
		{"Chinese series", mediacategory.Facts{Kind: "tv", Countries: []string{"TW"}}, "剧集/国产剧", "国产剧"},
		{"Japanese series", mediacategory.Facts{Kind: "tv", Countries: []string{"JP"}}, "剧集/日韩剧", "日韩剧"},
		{"Korean series", mediacategory.Facts{Kind: "tv", Countries: []string{"KR"}}, "剧集/日韩剧", "日韩剧"},
		{"Western series", mediacategory.Facts{Kind: "tv", Countries: []string{"DE"}}, "剧集/欧美剧", "欧美剧"},
		{"joint production", mediacategory.Facts{Kind: "tv", Countries: []string{"JP", "CN"}}, "剧集/国产剧", "国产剧"},
		{"reversed countries", mediacategory.Facts{Kind: "tv", Countries: []string{"CN", "JP"}}, "剧集/国产剧", "国产剧"},
		{"region priority", mediacategory.Facts{Kind: "tv", Countries: []string{"US", "KR"}}, "剧集/日韩剧", "日韩剧"},
		{"other region", mediacategory.Facts{Kind: "tv", Countries: []string{"TH"}}, "剧集/其他剧集", ""},
		{"unknown facts", mediacategory.Facts{Kind: "tv"}, "剧集/其他剧集", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(tt.facts)
			want := mediacategory.Result{Path: tt.path, RuleName: tt.rule, Fallback: tt.rule == ""}
			if err != nil || result != want {
				t.Fatalf("got %+v, %v; want %+v", result, err, want)
			}
		})
	}
}

const customYAML = `movie: {fallback: 其他电影, rules: []}
tv:
  fallback: 其他剧集
  rules:
    - name: 完整条件
      match:
        genres: [16, 18]
        countries: [JP, KR]
        languages: [ja, ko]
    - name: 其他动漫
      match:
        genres: [16]
`

func TestConditionsAndMissingFacts(t *testing.T) {
	classifier := compile(t, customYAML)
	for _, tt := range []struct {
		name  string
		facts mediacategory.Facts
		want  string
	}{
		{"every condition", mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{"JP"}, OriginalLanguage: "ja"}, "完整条件"},
		{"OR within lists", mediacategory.Facts{Kind: "tv", Genres: []int{99, 18}, Countries: []string{"CN", "KR"}, OriginalLanguage: "ko"}, "完整条件"},
		{"normalize incoming facts", mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{" jp "}, OriginalLanguage: " JA "}, "完整条件"},
		{"country mismatch", mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{"CN"}, OriginalLanguage: "ja"}, "其他动漫"},
		{"missing country", mediacategory.Facts{Kind: "tv", Genres: []int{16}, OriginalLanguage: "ja"}, "其他动漫"},
		{"language mismatch", mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{"JP"}, OriginalLanguage: "en"}, "其他动漫"},
		{"missing language", mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{"JP"}}, "其他动漫"},
		{"invalid facts are not codes", mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{"Japan", "日本"}, OriginalLanguage: "ja"}, "其他动漫"},
		{"genre mismatch", mediacategory.Facts{Kind: "tv", Genres: []int{99}, Countries: []string{"JP"}, OriginalLanguage: "ja"}, ""},
		{"missing genres", mediacategory.Facts{Kind: "tv", Countries: []string{"JP"}, OriginalLanguage: "ja"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(tt.facts)
			if err != nil || result.RuleName != tt.want || result.Fallback != (tt.want == "") {
				t.Fatalf("got %+v, %v; want rule %q", result, err, tt.want)
			}
		})
	}
}

func TestRuleOrderAndNormalization(t *testing.T) {
	narrow := "    - name: 日本动漫\n      match: {genres: [16], countries: [' jp ']}\n"
	broad := "    - name: 动漫\n      match: {genres: [16]}\n"
	header := "movie: {fallback: 其他电影, rules: []}\ntv:\n  fallback: 其他剧集\n  rules:\n"
	facts := mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{"JP"}}
	for _, tt := range []struct{ rules, want string }{{narrow + broad, "日本动漫"}, {broad + narrow, "动漫"}} {
		result, err := compile(t, header+tt.rules).Classify(facts)
		if err != nil || result.RuleName != tt.want {
			t.Fatalf("rule order was ignored: %+v, %v", result, err)
		}
	}
	source := strings.ReplaceAll(customYAML, "[JP, KR]", "[' jp ', kr]")
	source = strings.ReplaceAll(source, "[ja, ko]", "[' JA ', KO]")
	source = strings.ReplaceAll(source, "name: 完整条件", "name: ' 完整条件 '")
	facts.OriginalLanguage = "ja"
	result, err := compile(t, source).Classify(facts)
	if err != nil || result.RuleName != "完整条件" || result.Path != "剧集/完整条件" {
		t.Fatalf("configuration codes or names were not normalized: %+v, %v", result, err)
	}
}

func TestFallbacksAndInvalidKinds(t *testing.T) {
	classifier := compile(t, "movie: {fallback: 全部, rules: []}\ntv: {fallback: 全部, rules: []}\n")
	for kind, want := range map[string]string{"movie": "电影/全部", "tv": "剧集/全部"} {
		result, err := classifier.Classify(mediacategory.Facts{Kind: kind, Genres: []int{16}})
		if err != nil || !result.Fallback || result.RuleName != "" || result.Path != want {
			t.Fatalf("empty rule list must use fallback: %+v, %v", result, err)
		}
	}
	for _, kind := range []string{"", "person", "TV", " movie "} {
		if _, err := classifier.Classify(mediacategory.Facts{Kind: kind}); !errors.Is(err, mediacategory.ErrFacts) {
			t.Fatalf("unsupported kind %q: %v", kind, err)
		}
	}
	for _, uninitialized := range []*mediacategory.Classifier{nil, {}} {
		if _, err := uninitialized.Classify(mediacategory.Facts{Kind: "tv"}); !errors.Is(err, mediacategory.ErrConfig) {
			t.Fatal("uninitialized classifier accepted facts", err)
		}
	}
}

func TestClassifierDoesNotMutateFactsAndCanBeShared(t *testing.T) {
	classifier := compile(t, customYAML)
	facts := mediacategory.Facts{Kind: "tv", Genres: []int{99, 16}, Countries: []string{" jp "}, OriginalLanguage: " JA "}
	want := mediacategory.Facts{Kind: "tv", Genres: []int{99, 16}, Countries: []string{" jp "}, OriginalLanguage: " JA "}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				result, err := classifier.Classify(facts)
				if err != nil || result.RuleName != "完整条件" {
					t.Errorf("shared classifier changed: %+v, %v", result, err)
					return
				}
			}
		}()
	}
	workers.Wait()
	if !reflect.DeepEqual(facts, want) {
		t.Fatal("classification mutated caller-owned facts", facts)
	}
}

func BenchmarkClassify(b *testing.B) {
	classifier := compile(b, mediacategory.DefaultYAML())
	for _, tt := range []struct {
		name  string
		facts mediacategory.Facts
	}{
		{"first_rule", mediacategory.Facts{Kind: "tv", Genres: []int{16}, Countries: []string{"JP"}}},
		{"last_rule", mediacategory.Facts{Kind: "tv", Countries: []string{"US"}}},
		{"fallback", mediacategory.Facts{Kind: "tv", Countries: []string{"TH"}}},
	} {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := classifier.Classify(tt.facts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
