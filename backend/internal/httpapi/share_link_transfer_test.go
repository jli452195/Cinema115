package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

type shareTransferAPIFake struct {
	target service.ShareTransferTarget
	calls  int
	err    error
}

func (f *shareTransferAPIFake) Transfer(_ context.Context, target service.ShareTransferTarget, _, _ string) (service.ShareLinkTransferReceipt, error) {
	f.target = target
	f.calls++
	return service.ShareLinkTransferReceipt{TargetPath: target.TargetPath, ItemCount: 1, AcceptedItems: 1, BatchCount: 1}, f.err
}

func TestShareTransferTargetProfilesCRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, accounts, accountID := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "share-transfer.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.NewShareTransferSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	settings.PrimaryID = accounts.PrimaryID
	settings.ValidateAccount = accounts.ValidatePrimary
	RegisterShareLinkTransfer(r.Group("/api/admin"), settings, &shareTransferAPIFake{})
	base := "/api/admin/p115/share-transfer/profiles"
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}

	created := call(http.MethodPost, base, `{"name":"动画转存","accountId":"`+accountID+`","targetCID":"88","targetPath":"/动画"}`)
	if created.Code != http.StatusCreated || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create status=%d headers=%v body=%s", created.Code, created.Header(), created.Body.String())
	}
	if strings.Contains(created.Body.String(), "assignedBotId") || strings.Contains(created.Body.String(), "revision") {
		t.Fatalf("profile API leaked Bot ownership or version metadata: %s", created.Body.String())
	}
	var response struct {
		Profile struct {
			ID         string `json:"id"`
			TargetPath string `json:"targetPath"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Profile.ID == "" || response.Profile.TargetPath != "/动画" {
		t.Fatalf("unexpected created profile: %+v", response.Profile)
	}
	id := response.Profile.ID
	if err := settings.BindTarget(id, "", "bot-one"); err != nil {
		t.Fatal(err)
	}
	updated := call(http.MethodPut, base+"/"+id, `{"name":"电影转存","accountId":"`+accountID+`","targetCID":"99","targetPath":"/电影"}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"targetPath":"/电影"`) || strings.Contains(updated.Body.String(), "assignedBotId") {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	if current, ok := settings.Target(id); !ok || current.AssignedBotID != "bot-one" || current.TargetCID != "99" {
		t.Fatalf("editing a destination lost its Bot assignment or directory: %+v", current)
	}
	listed := call(http.MethodGet, base, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"targetPath":"/电影"`) {
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

func TestShareTransferTargetProfilesRejectInvalidDirectory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, accounts, accountID := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "share-transfer.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.NewShareTransferSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	settings.PrimaryID = accounts.PrimaryID
	settings.ValidateAccount = accounts.ValidatePrimary
	RegisterShareLinkTransfer(r.Group("/api/admin"), settings, &shareTransferAPIFake{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/share-transfer/profiles", strings.NewReader(`{"name":"invalid","accountId":"`+accountID+`","targetCID":"not-a-cid","targetPath":"/坏目录"}`)))
	if w.Code != http.StatusBadRequest || len(settings.Targets()) != 0 {
		t.Fatalf("invalid directory was saved: status=%d body=%s targets=%+v", w.Code, w.Body.String(), settings.Targets())
	}
}

func TestWebShareTransferUsesValidatedDirectorySnapshot(t *testing.T) {
	r, accounts, accountID := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "share-transfer.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.NewShareTransferSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	settings.PrimaryID, settings.ValidateAccount = accounts.PrimaryID, accounts.ValidatePrimary
	target, err := settings.SaveTarget(service.ShareTransferTarget{Name: "动画", AccountID: accountID, TargetCID: "88", TargetPath: "/动画"})
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.BindTarget(target.ID, "", "bot-one"); err != nil {
		t.Fatal(err)
	}
	fake := &shareTransferAPIFake{}
	RegisterShareLinkTransfer(r.Group("/api/admin"), settings, fake)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/api/admin/p115/share-transfer"+path, bytes.NewReader(encoded)))
		return w
	}
	listed := call(http.MethodGet, "/targets", nil)
	var targets struct {
		Profiles  []service.ShareTransferTarget `json:"profiles"`
		AccountID string                        `json:"accountId"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &targets); err != nil || listed.Code != http.StatusOK || len(targets.Profiles) != 1 || targets.AccountID != accountID {
		t.Fatalf("available targets: %s err=%v", listed.Body, err)
	}
	if strings.Contains(listed.Body.String(), "bot-one") || listed.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("target listing exposed Bot metadata or was cacheable")
	}
	post := func(snapshot service.ShareTransferTarget) *httptest.ResponseRecorder {
		return call(http.MethodPost, "", map[string]any{"target": snapshot, "url": "https://115.com/s/test-code?password=abcd"})
	}
	w := post(target)
	if w.Code != http.StatusOK || fake.calls != 1 || fake.target.AssignedBotID != "bot-one" || fake.target.TargetCID != "88" {
		t.Fatalf("shared destination was not used: status=%d body=%s calls=%d", w.Code, w.Body, fake.calls)
	}
	fake.err = errors.New("后续批次失败")
	w = post(target)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"outcomeUncertain":true`) {
		t.Fatalf("partial submission was retryable: status=%d body=%s", w.Code, w.Body)
	}
	for _, mutate := range []func(*service.ShareTransferTarget){
		func(v *service.ShareTransferTarget) { v.TargetCID = "99" },
		func(v *service.ShareTransferTarget) { v.AccountID = "default" },
		func(v *service.ShareTransferTarget) { v.ID = "missing" },
		func(v *service.ShareTransferTarget) { v.AssignedBotID = "injected" },
	} {
		snapshot := target
		mutate(&snapshot)
		before := fake.calls
		w := post(snapshot)
		if w.Code < 400 || fake.calls != before {
			t.Fatalf("invalid snapshot reached transfer: status=%d body=%s", w.Code, w.Body)
		}
	}
	account, err := accounts.Get(accountID)
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	if err := accounts.Update(accountID, account.Revision, account.Name, &empty); err != nil {
		t.Fatal(err)
	}
	listed = call(http.MethodGet, "/targets", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"profiles":[]`) || !strings.Contains(listed.Body.String(), `"accountId":""`) {
		t.Fatalf("unavailable primary retained selectable destinations: %s", listed.Body)
	}
	before := fake.calls
	if w := post(target); w.Code < 400 || fake.calls != before {
		t.Fatalf("missing primary reached transfer: status=%d body=%s", w.Code, w.Body)
	}
}
