package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestFolderMonitorScheduleAndManualAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	value := model.FolderMonitor{ID: "test", Name: "monitor", AccountID: "explicit-account", SourceCID: "100", Stage: "verifying", BatchDirectoryID: "900", LogTaskID: "existing-task", LastError: "snapshot pending", Enabled: true, IntervalSeconds: 30, UpdatedAt: time.Now().UTC()}
	if err := db.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	svc := service.NewFolderMonitorService(db, nil, nil, nil, t.TempDir(), nil)
	router := gin.New()
	RegisterFolderMonitors(router.Group("/api/admin"), svc)
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/admin/p115/folder-monitors"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	request(http.MethodPut, "/test/schedule", `{}`, http.StatusBadRequest)
	request(http.MethodPut, "/test/schedule", `{"enabled":true}`, http.StatusBadRequest)
	off := request(http.MethodPut, "/test/schedule", `{"enabled":false}`, http.StatusOK)
	if off["enabled"] != false {
		t.Fatalf("off=%v", off)
	}
	accepted := request(http.MethodPost, "/test/share", `{}`, http.StatusAccepted)
	if accepted["accepted"] != true {
		t.Fatalf("accepted=%v", accepted)
	}
	duplicate := request(http.MethodPost, "/test/scan", `{}`, http.StatusOK)
	if duplicate["accepted"] != false || duplicate["taskId"] != accepted["taskId"] {
		t.Fatalf("duplicate=%v", duplicate)
	}
	listed := request(http.MethodGet, "", "", http.StatusOK)
	view := listed["monitors"].([]any)[0].(map[string]any)
	if view["queued"] != true || view["running"] != false || view["enabled"] != false || view["trigger"] != "manual" {
		t.Fatalf("view=%v", view)
	}
	request(http.MethodPut, "/test/schedule", `{"enabled":false}`, http.StatusOK)
	value, err = db.GetFolderMonitor(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !value.TaskPending || value.Enabled {
		t.Fatal("switch cancelled a queued task")
	}
	value.Stage, value.TaskPending, value.BatchDirectoryID, value.LastError = "blocked", false, "900", "needs review"
	if err := db.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/test/scan", `{}`, http.StatusBadRequest)
	request(http.MethodPost, "/missing/scan", `{}`, http.StatusNotFound)
	request(http.MethodPost, "/test/end", `{"taskId":"stale"}`, http.StatusBadRequest)
	ended := request(http.MethodPost, "/test/end", `{"taskId":"existing-task"}`, http.StatusOK)
	if ended["stage"] != "ended" || ended["hasBatch"] != false || ended["queued"] != false {
		t.Fatalf("not released: %v", ended)
	}
	if len(ended["endedBatches"].([]any)) != 1 {
		t.Fatal("lost retained directory history")
	}
	request(http.MethodPost, "/test/end", `{"taskId":"existing-task"}`, http.StatusOK)
}
