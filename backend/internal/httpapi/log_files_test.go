package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func TestLogFilesDownloadAndIncrementalAdminAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logs := &service.GenerationLogs{}
	if err := logs.EnablePersistence(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	proxy := service.NewEmbyProxyLogs(logs)
	ctx, finish := proxy.BeginRequest(proxy.Trace(context.Background()), true)
	proxy.Add(ctx, "ERROR", "preserved failure")
	proxy.AddDetails(ctx, "DEBUG", "memory-only steps", service.ProxyLogDetails{URL: "https://cdn.115.com/file?signature=transient-signature", Status: 502})
	finish()
	logs.Append("STRM", "task", "目录生成", "DEBUG", "transient diagnostics")
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "test-admin" {
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	})
	RegisterLogs(admin, logs)
	call := func(path, auth string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/logs"+path, nil)
		request.Header.Set("Authorization", auth)
		router.ServeHTTP(response, request)
		return response
	}
	for _, path := range []string{"", "/files", "/files/cinema115.log"} {
		if response := call(path, ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("admin boundary missing: %s %d", path, response.Code)
		}
	}
	response := call("/files", "test-admin")
	var list struct {
		Files     []service.GenerationLogFile `json:"files"`
		MaxSizeMB int                         `json:"maxSizeMB"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || response.Code != http.StatusOK || len(list.Files) != 1 || !list.Files[0].Active || list.MaxSizeMB != 20 {
		t.Fatalf("invalid file list: %s err=%v", response.Body.String(), err)
	}
	download := call("/files/"+list.Files[0].Name, "test-admin")
	if download.Code != http.StatusOK || !strings.HasPrefix(download.Header().Get("Content-Type"), "application/x-ndjson") || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") || download.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid download response: %d %v", download.Code, download.Header())
	}
	var saved service.GenerationLog
	if err := json.Unmarshal(download.Body.Bytes(), &saved); err != nil || !strings.HasPrefix(saved.Message, "preserved failure") || saved.Details != nil || strings.Contains(download.Body.String(), "transient") || strings.Contains(download.Body.String(), "memory-only") {
		t.Fatalf("unexpected downloaded history: %s err=%v", download.Body.String(), err)
	}
	if response := call("/files/unrelated.log", "test-admin"); response.Code != http.StatusNotFound {
		t.Fatal("unmanaged file name was not rejected")
	}
	first := call("?module=ALL&level=ALL", "test-admin")
	var snapshot service.LogSnapshot
	if err := json.Unmarshal(first.Body.Bytes(), &snapshot); err != nil || len(snapshot.Entries) != 2 || !snapshot.Persistence.Enabled || snapshot.Entries[1].Details == nil || !strings.Contains(snapshot.Entries[1].Details.Steps, "memory-only steps") {
		t.Fatalf("invalid snapshot: %s err=%v", first.Body.String(), err)
	}
	incremental := call("?module=ALL&level=ALL&after="+strconv.FormatUint(snapshot.Cursor, 10), "test-admin")
	if err := json.Unmarshal(incremental.Body.Bytes(), &snapshot); err != nil || snapshot.Entries == nil || len(snapshot.Entries) != 0 || snapshot.Reset {
		t.Fatalf("incremental polling resent history: %s err=%v", incremental.Body.String(), err)
	}
	if response := call("?after=invalid", "test-admin"); response.Code != http.StatusBadRequest {
		t.Fatal("invalid cursor was accepted")
	}
}
