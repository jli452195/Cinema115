package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowseQueriesCacheAndNormalization(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if r.URL.Path != "/discover/tv" || q.Get("api_key") != "private-key" || q.Get("with_genres") != "16" || q.Get("with_origin_country") != "CN" || q.Get("first_air_date_year") != "2024" || q.Get("sort_by") != "vote_average.desc" || q.Get("vote_count.gte") != "100" || q.Get("include_adult") != "false" {
			t.Error("发现筛选或凭证参数错误")
		}
		w.Write([]byte(`{"page":1,"total_pages":1001,"total_results":22,"results":[{"id":42,"name":"测试剧集","original_name":"Original","poster_path":"/poster.jpg","first_air_date":"2024-01-01","vote_average":8.2,"vote_count":123},{"id":43,"name":"过滤条目","adult":true}]}`))
	}))
	defer server.Close()
	c := New("private-key")
	c.base = server.URL
	options := Options{Kind: "tv", Genre: "16", Region: "CN", Year: "2024", Sort: "rating", Page: 1}
	for i := 0; i < 2; i++ {
		page, err := c.Browse(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.TotalPages != 500 || page.Items[0].Title != "测试剧集" || page.Items[0].PosterURL != "https://image.tmdb.org/t/p/w342/poster.jpg" {
			t.Fatal("数据归一化失败")
		}
	}
	if calls != 1 {
		t.Fatal("重复查询没有命中缓存")
	}
}

func TestMovieSearchAndValidation(t *testing.T) {
	endpoint, q, err := optionsQuery(Options{Kind: "movie", Query: "测试电影", Page: 2})
	if err != nil || endpoint != "/search/movie" || q.Get("query") != "测试电影" || q.Get("page") != "2" || q.Get("sort_by") != "" {
		t.Fatal("搜索参数错误")
	}
	_, q, err = optionsQuery(Options{Kind: "movie", Year: "2020", Sort: "latest", Page: 1})
	if err != nil || q.Get("primary_release_year") != "2020" || q.Get("sort_by") != "primary_release_date.desc" {
		t.Fatal("电影日期筛选错误")
	}
	for _, bad := range []Options{{Kind: "../movie", Page: 1}, {Kind: "tv", Page: 501}, {Kind: "tv", Year: "1800", Page: 1}, {Kind: "tv", Region: "../", Page: 1}, {Kind: "tv", Genre: "16|99", Page: 1}, {Kind: "tv", Query: "片名", Genre: "16", Page: 1}, {Kind: "movie", Sort: "douban", Page: 1}} {
		if _, _, err := optionsQuery(bad); !errors.Is(err, ErrInput) {
			t.Fatal("非法或不能生效的筛选未拦截")
		}
	}
	for _, path := range []string{"https://private/image.jpg", "//other-host/image.jpg", "/../private", "/image.jpg?api_key=private"} {
		if posterURL(path) != "" {
			t.Fatal("非法海报路径未拦截")
		}
	}
}

func TestTMDBErrorsDoNotExposeKey(t *testing.T) {
	for _, status := range []int{401, 429, 502} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte("upstream-private-key"))
		}))
		c := New("upstream-private-key")
		c.base = server.URL
		_, err := c.Genres(context.Background(), "tv")
		server.Close()
		if err == nil || strings.Contains(err.Error(), "upstream-private-key") {
			t.Fatal("错误信息泄漏密钥或上游原始内容")
		}
	}
	if _, err := New("").Genres(context.Background(), "tv"); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("缺少密钥提示不正确")
	}
	c := New("private-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Genres(ctx, "movie"); !errors.Is(err, context.Canceled) {
		t.Fatal("取消未传递")
	}
}
