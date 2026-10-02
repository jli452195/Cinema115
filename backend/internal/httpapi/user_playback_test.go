package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestUserPlaybackAdminReleaseRoutes(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "slots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	slots, err := service.NewUserPlayback(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := slots.Acquire(ctx, model.MemberPlayback{UserID: "profile", ServerID: "server", EmbyID: "user"}, service.PoolRequest{Session: "sensitive-digest", Item: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) { c.Set("portal_role", c.GetHeader("Test-Role")) }, PortalAdminOnly())
	RegisterMemberPlayback(r.Group("/api/me"), admin, &service.MemberPlaybackService{UserPlayback: slots})
	call := func(method, path, body, role string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-Role", role)
		r.ServeHTTP(w, req)
		return w
	}
	base := "/api/admin/member-playback/playback-sessions"
	release := base + "/" + lease.Record.Key + "/release"
	for _, role := range []string{"", "user"} {
		if w := call("GET", base, "", role); w.Code != 403 {
			t.Fatal("list exposed", w.Code)
		}
		if w := call("POST", release, `{"generation":1}`, role); w.Code != 403 {
			t.Fatal("release exposed", w.Code)
		}
	}
	if w := call("GET", base, "", "admin"); w.Code != 200 || strings.Contains(w.Body.String(), lease.Record.SessionDigest) {
		t.Fatal("list failed or exposed digest", w.Code)
	}
	if w := call("POST", release, `{}`, "admin"); w.Code != 400 || !lease.Current() {
		t.Fatal("missing generation accepted", w.Code)
	}
	if w := call("POST", release, fmt.Sprintf(`{"generation":%d}`, lease.Record.Generation+1), "admin"); w.Code != 409 || !lease.Current() {
		t.Fatal("stale release accepted", w.Code)
	}
	if w := call("POST", release, fmt.Sprintf(`{"generation":%d}`, lease.Record.Generation), "admin"); w.Code != 200 || lease.Current() {
		t.Fatal("release failed", w.Code)
	}
}
