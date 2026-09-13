package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetailsMovieTVAndSeason(t *testing.T) {
	calls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.Method != "GET" || r.URL.Query().Get("language") != "zh-CN" || r.URL.Query().Get("api_key") != "test-key" {
			t.Error("详情请求方法、语言或凭证错误")
		}
		switch r.URL.Path {
		case "/movie/42":
			if r.URL.Query().Get("append_to_response") != "credits,external_ids" {
				t.Error("未附加电影演职员")
			}
			w.Write([]byte(`{"id":42,"title":"电影","original_title":"Movie","release_date":"2024-01-02","runtime":123,"poster_path":"/poster.jpg","backdrop_path":"/back.jpg","belongs_to_collection":{"id":7,"name":"系列"},"credits":{"cast":[{"id":1,"name":"演员","character":"角色","profile_path":"/actor.jpg"}]},"external_ids":{"imdb_id":"tt1234567"}}`))
		case "/tv/42":
			if r.URL.Query().Get("append_to_response") != "aggregate_credits,external_ids" {
				t.Error("未附加全季演员")
			}
			w.Write([]byte(`{"id":42,"name":"剧集","original_name":"TV","first_air_date":"2020-03-04","episode_run_time":[45],"number_of_episodes":10,"genres":[{"id":18,"name":"剧情"}],"production_countries":[{"name":"China"}],"seasons":[{"id":2,"season_number":0,"name":"特别篇","episode_count":1},{"id":3,"season_number":1,"name":"第1季","episode_count":9}],"aggregate_credits":{"cast":[{"id":8,"name":"主演","roles":[{"character":"主角"}],"profile_path":"https://untrusted/image"}]},"external_ids":{"imdb_id":"tt12/evil"}}`))
		case "/tv/42/season/0":
			w.Write([]byte(`{"id":2,"season_number":0,"name":"特别篇","episodes":[{"id":11,"episode_number":1,"name":"幕后","runtime":30,"vote_average":8.5,"vote_count":10,"still_path":"/still.jpg"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := New("test-key")
	c.base = server.URL
	for i := 0; i < 2; i++ {
		movie, err := c.Details(context.Background(), "movie", 42)
		if err != nil {
			t.Fatal(err)
		}
		if movie.Title != "电影" || movie.Date != "2024-01-02" || movie.Runtime != 123 || movie.Collection.ID != 7 || movie.Cast[0].Character != "角色" || movie.IMDBURL != "https://www.imdb.com/title/tt1234567/" || !strings.Contains(movie.BackdropURL, "/w1280/") || movie.Seasons == nil {
			t.Fatal("电影详情归一化错误")
		}
	}
	if calls["/movie/42"] != 1 {
		t.Fatal("详情缓存未生效")
	}
	tv, err := c.Details(context.Background(), "tv", 42)
	if err != nil {
		t.Fatal(err)
	}
	if tv.Title != "剧集" || tv.Runtime != 45 || tv.Date != "2020-03-04" || tv.Cast[0].Character != "主角" || tv.Cast[0].ProfileURL != "" || tv.IMDBURL != "" || len(tv.Seasons) != 2 || tv.Seasons[0].Number != 0 || tv.EpisodeCount != 10 {
		t.Fatal("剧集详情或特殊季处理错误")
	}
	if calls["/tv/42/season/0"] != 0 {
		t.Fatal("详情不应提前加载分集")
	}
	season, err := c.Season(context.Background(), 42, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(season.Episodes) != 1 || season.Episodes[0].Name != "幕后" || !strings.Contains(season.Episodes[0].StillURL, "/w300/") {
		t.Fatal("分集信息错误")
	}
	if _, err := c.Details(context.Background(), "movie", 404); !errors.Is(err, ErrNotFound) {
		t.Fatal("不存在的条目应返回可识别的错误")
	}
}

func TestDetailInputAndMalformedResponse(t *testing.T) {
	c := New("test-key")
	for _, kind := range []string{"person", "../movie"} {
		if _, err := c.Details(context.Background(), kind, 1); !errors.Is(err, ErrInput) {
			t.Fatal("错误类型未拦截")
		}
	}
	if _, err := c.Details(context.Background(), "movie", 0); !errors.Is(err, ErrInput) {
		t.Fatal("错误 ID 未拦截")
	}
	for _, number := range []int{-1, 10001} {
		if _, err := c.Season(context.Background(), 1, number); !errors.Is(err, ErrInput) {
			t.Fatal("错误季号未拦截")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"id":2,"season_number":2}`)) }))
	defer server.Close()
	c.base = server.URL
	if _, err := c.Details(context.Background(), "movie", 1); err == nil {
		t.Fatal("详情 ID 不匹配未拦截")
	}
	if _, err := c.Season(context.Background(), 1, 0); err == nil {
		t.Fatal("季数据缺失未拦截")
	}
}
