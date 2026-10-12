package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/client/woniu"
	"strm115/backend/internal/service"
)

type woniuFake struct {
	input      service.WoniuSearchInput
	configured bool
	err        error
	calls      int
}

func (f *woniuFake) View() service.WoniuView {
	return service.WoniuView{Configured: f.configured}
}
func (f *woniuFake) Configure(context.Context, service.WoniuConfigInput) (service.WoniuView, error) {
	return f.View(), nil
}
func (f *woniuFake) Clear(context.Context) error { return nil }
func (f *woniuFake) Search(_ context.Context, input service.WoniuSearchInput) (service.WoniuSearchResult, error) {
	f.input = input
	f.calls++
	return service.WoniuSearchResult{}, f.err
}

type woniuTMDBFake struct{ tmdbReaderFake }

func (*woniuTMDBFake) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "服务端中文名", OriginalTitle: "Server Original"}}, nil
}

func TestWoniuRoutesMetadataGateRolesAndSafeErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &woniuFake{configured: true}
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) { c.Set("portal_role", c.GetHeader("X-Test-Role")) }, PortalAdminOnly())
	RegisterWoniu(admin, fake, &woniuTMDBFake{})
	request := func(body, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/admin/woniu/search", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Test-Role", role)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	valid := `{"kind":"movie","tmdbId":"1","title":"forged","originalTitle":"forged","url":"http://localhost"}`
	if w := request(valid, "user"); w.Code != http.StatusForbidden || fake.calls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(valid, "admin"); w.Code != http.StatusOK || fake.input.Title != "服务端中文名" || fake.input.OriginalTitle != "Server Original" {
		t.Fatal(w.Code, fake.input)
	}
	fake.configured = false
	if w := request(valid, "admin"); w.Code != 409 || !strings.Contains(w.Body.String(), "NOT_CONFIGURED") || fake.calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	fake.configured = true
	for _, body := range []string{`{"kind":"tv","tmdbId":"1","page":5}`, `{"kind":"tv","tmdbId":"1","page":2}`, `{"kind":"tv","tmdbId":"1","method":"custom"}`} {
		if w := request(body, "admin"); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	fake.err = fmt.Errorf("private-response cookie=secret: %w", woniu.ErrRateLimited)
	w := request(valid, "admin")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "RATE_LIMITED") {
		t.Fatal(w.Code, w.Body.String())
	}
}
