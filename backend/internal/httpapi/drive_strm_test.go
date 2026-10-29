package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type driveSTRMAPIFixture struct{}

func (driveSTRMAPIFixture) DriveFiles(context.Context, string, int, string) (*p115.SharePage, error) {
	return &p115.SharePage{}, nil
}
func (driveSTRMAPIFixture) ReadDirectory(_ context.Context, cid, _ string) (p115.DriveDirectory, error) {
	return p115.DriveDirectory{CID: cid, Path: "/library"}, nil
}
func (driveSTRMAPIFixture) LifeEvents(context.Context, int, int, string) (p115.LifeEventPage, error) {
	return p115.LifeEventPage{}, nil
}

func TestDriveSTRMAPIProtectsRuntimeAndPreservesOutputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, err := service.OpenP115AccountsSQLite(ctx, db, root, "api-test-key")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("primary")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := accounts.Get(id)
	cookie := "UID=42_A; CID=private-secret"
	if err := accounts.Update(id, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	scanner := service.NewDriveSTRMService(db, accounts, driveSTRMAPIFixture{}, root)
	events := service.NewDriveSTRMEvents(scanner, driveSTRMAPIFixture{})
	router := gin.New()
	RegisterDriveSTRM(router.Group("/api/admin"), scanner, events, func() string { return "http://localhost:3002" })
	call := func(method, suffix, body string, want int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/admin/p115/drive-strm"+suffix, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, suffix, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "UID=") {
			t.Fatal("credentials leaked")
		}
		return w.Body.Bytes()
	}
	call(http.MethodPost, "", `{}`, http.StatusBadRequest)
	body := `{"name":"Library","accountId":"` + id + `","sourceCID":"100","sourcePath":"forged path","outputDir":"library","baseUrl":"http://localhost:3002","enabled":false,"status":"running","stats":{"written":999},"pendingDirectories":["999"]}`
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(call(http.MethodPost, "", body, http.StatusCreated), &saved); err != nil {
		t.Fatal(err)
	}
	value, err := db.GetDriveSTRM(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value.SourcePath != "/library" || value.Status != "queued" || value.Stats.Written != 0 || len(value.PendingDirectories) != 0 || !value.FullScan {
		t.Fatalf("client overwrote service-owned state: %+v", value)
	}
	listed := string(call(http.MethodGet, "", "", http.StatusOK))
	if strings.Contains(listed, "pendingDirectories") || strings.Contains(listed, "fullScan") {
		t.Fatal("unnecessary internal scan state returned")
	}
	call(http.MethodPut, "/"+saved.ID+"/monitor", `{}`, http.StatusBadRequest)
	call(http.MethodPost, "/"+saved.ID+"/check", "", http.StatusConflict)
	call(http.MethodPost, "/"+saved.ID+"/scan", "", http.StatusAccepted)
	call(http.MethodPut, "/"+saved.ID+"/monitor", `{"enabled":true}`, http.StatusOK)
	call(http.MethodPost, "/"+saved.ID+"/check", "", http.StatusOK)
	keep := filepath.Join(root, "preserved.strm")
	if err := os.WriteFile(keep, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	call(http.MethodDelete, "/"+saved.ID, "", http.StatusNoContent)
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("delete removed local output", err)
	}
	call(http.MethodPost, "/"+saved.ID+"/scan", "", http.StatusNotFound)
}
