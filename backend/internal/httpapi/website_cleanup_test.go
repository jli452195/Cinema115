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

func TestWebsiteCleanupAdminOnlyAndStaleSession(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := service.NewWebsiteAuth(db, "admin", "admin-password", "secret")
	u, err := auth.Create(ctx, "reader", "password123")
	if err != nil {
		t.Fatal(err)
	}
	userToken, _, err := auth.Login(ctx, "reader", "password123")
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := auth.Login(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := service.OpenP115AccountsSQLite(ctx, db, t.TempDir(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	auth.Members, err = service.NewMemberPlayback(ctx, db, accounts, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterWebsiteUsers(r.Group("/api/admin", WebsiteAuthenticated(auth), WebsiteAdminOnly()), auth)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	path := "/api/admin/users/" + u.ID + "/cleanup"
	for _, p := range []string{path, "/api/admin/users/cleanup-records"} {
		if w := call("GET", p, userToken, ""); w.Code != 403 {
			t.Fatal("member can inspect cleanup", w.Code)
		}
	}
	if w := call("POST", path, userToken, `{}`); w.Code != 403 {
		t.Fatal("member can delete user", w.Code)
	}
	w := call("GET", path, adminToken, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var preview service.WebsiteCleanupPreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if w := call("POST", path, adminToken, `{"mode":"none","username":"reader"}`); w.Code != 400 {
		t.Fatal("confirmation bypass", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"mode": "none", "username": "reader", "fingerprint": preview.Fingerprint})
	if w := call("POST", path, adminToken, string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := auth.Authenticate(ctx, userToken); err == nil {
		t.Fatal("deleted login still valid")
	}
	if w := call("GET", "/api/admin/users/cleanup-records", adminToken, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "reader") {
		t.Fatal("cleanup history missing")
	}
}
