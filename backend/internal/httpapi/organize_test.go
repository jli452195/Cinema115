package httpapi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"strm115/backend/internal/naming"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type httpNamingTemplates struct{}

func (httpNamingTemplates) NamingTemplate(_ context.Context, kind naming.Kind) (naming.Snapshot, error) {
	template, err := naming.Default(kind)
	return naming.Snapshot{Template: template}, err
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
	manualJob := service.OrganizeJob{ID: "manual", Config: config, Status: "interrupted", Items: []service.OrganizeItem{{File: service.OrganizeFile{ShareFile: p115.ShareFile{ID: "101", Name: "file.mkv", Size: 1}, ParentID: "10", SourcePath: "/source/file.mkv"}, Directory: "Show", Name: "show.mkv", Status: "pending"}}}
	downloadJob := manualJob
	downloadJob.ID = "download"
	downloadJob.Download = &service.ResourceDownload{Hash: strings.Repeat("a", 40), DirectoryID: "10", ParentCID: "0", ParentPath: "/"}
	raw, err := json.Marshal(map[string]any{"version": 1, "config": config, "downloadTarget": service.CloudDownloadConfig{AccountID: accountID, TargetCID: "30", TargetPath: "/download-target"}, "jobs": []service.OrganizeJob{manualJob, downloadJob}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	engine, err := service.NewOrganizeEngine(store, accounts, nil, nil, httpNamingTemplates{})
	if err != nil {
		t.Fatal(err)
	}
	RegisterDownloadOrganize(router.Group("/api/admin"), service.NewDownloadOrganize(engine, nil, nil, nil, nil))
	for _, workflow := range []string{"download-organize"} {
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
		if len(data.Jobs) != 1 || (data.Jobs[0].Download != nil) != (workflow == "download-organize") {
			t.Fatal("workflow history leaked", workflow, data.Jobs)
		}
		if strings.Contains(response.Body.String(), a.Cookie) {
			t.Fatal("history exposed credentials")
		}
	}
	response := accountRequest(t, router, "GET", "/download-organize/config", "", 200)
	if !strings.Contains(response.Body.String(), "download-target") || strings.Contains(response.Body.String(), "sourceCID") {
		t.Fatal("download target depends on manual configuration")
	}
	accountRequest(t, router, "POST", "/manual-organize/resume", `{"jobId":"download"}`, 404)
	accountRequest(t, router, "POST", "/download-organize/resume", `{"jobId":"manual"}`, 400)
	accountRequest(t, router, "POST", "/manual-organize/resume", `{}`, 404)
	accountRequest(t, router, "POST", "/download-organize/resume", `{}`, 400)
	accountRequest(t, router, "POST", "/download-organize/batch/preview", `{"resources":[]}`, 400)
	accountRequest(t, router, "POST", "/download-organize/batch/preview", `{"resources":"invalid"}`, 400)
	accountRequest(t, router, "POST", "/download-organize/batch/jobs", `{}`, 400)
	accountRequest(t, router, "POST", "/download-organize/batch/jobs", `{"previewId":"missing"}`, 409)
}
