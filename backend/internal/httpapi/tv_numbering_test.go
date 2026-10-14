package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type numberingManagerFake struct {
	tmdbScheduleFake
	writes int
}

func (f *numberingManagerFake) NumberingChoices(context.Context, int) (*service.NumberingChoices, error) {
	return &service.NumberingChoices{Current: tmdb.NumberingInfo{Revision: 1}}, nil
}
func (f *numberingManagerFake) PreviewNumbering(context.Context, int, string) (*service.NumberingPreview, error) {
	return &service.NumberingPreview{}, nil
}
func (f *numberingManagerFake) SaveNumbering(_ context.Context, _ int, input service.SaveNumberingInput) (*tmdb.NumberingInfo, error) {
	if input.Revision != 1 {
		return nil, storage.ErrNumberingConflict
	}
	f.writes++
	return &tmdb.NumberingInfo{Revision: 2}, nil
}
func (f *numberingManagerFake) PinNumbering(ctx context.Context, _ int) (context.Context, error) {
	return ctx, nil
}
func (f *numberingManagerFake) NumberingRevision(context.Context, int) (int64, error) { return 1, nil }
func TestNumberingAdminOnlyAndReadRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	manager := &numberingManagerFake{}
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "admin" {
			c.AbortWithStatus(403)
		}
	})
	RegisterTMDB(admin, manager)
	RegisterTVNumbering(admin, manager)
	RegisterTMDB(r.Group("/api/catalog"), manager)
	for _, tc := range []struct {
		method, path, auth, body string
		status                   int
	}{
		{"GET", "/api/admin/tmdb/tv/80462/episode-groups", "", "", 403},
		{"PUT", "/api/admin/tmdb/tv/80462/numbering", "user", `{"revision":1}`, 403},
		{"PUT", "/api/catalog/tmdb/tv/80462/numbering", "user", `{"revision":1}`, 404},
		{"GET", "/api/admin/tmdb/tv/0/episode-groups", "admin", "", 400},
		{"GET", "/api/admin/tmdb/tv/80462/episode-groups", "admin", "", 200},
		{"GET", "/api/admin/tmdb/tv/80462/episode-groups/group12", "admin", "", 200},
		{"PUT", "/api/admin/tmdb/tv/80462/numbering", "admin", `{"revision":0}`, 409},
		{"PUT", "/api/admin/tmdb/tv/80462/numbering", "admin", `invalid`, 400},
		{"PUT", "/api/admin/tmdb/tv/80462/numbering", "admin", `{"revision":1}`, 200},
		{"GET", "/api/catalog/tmdb/tv/80462/schedules?numberingRevision=0", "user", "", 409},
		{"GET", "/api/catalog/tmdb/tv/80462/schedule/1?numberingRevision=0", "user", "", 409},
		{"GET", "/api/catalog/tmdb/tv/80462/season/1?numberingRevision=1", "user", "", 200},
		{"GET", "/api/catalog/tmdb/tv/80462/schedules?numberingRevision=bad", "user", "", 400},
	} {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Authorization", tc.auth)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s %s: %d, %s", tc.method, tc.path, response.Code, response.Body.String())
		}
	}
	if manager.writes != 1 {
		t.Fatal("unexpected writes", manager.writes)
	}
}
