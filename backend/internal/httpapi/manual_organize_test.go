package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestManualOrganizeAPIRequiresExplicitAccount(t *testing.T) {
	router, accounts, _ := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "organize.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	organizer, err := service.NewManualOrganize(store, accounts, p115.NewShareClient(), nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterManualOrganize(router.Group("/api/admin"), organizer)
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/config", "", http.StatusOK},
		{"GET", "/jobs", "", http.StatusOK},
		{"GET", "/files?cid=0", "", http.StatusBadRequest},
		{"PUT", "/config", `{"sourceCID":"0","targetCID":"0","sourcePath":"/","targetPath":"/"}`, http.StatusBadRequest},
		{"POST", "/scan", `{"parentId":"0","entryId":"123"}`, http.StatusBadRequest},
		{"POST", "/jobs", `{"previewId":"forged"}`, http.StatusBadRequest},
		{"POST", "/preview", `{"files":[]}`, http.StatusBadRequest},
		{"POST", "/scan", `{`, http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, "/api/admin/p115/manual-organize"+tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		if response.Code != tc.code {
			t.Fatalf("%s %s: status=%d body=%s", tc.method, tc.path, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("整理响应不能缓存")
		}
		if strings.Contains(response.Body.String(), "cookie") {
			t.Fatal("响应不得包含凭据字段")
		}
	}
}
