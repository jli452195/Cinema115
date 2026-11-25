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
	addErr error
	steps  []string
	cid    string
	budget time.Duration
}

func (f *cloudSubmitAPIFake) LookupTask(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
	f.steps = append(f.steps, "lookup")
	return p115.CloudTask{}, false, nil
}
func (f *cloudSubmitAPIFake) DeleteTaskKeepFiles(_ context.Context, _, hash string) error {
	f.steps = append(f.steps, "delete:"+hash)
	return nil
}
func (f *cloudSubmitAPIFake) AddURL(ctx context.Context, _, raw, cid string) (json.RawMessage, error) {
	f.steps = append(f.steps, "add")
	f.cid = cid
	if deadline, ok := ctx.Deadline(); ok {
		f.budget = time.Until(deadline)
	}
	resource, err := p115.ParseCloudResource(raw)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"info_hash":"` + resource.Hash + `"}`), f.addErr
}
func (f *cloudSubmitAPIFake) SubmitURL(ctx context.Context, cookie, raw, cid string) error {
	_, err := f.AddURL(ctx, cookie, raw, cid)
	return err
}
func (f *cloudSubmitAPIFake) AddURLs(_ context.Context, _ string, urls []string, cid string) ([]p115.CloudURLAddResult, error) {
	f.steps = append(f.steps, "batch")
	f.cid = cid
	items := make([]p115.CloudURLAddResult, 0, len(urls))
	for _, raw := range urls {
		resource, err := p115.ParseCloudResource(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, p115.CloudURLAddResult{URL: raw, InfoHash: resource.Hash})
	}
	return items, f.addErr
}

func TestCloudDownloadPOSTSubmitsDirectly(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name, errorText string
		addErr          error
		status          int
		uncertain       bool
	}{
		{name: "提交成功", status: 202},
		{name: "提交被拒绝", addErr: errors.New("提交被拒绝"), status: 502, errorText: "拒绝提交"},
		{name: "提交超时", addErr: context.DeadlineExceeded, status: 504, uncertain: true, errorText: "结果未确认"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, accounts, id := setupAccountsAPI(t, nil, nil)
			settings := testCloudDownloadSettings(t, accounts)
			client := &cloudSubmitAPIFake{addErr: tc.addErr}
			RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &service.CloudDownloadCoordinator{}}, p115.NewCloudClient(), p115.NewShareClient())
			request := httptest.NewRequest(http.MethodPost, "/api/admin/p115/cloud-download/tasks?accountId="+id, strings.NewReader(`{"url":"magnet:?xt=urn:btih:`+hash+`","targetCID":"99"}`))
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			var body struct {
				Accepted         bool
				OutcomeUncertain bool
				Error            string
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || body.Accepted != (tc.status == 202) || body.OutcomeUncertain != tc.uncertain || !strings.Contains(body.Error, tc.errorText) || strings.Join(client.steps, ",") != "add" {
				t.Fatalf("status=%d body=%s steps=%v", response.Code, response.Body.String(), client.steps)
			}
			if body.Accepted && response.Body.String() != `{"accepted":true}` {
				t.Fatal("成功响应包含多余字段", response.Body.String())
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
