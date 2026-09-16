package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

type resourceMatcherHTTPFake struct {
	kind       string
	id         int
	candidates []service.ResourceCandidate
	result     service.ResourceMatchBatch
	err        error
}

func (f *resourceMatcherHTTPFake) Match(_ context.Context, kind string, id int, candidates []service.ResourceCandidate) (service.ResourceMatchBatch, error) {
	f.kind = kind
	f.id = id
	f.candidates = append([]service.ResourceCandidate(nil), candidates...)
	return f.result, f.err
}

func resourceMatchRequest(t *testing.T, router http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestResourceMatchRouteReturnsBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &resourceMatcherHTTPFake{result: service.ResourceMatchBatch{
		Status:  "available",
		Target:  service.MediaIdentity{Kind: "tv", TMDBID: 60843, Title: "请问您今天要来点兔子吗？", Year: "2014"},
		Missing: []service.EpisodeRef{{Season: 2, Episode: 4, Label: "S02E04"}},
		Results: []service.ResourceMatchDecision{{ID: "resource-1", Status: "matched", Matched: true}},
	}}
	router := gin.New()
	RegisterResourceMatch(router.Group("/api/catalog"), fake)
	response := resourceMatchRequest(t, router, "/api/catalog/library/tv/60843/resource-match", `{"resources":[{"id":"resource-1","title":"[日番] 请问您今天要来点兔子吗？ (2014) S01-S03"}]}`)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if fake.kind != "tv" || fake.id != 60843 || len(fake.candidates) != 1 || fake.candidates[0].ID != "resource-1" {
		t.Fatalf("matcher input = kind=%q id=%d candidates=%+v", fake.kind, fake.id, fake.candidates)
	}
	var batch service.ResourceMatchBatch
	if err := json.Unmarshal(response.Body.Bytes(), &batch); err != nil || len(batch.Results) != 1 || !batch.Results[0].Matched {
		t.Fatalf("response=%+v err=%v", batch, err)
	}
}

func TestResourceMatchRouteValidatesInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &resourceMatcherHTTPFake{}
	router := gin.New()
	RegisterResourceMatch(router.Group("/api/catalog"), fake)

	resources := make([]service.ResourceCandidate, 101)
	for i := range resources {
		resources[i] = service.ResourceCandidate{ID: "item", Title: "资源 (2020)"}
	}
	tooMany, err := json.Marshal(map[string]any{"resources": resources})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/catalog/library/person/1/resource-match", `{"resources":[{"title":"资源"}]}`},
		{"/api/catalog/library/tv/0/resource-match", `{"resources":[{"title":"资源"}]}`},
		{"/api/catalog/library/tv/not-a-number/resource-match", `{"resources":[{"title":"资源"}]}`},
		{"/api/catalog/library/tv/1/resource-match", `{"resources":[]}`},
		{"/api/catalog/library/tv/1/resource-match", string(tooMany)},
		{"/api/catalog/library/tv/1/resource-match", `{"resources":[{"title":""}]}`},
		{"/api/catalog/library/tv/1/resource-match", `{"resources":[{"title":"` + strings.Repeat("长", 1001) + `"}]}`},
	} {
		response := resourceMatchRequest(t, router, tc.path, tc.body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", tc.path, response.Code, response.Body.String())
		}
	}
	if fake.kind != "" || fake.id != 0 || len(fake.candidates) != 0 {
		t.Fatalf("invalid request called matcher: %+v", fake)
	}
}

func TestResourceMatchRouteKeepsUnavailableStructured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &resourceMatcherHTTPFake{
		result: service.ResourceMatchBatch{
			Status:  "unavailable",
			Target:  service.MediaIdentity{Kind: "tv", TMDBID: 9},
			Missing: []service.EpisodeRef{},
			Results: []service.ResourceMatchDecision{{ID: "resource-1", Status: "unavailable", Reason: "缺集信息暂时不可用"}},
		},
		err: errors.New("TMDB timeout"),
	}
	router := gin.New()
	RegisterResourceMatch(router.Group("/api/catalog"), fake)
	response := resourceMatchRequest(t, router, "/api/catalog/library/tv/9/resource-match", `{"resources":[{"id":"resource-1","title":"测试剧集 (2020)"}]}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"unavailable"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
