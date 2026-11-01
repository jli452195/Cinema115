package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

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
	RegisterShareLinkTransfer(r.Group("/api/admin"), settings)
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
	RegisterShareLinkTransfer(r.Group("/api/admin"), settings)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/share-transfer/profiles", strings.NewReader(`{"name":"invalid","accountId":"`+accountID+`","targetCID":"not-a-cid","targetPath":"/坏目录"}`)))
	if w.Code != http.StatusBadRequest || len(settings.Targets()) != 0 {
		t.Fatalf("invalid directory was saved: status=%d body=%s targets=%+v", w.Code, w.Body.String(), settings.Targets())
	}
}
