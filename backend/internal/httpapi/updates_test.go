package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

type recentUpdatesReaderFake struct{}

func (recentUpdatesReaderFake) List(_ context.Context, query service.RecentUpdatesQuery) (service.RecentUpdatesPage, error) {
	if query.Date == "invalid" {
		return service.RecentUpdatesPage{}, service.ErrRecentUpdatesInput
	}
	return service.RecentUpdatesPage{Items: []service.RecentUpdateItem{}, Ready: true, Status: "ready", From: "2026-09-17", To: "2026-09-17"}, nil
}

func TestRecentUpdatesRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRecentUpdates(router.Group("/api/catalog"), recentUpdatesReaderFake{})

	request := httptest.NewRequest(http.MethodGet, "/api/catalog/updates?range=today", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatal(response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/catalog/updates?date=invalid", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatal(response.Code, response.Body.String())
	}
}
