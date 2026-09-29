package httpapi

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
	"testing"
)

func TestWebsiteInvitationRegistration(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "invites.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := service.NewWebsiteAuth(db, "admin", "admin-password", "test-secret")
	r := gin.New()
	RegisterWebsiteAuth(r, auth)
	RegisterWebsiteUsers(r.Group("/api/admin", WebsiteAuthenticated(auth), WebsiteAdminOnly()), auth)
	call := func(method, path, token string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "code_hash") || strings.Contains(w.Body.String(), "$argon2id$") {
			t.Fatal("secret digest leaked")
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	admin := call("POST", "/api/auth/login", "", map[string]string{"username": "admin", "password": "admin-password"}, 200)["token"].(string)
	call("POST", "/api/admin/invites", "", map[string]int{"count": 1}, 401)
	call("POST", "/api/admin/invites", admin, map[string]int{"count": 21}, 400)
	created := call("POST", "/api/admin/invites", admin, map[string]any{"count": 2, "note": "friends"}, 201)["invites"].([]any)
	first := created[0].(map[string]any)
	second := created[1].(map[string]any)
	code := first["code"].(string)
	if len(code) != 35 || first["expiresAt"].(float64)-first["createdAt"].(float64) != 7*86400 {
		t.Fatal("invalid generated invite")
	}
	listing := call("GET", "/api/admin/invites", admin, nil, 200)["invites"].([]any)
	if _, ok := listing[0].(map[string]any)["code"]; ok {
		t.Fatal("list leaked original code")
	}
	call("POST", "/api/auth/invite/check", "", map[string]string{"code": code}, 200)
	call("POST", "/api/auth/invite/check", "", map[string]string{"code": code}, 200)
	call("POST", "/api/auth/register", "", map[string]string{"code": code, "username": "admin", "password": "test-password"}, 400)
	call("POST", "/api/auth/register", "", map[string]string{"code": code, "username": "reader", "password": "short"}, 400)
	registered := call("POST", "/api/auth/register", "", map[string]string{"code": strings.ToUpper(code), "username": "reader", "password": "test-password", "role": "admin"}, 201)["user"].(map[string]any)
	if registered["role"] != "user" {
		t.Fatal("registration escalated role")
	}
	call("POST", "/api/auth/register", "", map[string]string{"code": code, "username": "reader2", "password": "test-password"}, 400)
	call("POST", "/api/auth/invite/check", "", map[string]string{"code": code}, 400)
	token := call("POST", "/api/auth/login", "", map[string]string{"username": "reader", "password": "test-password"}, 200)["token"].(string)
	call("POST", "/api/admin/invites", token, map[string]int{"count": 1}, 403)
	call("GET", "/api/admin/invites", token, nil, 403)
	call("POST", "/api/admin/invites/"+second["id"].(string)+"/revoke", token, map[string]string{}, 403)
	call("POST", "/api/admin/invites/"+second["id"].(string)+"/revoke", admin, map[string]string{}, 200)
	call("POST", "/api/auth/invite/check", "", map[string]string{"code": second["code"].(string)}, 400)
	listing = call("GET", "/api/admin/invites", admin, nil, 200)["invites"].([]any)
	for _, entry := range listing {
		invite := entry.(map[string]any)
		if invite["id"] == first["id"] && invite["username"] != "reader" {
			t.Fatal("missing registration user")
		}
	}
	// Anonymous registration work has its own bounded rate budget.
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest("POST", "/api/auth/invite/check", strings.NewReader(`{"code":"invalid"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == 429 {
			if w.Header().Get("Retry-After") != "60" {
				t.Fatal("missing retry delay")
			}
			return
		}
	}
	t.Fatal("registration rate limit not enforced")
}
