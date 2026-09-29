package httpapi

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"strm115/backend/internal/model"

	"strm115/backend/internal/storage"
	"testing"
	"time"
)

func TestWebsiteNotificationIdentityAndReadBoundary(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(ctx, filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	auth := portalTestAuth(t, s, "admin", "admin-password", "secret")
	tokens := make(map[string]string)
	for _, name := range []string{"alice", "bob"} {
		user := portalTestUser(t, auth, name, "test-password")
		token, _, err := auth.Login(ctx, name, "test-password")
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = token
		if err := s.Subscribe(ctx, user.ID, model.Follow{Kind: "movie", TMDBID: 10, Title: "鐢靛奖"}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := s.SaveFollowCheck(ctx, jobs[0], model.FollowProgress{State: "completed", CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterWebsiteNotifications(r.Group("/api/me", PortalAuthenticated(auth)), s)
	call := func(method, path, token, body string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, "/api/me/notifications"+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	call("GET", "", "", "", 401)
	list := call("GET", "?userId=bob", tokens["alice"], "", 200)
	id := int64(list["items"].([]any)[0].(map[string]any)["id"].(float64))
	if list["unread"] != float64(1) {
		t.Fatal(list)
	}
	call("POST", "/read", tokens["bob"], `{"id":`+strconv.FormatInt(id, 10)+`,"userId":"alice"}`, 200)
	if data := call("GET", "/unread", tokens["alice"], "", 200); data["unread"] != float64(1) {
		t.Fatal("浠栦汉鏍囪浜嗛€氱煡", data)
	}
	call("POST", "/read", tokens["alice"], `{"id":`+strconv.FormatInt(id, 10)+`,"all":true}`, 200)
	if data := call("GET", "/unread", tokens["alice"], "", 200); data["unread"] != float64(0) {
		t.Fatal(data)
	}
	call("GET", "?before=-1", tokens["alice"], "", 400)
	call("POST", "/read", tokens["alice"], `{"id":0,"all":true}`, 400)
}
