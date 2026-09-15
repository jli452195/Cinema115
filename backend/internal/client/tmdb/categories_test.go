package tmdb

import (
	"errors"
	"testing"
)

func TestTVCategoryQuery(t *testing.T) {
	for _, tc := range []struct{ category, genre, with, without string }{
		{"", "35", "35", ""},
		{"series", "35", "35", "16,99,10764,10767,10763"},
		{"animation", "", "16", ""},
		{"animation", "16", "16", ""},
		{"animation", "10759", "16,10759", ""},
		{"documentary", "", "99", ""},
		{"variety", "", "10764|10767", ""},
		{"variety", "35", "10764|10767,35", ""},
	} {
		t.Run(tc.category+"/"+tc.genre, func(t *testing.T) {
			endpoint, q, err := optionsQuery(Options{Kind: "tv", Category: tc.category, Genre: tc.genre, Region: "JP", Year: "2024", Sort: "rating", Page: 3})
			if err != nil || endpoint != "/discover/tv" || q.Get("with_genres") != tc.with || q.Get("without_genres") != tc.without {
				t.Fatal(endpoint, q, err)
			}
			if q.Get("page") != "3" || q.Get("first_air_date_year") != "2024" || q.Get("with_origin_country") != "JP" {
				t.Fatal("组合筛选丢失", q)
			}
		})
	}
	for _, o := range []Options{
		{Kind: "movie", Category: "variety", Page: 1},
		{Kind: "movie", Category: "series", Page: 1},
		{Kind: "tv", Category: "film", Page: 1},
		{Kind: "movie", Category: "film", Genre: "99", Page: 1},
		{Kind: "tv", Category: "invalid", Page: 1},
		{Kind: "tv", Category: "variety", Query: "test", Page: 1},
		{Kind: "tv", Category: "series", Genre: "16", Page: 1},
	} {
		if _, _, err := optionsQuery(o); !errors.Is(err, ErrInput) {
			t.Fatal("不合法类别未拒绝", o, err)
		}
	}
	_, q, err := optionsQuery(Options{Kind: "movie", Genre: "16", Page: 1})
	if err != nil || q.Get("with_genres") != "16" || q.Get("without_genres") != "" {
		t.Fatal("影响电影筛选", q, err)
	}
}

func TestMovieCategoryQuery(t *testing.T) {
	for _, tc := range []struct{ category, genre, with, without string }{
		{"film", "35", "35", "16,99"},
		{"animation", "", "16", ""},
		{"animation", "12", "16,12", ""},
		{"documentary", "", "99", ""},
	} {
		endpoint, q, err := optionsQuery(Options{Kind: "movie", Category: tc.category, Genre: tc.genre, Year: "2024", Region: "CN", Page: 2})
		if err != nil || endpoint != "/discover/movie" || q.Get("with_genres") != tc.with || q.Get("without_genres") != tc.without || q.Get("primary_release_year") != "2024" || q.Get("page") != "2" || q.Get("with_origin_country") != "CN" {
			t.Fatal(endpoint, q, err)
		}
	}
}

func TestThailandRegionQuery(t *testing.T) {
	_, q, err := optionsQuery(Options{Kind: "tv", Region: "TH", Page: 1})
	if err != nil || q.Get("with_origin_country") != "TH" {
		t.Fatal("泰国地区筛选参数错误", q, err)
	}
}
