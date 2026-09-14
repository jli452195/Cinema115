package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCollectionDetails(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/collection/7" || r.URL.Query().Get("language") != "zh-CN" {
			t.Error("系列路径或语言错误")
		}
		_, _ = w.Write([]byte(`{"id":7,"name":"测试系列","poster_path":"/poster.jpg","backdrop_path":"/../bad","parts":[{"id":3,"title":"日期待定"},{"id":2,"title":"第二部","release_date":"2025-01-01"},{"id":1,"original_title":"第一部","release_date":"2020-01-01","poster_path":"/movie.jpg"},{"id":1,"title":"重复"},{"id":4,"adult":true},{"id":0}]}`))
	}))
	defer server.Close()
	c := New("test-key")
	c.base = server.URL
	for i := 0; i < 2; i++ {
		data, err := c.CollectionDetails(context.Background(), 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(data.Items) != 3 || data.Items[0].ID != 1 || data.Items[1].ID != 2 || data.Items[2].ID != 3 {
			t.Fatal("过滤、去重或上映顺序错误")
		}
		if data.Items[0].Kind != "movie" || data.Items[0].Title != "第一部" || data.BackdropURL != "" || data.PosterURL != "https://image.tmdb.org/t/p/w500/poster.jpg" {
			t.Fatal("字段归一化或图片校验错误")
		}
	}
	if calls != 1 {
		t.Fatal("系列缓存未命中")
	}
	if _, err := c.CollectionDetails(context.Background(), 0); !errors.Is(err, ErrInput) {
		t.Fatal("未校验非法系列 ID")
	}
}

func TestCollectionErrors(t *testing.T) {
	for _, tc := range []struct {
		status   int
		body     string
		notFound bool
		valid    bool
	}{{404, `{}`, true, false}, {200, `{"id":7}`, false, false}, {200, `{"id":8,"parts":[]}`, false, false}, {200, `{"id":7,"parts":[]}`, false, true}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := New("test-key")
		c.base = server.URL
		data, err := c.CollectionDetails(context.Background(), 7)
		server.Close()
		if tc.valid {
			if err != nil || data.Items == nil {
				t.Fatal("空系列应返回空列表")
			}
			continue
		}
		if err == nil || (tc.notFound && !errors.Is(err, ErrNotFound)) {
			t.Fatal("系列错误未正确处理")
		}
	}
}
