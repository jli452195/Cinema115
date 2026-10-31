package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/mediacategory"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestMediaClassificationHTTP(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "classification.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	auth := portalTestAuth(t, db, "admin", "admin-password", "classification-secret")
	admin, _, err := auth.LoginAdmin(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := auth.Login(ctx, "reader", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "classification.yaml")
	settings, err := service.OpenMediaClassification(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterMediaClassification(r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), settings)
	call := func(method, endpoint, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/admin/classification"+endpoint, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: status=%d, cache=%q, body=%s", method, endpoint, w.Code, w.Header().Get("Cache-Control"), w.Body.String())
		}
		return w
	}
	payload := func(source, revision string) string {
		t.Helper()
		raw, err := json.Marshal(map[string]string{"yaml": source, "revision": revision})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	decode := func(w *httptest.ResponseRecorder, out any) {
		t.Helper()
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []struct{ method, endpoint, body string }{
		{"GET", "", ""}, {"PUT", "", "{}"}, {"POST", "/reload", ""},
	} {
		call(route.method, route.endpoint, "", route.body, 401)
		call(route.method, route.endpoint, user, route.body, 403)
	}
	var initial struct {
		Record      service.ClassificationDocument `json:"record"`
		DefaultYAML string                         `json:"defaultYAML"`
	}
	decode(call("GET", "", admin, "", 200), &initial)
	if initial.DefaultYAML != mediacategory.DefaultYAML() || initial.Record.YAML != initial.DefaultYAML || len(initial.Record.Revision) != 64 || initial.Record.ActiveRevision != initial.Record.Revision {
		t.Fatal("missing initial editor state", initial)
	}
	original := initial.Record
	source := "# retain comment\r\n" + strings.ReplaceAll(strings.Replace(original.YAML, "其他电影", "测试", 1), "\n", "\r\n")
	var saved service.ClassificationDocument
	body := payload(source, original.Revision)
	decode(call("PUT", "", admin, body, 200), &saved)
	if saved.YAML != source || saved.Revision == original.Revision || saved.ActiveRevision != saved.Revision {
		t.Fatal("saved document mismatch", saved)
	}
	call("PUT", "", admin, body, 200) // A lost save response can be confirmed safely.
	conflict := call("PUT", "", admin, payload("# stale\n"+original.YAML, original.Revision), 409)
	if !strings.Contains(conflict.Body.String(), `"code":"REVISION_CONFLICT"`) {
		t.Fatal(conflict.Body.String())
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != source {
		t.Fatal("conflict changed saved bytes", err)
	}
	invalid := strings.Replace(source, "genres: [16]", "genres: []", 1)
	var invalidResponse struct {
		Code       string                     `json:"code"`
		Diagnostic *mediacategory.ConfigError `json:"diagnostic"`
	}
	decode(call("PUT", "", admin, payload(invalid, saved.Revision), 400), &invalidResponse)
	if invalidResponse.Code != "INVALID_CLASSIFICATION" || invalidResponse.Diagnostic == nil || invalidResponse.Diagnostic.Field != "movie.rules[0].match.genres" || invalidResponse.Diagnostic.Line <= 0 || invalidResponse.Diagnostic.Column <= 0 {
		t.Fatal("missing field diagnostic", invalidResponse)
	}
	if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	var external struct {
		Record service.ClassificationDocument `json:"record"`
	}
	decode(call("GET", "", admin, "", 200), &external)
	if external.Record.YAML != invalid || external.Record.LoadError == "" || external.Record.Diagnostic == nil || external.Record.Revision == saved.Revision || external.Record.ActiveRevision != saved.Revision {
		t.Fatal("broken external file must remain editable", external)
	}
	call("POST", "/reload", admin, "", 400)
	if snapshot, err := settings.Snapshot(); err != nil || snapshot.YAML != saved.YAML {
		t.Fatal("failed reload changed active rules", err)
	}
	call("PUT", "", admin, payload(original.YAML, saved.Revision), 409)
	call("PUT", "", admin, payload(original.YAML, external.Record.Revision), 200)
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	decode(call("POST", "/reload", admin, "", 200), &saved)
	if saved.YAML != source || saved.ActiveRevision != saved.Revision {
		t.Fatal("reload did not activate the external file", saved)
	}

	t.Run("invalid requests preserve rules", func(t *testing.T) {
		for _, body := range []string{
			`{}`, `null`, `[]`, `{"yaml":null,"revision":""}`, `{"yaml":""}`, `{"yaml":"","revision":null}`,
			`{"yaml":123,"revision":""}`, `{"yaml":"","revision":false}`, payload(source, saved.Revision) + `{}`,
			`{"yaml":"","revision":"","extra":true}`, payload(source+"\n---\n", saved.Revision),
			payload(strings.Repeat(" ", mediacategory.MaxYAMLBytes+1), saved.Revision),
		} {
			call("PUT", "", admin, body, 400)
		}
		w := call("PUT", "", admin, payload(strings.Repeat(" ", 1<<20), saved.Revision), 413)
		if !strings.Contains(w.Body.String(), `"code":"REQUEST_TOO_LARGE"`) {
			t.Fatal(w.Body.String())
		}
		if snapshot, err := settings.Snapshot(); err != nil || snapshot.Revision != saved.Revision {
			t.Fatal("invalid request changed active rules", err)
		}
	})

	t.Run("storage errors conceal paths", func(t *testing.T) {
		// A NUL path produces an I/O error on both Windows and Linux, independently
		// of the current user's filesystem permissions.
		unavailable, err := service.OpenMediaClassification(ctx, filepath.Join(t.TempDir(), "secret\x00.yaml"))
		if err == nil {
			t.Fatal("expected storage error")
		}
		failed := gin.New()
		RegisterMediaClassification(failed.Group("/api/admin"), unavailable)
		for _, method := range []string{"GET", "PUT", "POST"} {
			endpoint := "/api/admin/classification"
			if method == "POST" {
				endpoint += "/reload"
			}
			req := httptest.NewRequest(method, endpoint, strings.NewReader(payload(source, saved.Revision)))
			w := httptest.NewRecorder()
			failed.ServeHTTP(w, req)
			if w.Code != 500 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"code":"CLASSIFICATION_UNAVAILABLE"`) {
				t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
			}
		}
	})
}

func TestMediaClassificationHTTPCancellation(t *testing.T) {
	settings, err := service.OpenMediaClassification(context.Background(), filepath.Join(t.TempDir(), "classification.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterMediaClassification(r.Group("/api/admin"), settings)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/api/admin/classification/reload", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 408 || !strings.Contains(w.Body.String(), `"code":"REQUEST_TIMEOUT"`) {
		t.Fatalf("cancellation: %d %s", w.Code, w.Body.String())
	}
}
