package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchPeopleAndCredits(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("language") != "zh-CN" {
			t.Error("缺少中文参数")
		}
		if r.URL.Path == "/search/person" {
			if r.URL.Query().Get("query") != "测试" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("include_adult") != "false" {
				t.Error("演员搜索参数错误")
			}
			_, _ = w.Write([]byte(`{"page":2,"total_pages":900,"results":[{"id":1,"name":"演员","known_for_department":"Acting","profile_path":"/a.jpg","known_for":[{"title":"电影"},{"name":"剧集"},{"title":"过滤","adult":true}]},{"id":2,"known_for_department":"Directing"},{"id":3,"known_for_department":"Acting","adult":true}]}`))
			return
		}
		if r.URL.Path != "/person/1" || r.URL.Query().Get("append_to_response") != "combined_credits" {
			t.Error("演员详情参数错误")
		}
		_, _ = w.Write([]byte(`{"id":1,"name":"演员","biography":"简介","profile_path":"/../bad","combined_credits":{"cast":[{"id":7,"media_type":"movie","title":"电影","release_date":"2020-01-01"},{"id":7,"media_type":"tv","name":"剧集","first_air_date":"2025-01-01"},{"id":7,"media_type":"movie","title":"重复"},{"id":8,"media_type":"movie","adult":true},{"id":9,"media_type":"person"},{"id":10,"media_type":"movie","title":"待定"}]}}`))
	}))
	defer server.Close()
	c := New("test-key")
	c.base = server.URL
	for i := 0; i < 2; i++ {
		page, err := c.SearchPeople(context.Background(), " 测试 ", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || len(page.Items[0].KnownFor) != 2 || page.Items[0].ProfileURL != "https://image.tmdb.org/t/p/w342/a.jpg" || page.TotalPages != 500 {
			t.Fatal("演员过滤、照片或代表作错误")
		}
		detail, err := c.PersonDetails(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Items) != 3 || detail.Items[0].Kind != "tv" || detail.Items[1].Kind != "movie" || detail.Items[2].ID != 10 || detail.ProfileURL != "" {
			t.Fatal("参演作品去重、排序或照片校验错误")
		}
	}
	if calls != 2 {
		t.Fatal("搜索和详情没有命中缓存")
	}
	for _, tc := range []struct {
		q string
		p int
	}{{"", 1}, {"测试", 0}, {"测试", 501}} {
		if _, err := c.SearchPeople(context.Background(), tc.q, tc.p); !errors.Is(err, ErrInput) {
			t.Fatal("非法搜索参数未拦截")
		}
	}
	if _, err := c.PersonDetails(context.Background(), 0); !errors.Is(err, ErrInput) {
		t.Fatal("非法演员 ID 未拦截")
	}
}
