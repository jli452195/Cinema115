package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type cloudSubmitAPIFake struct {
	task              p115.CloudTask
	found             bool
	removeErr, addErr error
	steps             []string
	cid               string
	budget            time.Duration
}

func (f *cloudSubmitAPIFake) LookupTask(ctx context.Context, _ string, _ p115.CloudResource) (p115.CloudTask, bool, error) {
	f.steps = append(f.steps, "lookup")
	if deadline, ok := ctx.Deadline(); ok {
		f.budget = time.Until(deadline)
	}
	return f.task, f.found, nil
}
func (f *cloudSubmitAPIFake) DeleteTaskKeepFiles(_ context.Context, _, hash string) error {
	f.steps = append(f.steps, "delete:"+hash)
	return f.removeErr
}
func (f *cloudSubmitAPIFake) AddURL(_ context.Context, _, _, cid string) (json.RawMessage, error) {
	f.steps = append(f.steps, "add")
	f.cid = cid
	return json.RawMessage(`{"info_hash":"0123456789abcdef0123456789abcdef01234567"}`), f.addErr
}

func TestCloudDownloadPOSTDefaultsToSafeResubmit(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name, display, errorText string
		found                    bool
		removeErr, addErr        error
		status                   int
		removed, uncertain       bool
		steps                    int
	}{
		{name: "新任务", status: 202, steps: 2},
		{name: "网页默认删除重提", found: true, display: "finished", status: 202, removed: true, steps: 3},
		{name: "运行任务不动", found: true, display: "running", status: 409, steps: 1, errorText: "未删除或重新提交"},
		{name: "未知任务不动", found: true, status: 409, steps: 1, errorText: "未执行删除"},
		{name: "删除失败不提交", found: true, display: "finished", removeErr: errors.New("删除被拒绝"), status: 502, steps: 2, errorText: "删除被拒绝"},
		{name: "删除后提交失败", found: true, display: "finished", addErr: errors.New("提交被拒绝"), status: 502, removed: true, steps: 3, errorText: "旧任务记录已移除，但重新提交失败"},
		{name: "删除后超时", found: true, display: "finished", addErr: context.DeadlineExceeded, status: 504, removed: true, uncertain: true, steps: 3, errorText: "重新提交结果尚未确认"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, accounts, id := setupAccountsAPI(t, nil, nil)
			settings := testCloudDownloadSettings(t, accounts)
			client := &cloudSubmitAPIFake{task: p115.CloudTask{InfoHash: hash, DisplayStatus: tc.display}, found: tc.found, removeErr: tc.removeErr, addErr: tc.addErr}
			RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &service.CloudDownloadCoordinator{}}, p115.NewCloudClient(), p115.NewShareClient())
			request := httptest.NewRequest(http.MethodPost, "/api/admin/p115/cloud-download/tasks?accountId="+id, strings.NewReader(`{"url":"magnet:?xt=urn:btih:`+hash+`","targetCID":"99"}`))
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			var body struct {
				Accepted         bool
				RemovedOldTask   bool
				OutcomeUncertain bool
				Error            string
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || body.Accepted != (tc.status == 202) || body.RemovedOldTask != tc.removed || body.OutcomeUncertain != tc.uncertain || !strings.Contains(body.Error, tc.errorText) || len(client.steps) != tc.steps {
				t.Fatalf("status=%d body=%s steps=%v", response.Code, response.Body.String(), client.steps)
			}
			if tc.removed && tc.status != 202 && !strings.Contains(body.Error, "网盘文件已保留") {
				t.Fatal("未报告文件保留")
			}
			if tc.status == 202 && client.cid != "99" {
				t.Fatal("未提交到本次指定目录")
			}
			if client.budget < 85*time.Second || client.budget > service.CloudDownloadTimeout {
				t.Fatal("提交预算不是 90 秒", client.budget)
			}
			if strings.Contains(response.Body.String(), "UID=") {
				t.Fatal("响应泄露凭据")
			}
		})
	}
}

func TestCloudDownloadRequiresConfiguredPrimary(t *testing.T) {
	r, accounts, id := setupAccountsAPI(t, nil, nil)
	if err := accounts.Delete(id, 2); err != nil {
		t.Fatal(err)
	}
	settings := testCloudDownloadSettings(t, accounts)
	cloud := p115.NewCloudClient()
	RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: cloud, Coordinator: &service.CloudDownloadCoordinator{}}, cloud, p115.NewShareClient())
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
		if w.Code != http.StatusConflict {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestCloudDownloadNamedTargetProfilesCRUD(t *testing.T) {
	r, accounts, accountID := setupAccountsAPI(t, nil, nil)
	settings := testCloudDownloadSettings(t, accounts)
	cloud := p115.NewCloudClient()
	RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: cloud, Coordinator: &service.CloudDownloadCoordinator{}}, cloud, p115.NewShareClient())
	base := "/api/admin/p115/cloud-download/profiles"
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	created := call(http.MethodPost, base, `{"name":"转存 XX","accountId":"`+accountID+`","targetCID":"0","targetPath":"/"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var response struct {
		Profile struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			TargetPath string `json:"targetPath"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Profile.ID == "" || response.Profile.Name != "转存 XX" || response.Profile.TargetPath != "/" || strings.Contains(created.Body.String(), `"revision"`) || strings.Contains(created.Body.String(), `"assignedBotId"`) {
		t.Fatalf("unexpected target response, including bot-owned metadata: %+v", response.Profile)
	}
	id := response.Profile.ID
	updated := call(http.MethodPut, base+"/"+id, `{"name":"转存 XC","accountId":"`+accountID+`","targetCID":"88","targetPath":"/XC"}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"name":"转存 XC"`) {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	listed := call(http.MethodGet, base, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"targetPath":"/XC"`) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	deleted := call(http.MethodDelete, base+"/"+id, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	listed = call(http.MethodGet, base, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"profiles":[]`) {
		t.Fatalf("empty list status=%d body=%s", listed.Code, listed.Body.String())
	}
}

func TestCloudDownloadDirectoryAndTaskUseSelectedAccount(t *testing.T) {
	r, accounts, id := setupAccountsAPI(t, nil, nil)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.Header.Get("Cookie"), "UID=222_B") {
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
	RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: cloud, Coordinator: &service.CloudDownloadCoordinator{}}, cloud, drive)

	for _, path := range []string{
		"/api/admin/p115/cloud-download/paths?accountId=" + id + "&cid=0",
		"/api/admin/p115/cloud-download/tasks?accountId=" + id,
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
	settings.ValidateAccount = accounts.ValidatePrimary
	settings.PrimaryID = accounts.PrimaryID
	return settings
}
