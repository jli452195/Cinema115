package httpapi

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"strm115/backend/internal/storage"
	"testing"
)

func TestPortalHTTPAuthenticationAndPermissions(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := portalTestAuth(t, db, "administrator", "admin-password", "secret")
	r := gin.New()
	RegisterPortalAuth(r, auth)
	admin := r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly())
	RegisterPortalInvites(admin, auth)
	admin.GET("/secret", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	call := func(method, path, token, body string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var data map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &data)
		return data
	}
	user := call("POST", "/api/auth/login", "", `{"username":"reader","password":"test-password"}`, 200)["token"].(string)
	manager := call("POST", "/api/auth/admin/login", "", `{"username":"administrator","password":"admin-password"}`, 200)["token"].(string)
	call("POST", "/api/auth/login", "", `{"username":"administrator","password":"admin-password"}`, 401)
	call("GET", "/api/admin/secret", user, "", 403)
	call("GET", "/api/admin/secret", manager, "", 200)
	call("GET", "/api/auth/me", user, "", 200)
	call("POST", "/api/admin/users", manager, `{"username":"reader","password":"test-password"}`, 404)
	call("POST", "/api/admin/invites", manager, `{"count":1,"days":7,"playbackSource":"primary"}`, 400)
	call("POST", "/api/admin/invites", manager, `{"count":1,"days":7,"playbackSource":"assigned"}`, 201)
}
