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

func TestMemberPlaybackHTTPRequiresOwnerAndAdminRoles(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "members.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := service.NewWebsiteAuth(db, "admin", "admin-password", "secret")
	if _, err := auth.Create(ctx, "reader", "password123"); err != nil {
		t.Fatal(err)
	}
	memberToken, _, err := auth.Login(ctx, "reader", "password123")
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
	s, err := service.NewMemberPlayback(ctx, db, accounts, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterMemberPlayback(r.Group("/api/me", WebsiteAuthenticated(auth)), r.Group("/api/admin", WebsiteAuthenticated(auth), WebsiteAdminOnly()), s)
	for _, tc := range []struct {
		path, token string
		want        int
	}{
		{"/api/me/playback", "", 401}, {"/api/me/playback", adminToken, 403}, {"/api/me/playback?userId=someone-else&accountId=primary", memberToken, 200},
		{"/api/admin/member-playback", memberToken, 403}, {"/api/admin/member-playback", adminToken, 200},
		{"/api/admin/member-playback/accounts", memberToken, 403},
		{"/api/admin/member-playback/users", memberToken, 403}, {"/api/admin/member-playback/users", adminToken, 200},
		{"/api/admin/member-playback/adoption/target", memberToken, 403},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s returned %d expected %d", tc.path, w.Code, tc.want)
		}
		if tc.want == 200 {
			var data map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(w.Body.String(), "cookie") || strings.Contains(w.Body.String(), "password") {
				t.Fatal("credentials exposed")
			}
			if strings.HasPrefix(tc.path, "/api/me/") && data["stage"] != "unbound" {
				t.Fatal("user scope incorrect")
			}
		}
	}
	for _, path := range []string{"/api/admin/member-playback/reader/source", "/api/admin/member-playback/reader", "/api/admin/member-playback/reader/sync", "/api/admin/member-playback/adoption/target"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"source":"primary","blocked":false}`))
		req.Header.Set("Authorization", "Bearer "+memberToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatal("member can grant playback", path, w.Code)
		}
	}
	for _, operation := range []string{"username", "password"} {
		for _, body := range []string{`{"username":"newname","password":"password123"}`, `{"username":"newname","password":"password123","revision":0,"userId":"someone-else","embyId":"someone-else"}`} {
			req := httptest.NewRequest("POST", "/api/me/playback/"+operation, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+memberToken)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 400 {
				t.Fatal("unprovisioned or stale update accepted", operation, w.Code)
			}
		}
	}
}
