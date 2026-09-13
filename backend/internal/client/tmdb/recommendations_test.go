package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecommendationsFillDeduplicateAndCache(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !strings.HasPrefix(r.URL.Path, "/"+kind+"/42/") || r.URL.Query().Get("language") != "zh-CN" || r.URL.Query().Get("api_key") != "test-key" {
					t.Error("推荐类型、语言或认证不正确")
				}
				if strings.HasSuffix(r.URL.Path, "recommendations") {
					fmt.Fprint(w, `{"page":1,"results":[{"id":42},{"id":7,"title":"电影","name":"剧集","poster_path":"/a.jpg"},{"id":7},{"id":8,"adult":true}]}`)
				} else {
					fmt.Fprint(w, `{"page":1,"results":[{"id":7},{"id":9,"original_title":"Movie","original_name":"TV","poster_path":"https://bad/image"}]}`)
				}
			}))
			defer server.Close()
			client := New("test-key")
			client.base = server.URL
			for i := 0; i < 2; i++ {
				page, err := client.Recommendations(context.Background(), kind, 42)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 2 || page.Items[0].ID != 7 || page.Items[1].ID != 9 || page.Items[1].PosterURL != "" || page.Items[0].Kind != kind || page.Total != 2 {
					t.Fatal("推荐去重、补充或归一化错误", page)
				}
				want := "电影"
				if kind == "tv" {
					want = "剧集"
				}
				if page.Items[0].Title != want {
					t.Fatal("电影剧集标题映射错误")
				}
			}
			if calls != 2 {
				t.Fatal("没有复用已有缓存", calls)
			}
		})
	}
}

func TestRecommendationsCapAvoidsExtraRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "similar") {
			t.Error("推荐充足仍请求相似接口")
		}
		fmt.Fprint(w, `{"page":1,"results":[`)
		for i := 1; i <= 25; i++ {
			if i > 1 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"title":"电影"}`, i)
		}
		fmt.Fprint(w, ` ]}`)
	}))
	defer server.Close()
	client := New("test")
	client.base = server.URL
	page, err := client.Recommendations(context.Background(), "movie", 100)
	if err != nil || len(page.Items) != 20 || calls != 1 {
		t.Fatal("推荐数量限制失败", err, calls)
	}
}

func TestRecommendationsErrorsAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, recommendations string
		similarStatus         int
		wantCount             int
		wantError             bool
	}{
		{"空列表", `{"page":1,"results":[]}`, 200, 0, false},
		{"保留已有推荐", `{"page":1,"results":[{"id":8}]}`, 502, 1, false},
		{"完全失败", `{"page":1,"results":[]}`, 502, 0, true},
		{"无效上游", `{"page":1}`, 200, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "recommendations") {
					fmt.Fprint(w, tc.recommendations)
					return
				}
				w.WriteHeader(tc.similarStatus)
				fmt.Fprint(w, `{"page":1,"results":[]}`)
			}))
			defer server.Close()
			client := New("secret")
			client.base = server.URL
			page, err := client.Recommendations(context.Background(), "tv", 42)
			if (err != nil) != tc.wantError {
				t.Fatal("异常处理错误", err)
			}
			if err == nil && (page.Items == nil || len(page.Items) != tc.wantCount) {
				t.Fatal("空列表或部分推荐错误")
			}
		})
	}
	for _, tc := range []struct {
		kind string
		id   int
	}{{"person", 42}, {"movie", 0}, {"tv", -1}} {
		if _, err := New("").Recommendations(context.Background(), tc.kind, tc.id); !errors.Is(err, ErrInput) {
			t.Fatal("非法参数未提前拒绝")
		}
	}
}
