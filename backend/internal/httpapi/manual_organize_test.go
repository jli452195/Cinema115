package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func newHTTPManualOrganize(store interface {
	Value() string
	Save(string) error
}, accounts *service.P115Accounts, drive service.OrganizeDrive, metadata service.OrganizeMetadata) (*service.ManualOrganize, error) {
	engine, err := service.NewOrganizeEngine(store, accounts, drive, metadata)
	if err != nil {
		return nil, err
	}
	return service.NewManualOrganize(engine), nil
}

func TestOrganizeWorkflowHistoryAndResumeIsolation(t *testing.T) {
	router, accounts, accountID := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "organize.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	a, err := accounts.Get(accountID)
	if err != nil {
		t.Fatal(err)
	}
	config := service.OrganizeConfig{AccountID: accountID, SourceCID: "10", SourcePath: "/source", TargetCID: "20", TargetPath: "/manual"}
	manualJob := service.OrganizeJob{ID: "manual", Config: config, Revision: a.Revision, Status: "interrupted", Items: []service.OrganizeItem{{File: service.OrganizeFile{ShareFile: p115.ShareFile{ID: "101", Name: "file.mkv", Size: 1}, ParentID: "10", SourcePath: "/source/file.mkv"}, Directory: "Show", Name: "show.mkv", Status: "pending"}}}
	downloadJob := manualJob
	downloadJob.ID = "download"
	downloadJob.Download = &service.GardenDownload{Hash: strings.Repeat("a", 40), DirectoryID: "10", ParentCID: "0", ParentPath: "/"}
	raw, err := json.Marshal(map[string]any{"version": 1, "config": config, "downloadTarget": service.CloudDownloadConfig{AccountID: accountID, TargetCID: "30", TargetPath: "/download-target"}, "jobs": []service.OrganizeJob{manualJob, downloadJob}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	engine, err := service.NewOrganizeEngine(store, accounts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterManualOrganize(router.Group("/api/admin"), service.NewManualOrganize(engine))
	RegisterGardenOrganize(router.Group("/api/admin"), service.NewGardenOrganize(engine, nil, nil, nil, nil))
	for _, workflow := range []string{"manual-organize", "garden-organize"} {
		response := accountRequest(t, router, "GET", "/"+workflow+"/jobs", "", 200)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("task response must not be cached")
		}
		var data struct {
			Jobs []service.OrganizeJob `json:"jobs"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Jobs) != 1 || (data.Jobs[0].Download != nil) != (workflow == "garden-organize") {
			t.Fatal("workflow history leaked", workflow, data.Jobs)
		}
		if strings.Contains(response.Body.String(), a.Cookie) {
			t.Fatal("history exposed credentials")
		}
	}
	response := accountRequest(t, router, "GET", "/garden-organize/config", "", 200)
	if !strings.Contains(response.Body.String(), "download-target") || strings.Contains(response.Body.String(), "sourceCID") {
		t.Fatal("download target depends on manual configuration")
	}
	accountRequest(t, router, "POST", "/manual-organize/resume", `{"jobId":"download"}`, 400)
	accountRequest(t, router, "POST", "/garden-organize/resume", `{"jobId":"manual"}`, 400)
	accountRequest(t, router, "POST", "/manual-organize/resume", `{}`, 400)
	accountRequest(t, router, "POST", "/garden-organize/resume", `{}`, 400)
}

func TestManualOrganizeCleanupAPIConfirmationAndReplay(t *testing.T) {
	router, accounts, accountID := setupAccountsAPI(t, nil, nil)
	a, err := accounts.Get(accountID)
	if err != nil {
		t.Fatal(err)
	}
	deleted, writes := false, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != a.Cookie {
			t.Error("wrong account snapshot")
		}
		switch r.URL.Path {
		case "/files":
			if r.URL.Query().Get("cid") != "10" {
				t.Error("wrong source directory")
			}
			files := []map[string]any{}
			if !deleted {
				files = append(files, map[string]any{"fid": "101", "cid": "10", "n": "readme.txt", "s": 1})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"state": true, "cid": "10", "count": len(files), "data": files})
		case "/rb/delete":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if r.Method != "POST" || r.PostForm.Get("fid") != "101" || len(r.PostForm) != 1 {
				t.Errorf("unexpected delete request %s %v", r.Method, r.PostForm)
			}
			deleted, writes = true, writes+1
			_, _ = w.Write([]byte(`{"state":true}`))
		default:
			t.Errorf("unexpected upstream operation %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer upstream.Close()
	client := p115.NewShareClient()
	client.WebURL, client.RequestInterval, client.ReadRetryDelays = upstream.URL, 0, nil
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "organize.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	organizer, err := newHTTPManualOrganize(store, accounts, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterManualOrganize(router.Group("/api/admin"), organizer)
	body, err := json.Marshal(service.OrganizeCleanupInput{AccountID: accountID, ParentID: "10", ParentPath: "/来源", Mode: "selected", IDs: []string{"101"}})
	if err != nil {
		t.Fatal(err)
	}
	response := accountRequest(t, router, "POST", "/manual-organize/cleanup/preview", string(body), 200)
	var plan service.OrganizeCleanup
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Path != "/来源/readme.txt" || writes != 0 {
		t.Fatalf("preview=%+v writes=%d", plan, writes)
	}
	confirmation, err := json.Marshal(map[string]string{"accountId": accountID, "previewId": plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		response = accountRequest(t, router, "POST", "/manual-organize/cleanup", string(confirmation), 200)
		var result service.OrganizeCleanup
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != "completed" || result.Items[0].Status != "done" || writes != 1 {
			t.Fatalf("result=%+v writes=%d", result, writes)
		}
		if strings.Contains(response.Body.String(), "private") {
			t.Fatal("credentials in response")
		}
	}
	response = accountRequest(t, router, "GET", "/manual-organize/cleanup", "", 200)
	if !strings.Contains(response.Body.String(), plan.ID) {
		t.Fatal("cleanup result missing from history")
	}
}

func TestManualOrganizeAPIRequiresExplicitAccount(t *testing.T) {
	router, accounts, _ := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "organize.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	organizer, err := newHTTPManualOrganize(store, accounts, p115.NewShareClient(), nil)
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
		{"GET", "/cleanup", "", http.StatusOK},
		{"GET", "/subtitles", "", http.StatusOK},
		{"GET", "/subtitles/videos?jobId=forged", "", http.StatusBadRequest},
		{"POST", "/subtitles/preview", `{"files":[]}`, http.StatusBadRequest},
		{"POST", "/subtitles/import", `{}`, http.StatusBadRequest},
		{"POST", "/cleanup/preview", `{"parentId":"10","parentPath":"/来源","mode":"selected","ids":["30"]}`, http.StatusBadRequest},
		{"POST", "/cleanup", `{"previewId":"forged"}`, http.StatusBadRequest},
		{"POST", "/cleanup/preview", `{`, http.StatusBadRequest},
		{"POST", "/cleanup", `{`, http.StatusBadRequest},
		{"GET", "/files?cid=0", "", http.StatusBadRequest},
		{"PUT", "/config", `{"sourceCID":"0","targetCID":"0","sourcePath":"/","targetPath":"/"}`, http.StatusOK},
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
