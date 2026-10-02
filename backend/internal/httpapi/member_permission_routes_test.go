package httpapi

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestMemberPlaybackPermissionEditingRoutesRemoved(t *testing.T) {
	r := gin.New()
	RegisterMemberPlayback(r.Group("/api/me"), r.Group("/api/admin"), nil)
	for _, method := range []string{"GET", "POST"} {
		for _, suffix := range []string{"playback", "permissions"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/api/admin/member-playback/user/"+suffix, nil))
			if w.Code != 404 {
				t.Fatalf("removed route remains: %s %s: %d", method, suffix, w.Code)
			}
		}
	}
}
