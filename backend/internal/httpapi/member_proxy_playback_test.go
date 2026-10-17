package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProxyPlaybackRoutesRequireAdminAndExplicitFields(t *testing.T) {
	for _, role := range []string{"", "user", "admin"} {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("portal_role", role) })
		registerMemberProxyPlayback(r.Group("/members"), nil, func(*gin.Context, error) { t.Fatal("unexpected service call") })
		for _, body := range []string{`{}`, `{"enabled":false}`, `{"expectedRevision":0}`, `{"enabled":null,"expectedRevision":0}`, `{"enabled":"false","expectedRevision":0}`, `{"enabled":false,"expectedRevision":-1}`} {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/members/reader/proxy-playback", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			want := 403
			if role == "admin" {
				want = 400
			}
			if w.Code != want {
				t.Fatal(role, body, w.Code)
			}
		}
		if role != "admin" {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/members/reader/proxy-playback", nil))
			if w.Code != 403 {
				t.Fatal(w.Code)
			}
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/members/reader/account-state", strings.NewReader(`{"enabled":false,"expectedEnabled":true}`)))
		if w.Code != 404 {
			t.Fatal("old global account mutation endpoint still exists", w.Code)
		}
	}
}
