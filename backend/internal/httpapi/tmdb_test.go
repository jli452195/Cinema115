package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
)

type tmdbReaderFake struct{ calls int }

func (f *tmdbReaderFake) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	f.calls++
	if id == 404 {
		return nil, tmdb.ErrNotFound
	}
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind}}, nil
}

func (f *tmdbReaderFake) Season(_ context.Context, id, number int) (*tmdb.SeasonDetail, error) {
	f.calls++
	return &tmdb.SeasonDetail{ID: id, Number: number, Episodes: []tmdb.Episode{}}, nil
}

func (f *tmdbReaderFake) Genres(context.Context, string) ([]tmdb.Genre, error) {
	f.calls++
	return []tmdb.Genre{{ID: 16, Name: "动画"}}, nil
}

func TestTMDBDetailRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	client := &tmdbReaderFake{}
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "admin-test" {
			c.AbortWithStatus(401)
		}
	})
	RegisterTMDB(admin, client)
	for _, tc := range []struct {
		path, auth string
		status     int
	}{
		{"detail/movie/42", "", 401}, {"tv/42/season/0", "", 401},
		{"detail/movie/42", "admin-test", 200}, {"detail/tv/42", "admin-test", 200},
		{"detail/tv/404", "admin-test", 404}, {"tv/42/season/0", "admin-test", 200},
		{"detail/person/42", "admin-test", 400}, {"detail/tv/nope", "admin-test", 400},
		{"detail/movie/0", "admin-test", 400}, {"detail/movie/999999999999999999999", "admin-test", 400},
		{"tv/42/season/-1", "admin-test", 400}, {"tv/42/season/x", "admin-test", 400},
		{"tv/42/season/10001", "admin-test", 400}, {"tv/0/season/1", "admin-test", 400},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/tmdb/"+tc.path, nil)
		req.Header.Set("Authorization", tc.auth)
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Errorf("%s: HTTP %d, 预期 %d", tc.path, w.Code, tc.status)
		}
		if tc.auth != "" && w.Header().Get("Cache-Control") != "no-store" {
			t.Error("详情未禁止浏览器缓存")
		}
	}
	if client.calls != 4 {
		t.Fatal("非法或未授权详情请求访问上游")
	}
}
func (f *tmdbReaderFake) Browse(_ context.Context, options tmdb.Options) (*tmdb.Page, error) {
	f.calls++
	if options.Page != 2 || options.Kind != "movie" {
		return nil, tmdb.ErrInput
	}
	return &tmdb.Page{Items: []tmdb.Item{}, Page: 2}, nil
}

func TestTMDBRoutesRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	client := &tmdbReaderFake{}
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "admin-test" {
			c.AbortWithStatus(401)
		}
	})
	RegisterTMDB(admin, client)
	for _, tc := range []struct {
		path, auth string
		status     int
	}{{"browse", "", 401}, {"genres", "", 401}, {"genres?kind=tv", "admin-test", 200}, {"browse?kind=movie&page=2", "admin-test", 200}, {"browse?page=bad", "admin-test", 400}, {"browse?page=3", "admin-test", 400}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/tmdb/"+tc.path, nil)
		req.Header.Set("Authorization", tc.auth)
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatal("鉴权或参数状态不正确", w.Code)
		}
		if tc.auth != "" && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("未禁止浏览器缓存")
		}
	}
	if client.calls != 3 {
		t.Fatal("未授权或无效请求访问上游")
	}
}
