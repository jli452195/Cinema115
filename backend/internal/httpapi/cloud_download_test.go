package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestCloudDownloadRequiresExplicitAccount(t *testing.T) {
	r, accounts, _ := setupAccountsAPI(t, nil, nil)
	settings := testCloudDownloadSettings(t, accounts)
	cloud := p115.NewCloudClient()
	RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: cloud}, cloud, p115.NewShareClient())
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/admin/p115/cloud-download/paths", ""},
		{http.MethodGet, "/api/admin/p115/cloud-download/tasks", ""},
		{http.MethodPost, "/api/admin/p115/cloud-download/tasks", `{"url":"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}`},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestCloudDownloadDirectoryAndTaskUseSelectedAccount(t *testing.T) {
	r, accounts, _ := setupAccountsAPI(t, nil, nil)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.Header.Get("Cookie"), "UID=111_A") {
			t.Fatalf("request did not use selected account cookie")
		}
		switch request.URL.Path {
		case "/files":
			if request.URL.Query().Get("cid") != "0" {
				t.Fatalf("cid=%s", request.URL.Query().Get("cid"))
			}
			_, _ = io.WriteString(w, `{"state":1,"count":"1","data":[{"fid":"0","cid":"123","n":"云下载"}]}`)
		case "/web/":
			_, _ = io.WriteString(w, `{"state":1,"tasks":[{"info_hash":"abc","name":"movie","status":"12","percent":"20"}],"count":1}`)
		default:
			t.Fatalf("unexpected upstream path %s", request.URL.Path)
		}
	}))
	defer upstream.Close()
	cloud := p115.NewCloudClient()
	cloud.HTTP, cloud.BaseURL = upstream.Client(), upstream.URL
	drive := p115.NewShareClient()
	drive.HTTP, drive.WebURL = upstream.Client(), upstream.URL
	settings := testCloudDownloadSettings(t, accounts)
	RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: cloud}, cloud, drive)

	for _, path := range []string{
		"/api/admin/p115/cloud-download/paths?accountId=default&cid=0",
		"/api/admin/p115/cloud-download/tasks?accountId=default",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

func testCloudDownloadSettings(t *testing.T, accounts *service.P115Accounts) *service.CloudDownloadSettings {
	t.Helper()
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cloud-download.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.NewCloudDownloadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	settings.ValidateAccount = accounts.ValidateSelection
	return settings
}
