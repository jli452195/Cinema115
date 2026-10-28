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
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/service"
)

type httpMediaMetadata struct{}

func (httpMediaMetadata) Browse(context.Context, tmdb.Options) (*tmdb.Page, error) {
	return &tmdb.Page{Items: []tmdb.Item{{ID: 42, Kind: "movie", Title: "Work", Date: "2024-01-01"}}}, nil
}
func (httpMediaMetadata) Details(context.Context, string, int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: 42, Kind: "movie", Title: "作品", Date: "2024-01-01"}}, nil
}

func TestMediaOrganizeAPIWorkflowAndValidation(t *testing.T) {
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "media.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	s, err := service.NewMediaOrganizer(store, httpMediaMetadata{}, httpNamingTemplates{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer admin-test" {
			c.AbortWithStatus(401)
			return
		}
		c.Next()
	})
	RegisterMediaOrganize(admin, s)
	request := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/admin/media-organize"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer admin-test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache enabled")
		}
		return w
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/media-organize", nil))
	if w.Code != 401 {
		t.Fatal("missing admin middleware")
	}
	for _, query := range []string{"?page=0", "?page=-1", "?page=abc"} {
		request("GET", query, "", 400)
	}
	var state service.MediaOrganizeState
	readState := func(query string) {
		t.Helper()
		response := request("GET", query, "", 200)
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
	}
	readState("")
	if state.History.Items == nil || state.History.Total != 0 || state.History.Page != 1 {
		t.Fatal("missing empty history contract", state)
	}
	source, target := t.TempDir(), t.TempDir()
	file := filepath.Join(source, "Work.2024.strm")
	const body = "http://unmodified.invalid/strm"
	if err := os.WriteFile(file, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	rule := service.MediaOrganizeRule{Name: "API验收", Storage: "local", Source: source, Target: target, Failure: t.TempDir(), IntervalMinutes: 5}
	raw, _ := json.Marshal(rule)
	response := request("POST", "/rules", string(raw), 200)
	if err := json.Unmarshal(response.Body.Bytes(), &rule); err != nil {
		t.Fatal(err)
	}
	if rule.Failure == "" {
		t.Fatal("optional failure directory was lost")
	}
	request("POST", "/rules", `{"scope":"strm"}`, 400)
	request("POST", "/rules", `{"extra":"`+strings.Repeat("x", 1<<20)+`"}`, 400)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("worker leaked")
		}
	}()
	response = request("POST", "/rules/"+rule.ID+"/run", "", 202)
	var run service.MediaOrganizeRun
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	wait := func(status string) {
		t.Helper()
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			snapshot, err := s.Snapshot(run.ID)
			if err == nil && snapshot.Status == status {
				run = snapshot
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("task did not reach", status)
	}
	wait("completed")
	data, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(run.Entries[0].Relative)))
	if err != nil || string(data) != body {
		t.Fatal("file contents changed", err)
	}
	request("GET", "/runs/"+run.ID+"/task?fileId=Work.2024.strm", "", 200)
	request("POST", "/runs/"+run.ID+"/retry", "{\"fileId\":\"Work.2024.strm\",\"season\":-1}", 400)
	request("POST", "/runs/"+run.ID+"/retry", "{}", 400)
	readState("?page=99")
	if state.History.Page != 1 || state.History.Total != 1 || state.History.Items[0].Status != "done" || state.History.Items[0].Target == "" {
		t.Fatal("executed file missing from history", state)
	}
	request("DELETE", "/rules/"+rule.ID, "", 200)
}
