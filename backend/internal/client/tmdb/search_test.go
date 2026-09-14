package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAggregateSearchMediaTypes(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/search/multi" || r.URL.Query().Get("query") != "测试" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("include_adult") != "false" {
			t.Error("聚合搜索请求参数错误")
		}
		_, _ = w.Write([]byte(`{"page":2,"total_pages":3,"total_results":45,"results":[{"id":7,"media_type":"movie","title":"电影","name":"错误标题","release_date":"2020-01-01"},{"id":7,"media_type":"tv","name":"剧集","title":"错误标题","first_air_date":"2021-01-01"},{"id":8,"media_type":"person","name":"人物"},{"id":9,"media_type":"movie","title":"过滤","adult":true},{"id":10,"name":"未知类型"}]}`))
	}))
	defer server.Close()
	c := New("test-key")
	c.base = server.URL
	for i := 0; i < 2; i++ {
		result, err := c.Browse(context.Background(), Options{Kind: "multi", Query: " 测试 ", Page: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 2 || result.Page != 2 || result.TotalPages != 3 {
			t.Fatal("过滤或分页错误")
		}
		if result.Items[0].Kind != "movie" || result.Items[0].Title != "电影" || result.Items[0].Date != "2020-01-01" || result.Items[1].Kind != "tv" || result.Items[1].Title != "剧集" || result.Items[1].Date != "2021-01-01" {
			t.Fatal("电影剧集类型或字段错误")
		}
	}
	if calls != 1 {
		t.Fatal("聚合搜索没有复用缓存")
	}
	for _, options := range []Options{{Kind: "multi", Page: 1}, {Kind: "multi", Query: " ", Page: 1}, {Kind: "multi", Query: "测试", Sort: "rating", Page: 1}, {Kind: "multi", Query: "测试", Page: 501}} {
		if _, err := c.Browse(context.Background(), options); !errors.Is(err, ErrInput) {
			t.Fatal("聚合搜索参数校验错误")
		}
	}
	if _, err := c.Genres(context.Background(), "multi"); !errors.Is(err, ErrInput) {
		t.Fatal("聚合类型不应影响原类型校验")
	}
}
