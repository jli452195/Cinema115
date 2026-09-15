package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestWebsiteUserAuthenticationAndPermissions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "users.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	auth := service.NewWebsiteAuth(db, "administrator", "admin-password", "test-secret")
	r := gin.New()
	RegisterWebsiteAuth(r, auth)
	admin := r.Group("/api/admin", WebsiteAuthenticated(auth), WebsiteAdminOnly())
	RegisterWebsiteUsers(admin, auth)
	admin.GET("/secrets", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	catalog := r.Group("/api/catalog", WebsiteAuthenticated(auth))
	catalog.GET("/browse", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
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
			t.Fatalf("%s %s status=%d want=%d", method, path, w.Code, want)
		}
		if strings.Contains(w.Body.String(), "$argon2id$") || strings.Contains(w.Body.String(), "token_version") {
			t.Fatal("响应泄露了密码哈希或凭证版本")
		}
		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	login := func(name, password string, want int) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"username": name, "password": password})
		data := call("POST", "/api/auth/login", "", string(body), want)
		token, _ := data["token"].(string)
		return token
	}
	adminToken := login("administrator", "admin-password", 200)
	call("GET", "/api/admin/users", "", "", 401)
	call("POST", "/api/admin/users", adminToken, `{"username":"Administrator","password":"test-password"}`, 400)
	call("POST", "/api/admin/users", adminToken, `{"username":"reader","password":"short"}`, 400)
	created := call("POST", "/api/admin/users", adminToken, `{"username":"reader","password":"test-password","role":"admin"}`, 201)
	u := created["user"].(map[string]any)
	id := u["id"].(string)
	if u["role"] != "user" {
		t.Fatal("创建普通用户时允许提权")
	}
	call("POST", "/api/admin/users", adminToken, `{"username":"READER","password":"test-password"}`, 409)
	userToken := login("READER", "test-password", 200)
	login("reader", "wrong-password", 401)
	login("missing-user", "test-password", 401)
	call("GET", "/api/catalog/browse", userToken, "", 200)
	call("GET", "/api/admin/secrets", userToken, "", 403)
	call("GET", "/api/admin/users", userToken, "", 403)
	call("POST", "/api/admin/users", userToken, `{"username":"attacker","password":"test-password"}`, 403)
	call("POST", "/api/admin/users/"+id, userToken, `{"disabled":false}`, 403)
	me := call("GET", "/api/auth/me", userToken, "", 200)
	if me["user"].(map[string]any)["id"] != id {
		t.Fatal("用户身份不稳定")
	}
	stored, err := db.WebsiteUser(ctx, id)
	if err != nil || !strings.HasPrefix(stored.PasswordHash, "$argon2id$") || strings.Contains(stored.PasswordHash, "test-password") {
		t.Fatal("密码没有正确哈希存储", err)
	}
	call("POST", "/api/admin/users/"+id, adminToken, `{"password":"new-password"}`, 200)
	call("GET", "/api/auth/me", userToken, "", 401)
	login("reader", "test-password", 401)
	userToken = login("reader", "new-password", 200)
	call("POST", "/api/admin/users/"+id, adminToken, `{"disabled":true}`, 200)
	call("GET", "/api/catalog/browse", userToken, "", 401)
	login("reader", "new-password", 401)
	call("POST", "/api/admin/users/"+id, adminToken, `{"disabled":false}`, 200)
	call("GET", "/api/auth/me", userToken, "", 401)
	userToken = login("reader", "new-password", 200)
	// 重新打开数据库模拟重启，账号和凭证版本仍然持久保存。
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	restored := service.NewWebsiteAuth(db, "administrator", "admin-password", "test-secret")
	if got, err := restored.Authenticate(ctx, userToken); err != nil || got.ID != id {
		t.Fatal("重启后账号未恢复", err)
	}
	// 原管理员令牌兼容；未知主体、缺少到期时间或伪造角色不能越权。
	for _, tc := range []struct {
		sub, role string
		expiry    bool
		want      int
	}{
		{"administrator", "", true, 200}, {"reader", "", true, 401}, {"user:" + id, "admin", true, 401}, {"administrator", "user", true, 401}, {"administrator", "admin", false, 401},
	} {
		claims := jwt.MapClaims{"sub": tc.sub, "role": tc.role}
		if tc.expiry {
			claims["exp"] = time.Now().Add(time.Hour).Unix()
		}
		value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		// 路由使用最初的存储对象，重启后的验证直接检查新服务。
		_, err = restored.Authenticate(ctx, value)
		if (err == nil) != (tc.want == 200) {
			t.Fatalf("异常令牌校验 sub=%s role=%s", tc.sub, tc.role)
		}
	}
}

func TestWebsiteCatalogDoesNotExposeServerMetadata(t *testing.T) {
	result := catalogMatches([]model.EmbyMatch{{Kind: "tv", TMDB: "100", State: "present", Items: []model.EmbyItem{{ID: "private-file", LibraryID: "private-library", SeriesID: "private-series", ParentID: "private-parent", Name: "private-path", Type: "Episode", Season: 2, Episode: 3, EndEpisode: 4}}}})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-") {
		t.Fatal("普通用户可读取服务器媒体信息")
	}
	if item := result[0].Items[0]; item.Season != 2 || item.Episode != 3 || item.EndEpisode != 4 {
		t.Fatal("脱敏破坏了入库集号")
	}
}
