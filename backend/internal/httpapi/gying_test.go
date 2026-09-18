package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/gying"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/service"
)

type gyingSearcherFake struct {
	input         service.GyingSearchInput
	resourceOrder []string
}

func (f *gyingSearcherFake) View() service.GyingView {
	return service.GyingView{BaseURL: "https://example.com", Username: "saved-user", Configured: true, HasSession: true, ResourceOrder: f.resourceOrder}
}
func (f *gyingSearcherFake) Configure(_ context.Context, input service.GyingConfigInput) (service.GyingView, error) {
	if input.Password == "bad" {
		return service.GyingView{}, gying.ErrLogin
	}
	return f.View(), nil
}
func (f *gyingSearcherFake) UpdateResourceOrder(input []string) (service.GyingView, error) {
	for _, resourceType := range input {
		if resourceType == "invalid" {
			return service.GyingView{}, gying.ErrInput
		}
	}
	f.resourceOrder = append([]string(nil), input...)
	return f.View(), nil
}
func (f *gyingSearcherFake) Clear() error { return nil }
func (f *gyingSearcherFake) Search(_ context.Context, input service.GyingSearchInput) (service.GyingSearchResult, error) {
	f.input = input
	return service.GyingSearchResult{SearchResponse: gying.SearchResponse{Query: input.OriginalTitle, MatchedBy: "title", Items: []gying.SearchItem{}}}, nil
}

type gyingTMDBFake struct{ tmdbReaderFake }

func (f *gyingTMDBFake) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, OriginalTitle: "Original Name"}, IMDBID: "tt1234567"}, nil
}

func TestGyingRoutesUseServerTMDBMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	searcher := &gyingSearcherFake{}
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "admin-test" {
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	})
	RegisterGying(admin, searcher, &gyingTMDBFake{})

	request := httptest.NewRequest(http.MethodPost, "/api/admin/gying/search", strings.NewReader(`{"kind":"tv","tmdbId":"42","originalTitle":"forged","imdbId":"tt0","refresh":true}`))
	request.Header.Set("Authorization", "admin-test")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if searcher.input.OriginalTitle != "Original Name" || searcher.input.IMDBID != "tt1234567" {
		t.Fatalf("搜索使用了前端伪造的元数据: original=%q imdb=%q", searcher.input.OriginalTitle, searcher.input.IMDBID)
	}
	if searcher.input.CacheKey != "tv:42" || !searcher.input.Refresh {
		t.Fatalf("搜索缓存参数不正确: %+v", searcher.input)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("观影接口允许浏览器缓存")
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/admin/gying/config", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("非管理员访问状态码=%d", response.Code)
	}
}

func TestGyingConfigDoesNotReturnSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterGying(router.Group("/api/admin"), &gyingSearcherFake{}, &gyingTMDBFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/gying/config", strings.NewReader(`{"baseUrl":"https://example.com","username":"saved-user","password":"secret-password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret-password") || strings.Contains(strings.ToLower(response.Body.String()), "cookie") {
		t.Fatalf("配置响应泄漏凭据: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGyingResourceOrderRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	searcher := &gyingSearcherFake{}
	RegisterGying(router.Group("/api/admin"), searcher, &gyingTMDBFake{})

	request := httptest.NewRequest(http.MethodPut, "/api/admin/gying/resource-order", strings.NewReader(`{"types":["115","magnet","quark"]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Join(searcher.resourceOrder, ",") != "115,magnet,quark" {
		t.Fatalf("资源排序未保存: status=%d body=%s order=%v", response.Code, response.Body.String(), searcher.resourceOrder)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/admin/gying/resource-order", strings.NewReader(`{"types":["invalid"]}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("非法资源排序状态码=%d body=%s", response.Code, response.Body.String())
	}
}
