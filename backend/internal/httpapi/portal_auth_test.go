package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
	"testing"
)

func TestPortalAuthUnavailableIsRetryableAndSanitized(t *testing.T) {
	r := gin.New()
	r.GET("/probe", func(c *gin.Context) {
		portalAuthError(c, errors.Join(service.ErrAuthUnavailable, errors.New("private-api-key")))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/probe", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "private-api-key") {
		t.Fatal("transient authentication failure logged out user or leaked details", w.Code)
	}
}

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
	login := call("POST", "/api/auth/admin/login", "", `{"username":"administrator","password":"admin-password"}`, 200)
	manager := login["token"].(string)
	if login["expiresIn"] != service.PortalSessionLifetime.Seconds() {
		t.Fatal("HTTP login lifetime differs from JWT lifetime")
	}
	call("POST", "/api/auth/login", "", `{"username":"administrator","password":"admin-password"}`, 401)
	call("GET", "/api/admin/secret", user, "", 403)
	call("GET", "/api/admin/secret", manager, "", 200)
	call("GET", "/api/auth/me", user, "", 200)
	if call("GET", "/api/auth/me", manager, "", 200)["token"] != nil {
		t.Fatal("fresh login unnecessarily renewed")
	}
	now := time.Now()
	nearExpiry, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "admin", "role": "admin", "iss": "cinema115-portal",
		"iat": now.Add(-6 * 24 * time.Hour).Unix(), "exp": now.Add(time.Hour).Unix(),
	}).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	renewed, ok := call("GET", "/api/auth/me", nearExpiry, "", 200)["token"].(string)
	if !ok || renewed == "" || renewed == nearExpiry {
		t.Fatal("session probe did not renew near-expiry login")
	}
	call("GET", "/api/admin/secret", renewed, "", 200)
	call("GET", "/api/auth/me", "invalid-token", "", 401)
	call("POST", "/api/admin/users", manager, `{"username":"reader","password":"test-password"}`, 404)
	call("POST", "/api/admin/invites", manager, `{"count":1,"days":7,"playbackSource":"primary"}`, 400)
	call("POST", "/api/admin/invites", manager, `{"count":1,"days":7,"playbackSource":"assigned"}`, 201)
}
