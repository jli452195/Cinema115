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
	"strm115/backend/internal/model"
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
	initial := string(call(http.MethodGet, "", "", http.StatusOK))
	if !strings.Contains(initial, `"settingsSaved":false`) || strings.Contains(initial, `"defaults"`) || !strings.Contains(initial, `"baseUrl":"http://localhost:3002"`) {
		t.Fatalf("missing initial settings: %s", initial)
	}
	body := `{"name":"Library","accountId":"` + id + `","sourceCID":"100","sourcePath":"forged path","outputDir":"library","baseUrl":"https://forged.example","settings":{"baseUrl":"https://forged.example"},"enabled":false,"status":"running","stats":{"written":999},"pendingDirectories":["999"]}`
	call(http.MethodPost, "", body, http.StatusBadRequest)
	call(http.MethodPut, "/settings", `{"baseUrl":"http://localhost:3002","videoExtensions":"mkv,mp4","metadataExtensions":"srt,ass,ssa","minSizeMB":0}`, http.StatusOK)
	createPolicy := `{"reconcileEnabled":false,"reconcileIntervalHours":168,"fullScanEnabled":true,"fullScanIntervalHours":336}`
	body = strings.TrimSuffix(body, "}") + `,"schedule":` + createPolicy + `}`
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
	if value.Schedule.ReconcileEnabled || value.Schedule.ReconcileIntervalHours != 168 || !value.Schedule.FullScanEnabled || value.Schedule.FullScanIntervalHours != 336 {
		t.Fatal("创建请求未同时保存自定义周期", value.Schedule)
	}
	for _, invalidPolicy := range []string{`null`, `{}`, `{"reconcileEnabled":false,"reconcileIntervalHours":168,"fullScanEnabled":true}`, `{"reconcileEnabled":false,"reconcileIntervalHours":168,"fullScanEnabled":true,"fullScanIntervalHours":336,"eventIntervalSeconds":20}`} {
		invalidBody := strings.Replace(body, createPolicy, invalidPolicy, 1)
		call(http.MethodPut, "/"+saved.ID, invalidBody, http.StatusBadRequest)
	}
	withoutPolicy := strings.Replace(body, `,"schedule":`+createPolicy, "", 1)
	call(http.MethodPut, "/"+saved.ID, withoutPolicy, http.StatusOK)
	value, err = db.GetDriveSTRM(ctx, saved.ID)
	if err != nil || value.Schedule.ReconcileIntervalHours != 168 || value.Schedule.FullScanIntervalHours != 336 {
		t.Fatal("编辑省略周期时覆盖了已保存设置", value.Schedule, err)
	}
	settings, err := db.GetDriveSTRMSettings(ctx)
	if err != nil || settings.BaseURL != "http://localhost:3002" || value.Settings != (model.DriveSTRMSettings{}) {
		t.Fatalf("mapping input overwrote settings: %+v %v", settings, err)
	}
	rawMapping, _ := json.Marshal(value)
	if strings.Contains(string(rawMapping), "baseUrl") || strings.Contains(string(rawMapping), "videoExtensions") {
		t.Fatalf("mapping contains duplicate settings: %s", rawMapping)
	}
	listed := string(call(http.MethodGet, "", "", http.StatusOK))
	if strings.Contains(listed, `"pendingDirectories"`) || strings.Contains(listed, `"fullScan"`) {
		t.Fatal("unnecessary internal scan state returned")
	}
	call(http.MethodPut, "/"+saved.ID+"/monitor", `{}`, http.StatusBadRequest)
	call(http.MethodPost, "/"+saved.ID+"/check", "", http.StatusConflict)
	call(http.MethodPost, "/"+saved.ID+"/scan", "", http.StatusAccepted)
	call(http.MethodPut, "/"+saved.ID+"/monitor", `{"enabled":true}`, http.StatusOK)
	call(http.MethodPost, "/"+saved.ID+"/check", "", http.StatusOK)
	value, err = db.GetDriveSTRM(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	value.Status, value.Queued = "running", false
	value.Stats.Written = 7
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	schedulePath := "/" + saved.ID + "/schedule"
	for _, invalid := range []string{
		`{}`,
		`null`,
		`{"schedule":null}`,
		`{"schedule":true}`,
		`{"schedule":{}}`,
		`{"schedule":{"reconcileIntervalHours":24,"fullScanIntervalHours":720}}`,
		`{"schedule":{"reconcileEnabled":null,"reconcileIntervalHours":24,"fullScanEnabled":false,"fullScanIntervalHours":720}}`,
		`{"schedule":{"reconcileEnabled":true,"reconcileIntervalHours":0,"fullScanEnabled":false,"fullScanIntervalHours":720}}`,
		`{"schedule":{"reconcileEnabled":false,"reconcileIntervalHours":24,"fullScanEnabled":true,"fullScanIntervalHours":12}}`,
		`{"schedule":{"reconcileEnabled":false,"reconcileIntervalHours":24,"fullScanEnabled":false,"fullScanIntervalHours":720,"eventIntervalSeconds":20}}`,
	} {
		call(http.MethodPut, schedulePath, invalid, http.StatusBadRequest)
	}
	customBody := `{"schedule":{"reconcileEnabled":true,"reconcileIntervalHours":6,"fullScanEnabled":false,"fullScanIntervalHours":336},"status":"stopped","stats":{"written":999}}`
	var policyResponse model.DriveSTRM
	if err := json.Unmarshal(call(http.MethodPut, schedulePath, customBody, http.StatusOK), &policyResponse); err != nil {
		t.Fatal(err)
	}
	if policyResponse.Schedule.ReconcileIntervalHours != 6 || policyResponse.Schedule.FullScanIntervalHours != 336 || policyResponse.Status != "running" || policyResponse.Stats.Written != 7 || !policyResponse.UpdatedAt.Equal(value.UpdatedAt) {
		t.Fatal("运行中更新周期覆盖了任务状态或丢失禁用周期", policyResponse)
	}
	call(http.MethodPut, "/missing/schedule", customBody, http.StatusNotFound)
	call(http.MethodPut, schedulePath, `{"schedule":{"reconcileEnabled":false,"reconcileIntervalHours":24,"fullScanEnabled":false,"fullScanIntervalHours":720}}`, http.StatusOK)
	value, err = db.GetDriveSTRM(ctx, saved.ID)
	if err != nil || value.Schedule.ReconcileEnabled || value.Schedule.FullScanEnabled || value.Status != "running" || value.Stats.Written != 7 {
		t.Fatal("关闭目录周期修改了运行任务", value, err)
	}
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
