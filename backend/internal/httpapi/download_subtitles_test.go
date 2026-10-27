package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/service"
)

func TestDownloadSubtitleMultipartLimitAndValidation(t *testing.T) {
	router, accounts, _ := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "organize.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := service.NewOrganizeEngine(store, accounts, nil, nil, httpNamingTemplates{})
	if err != nil {
		t.Fatal(err)
	}
	RegisterDownloadOrganize(router.Group("/api/admin"), service.NewDownloadOrganize(engine, nil, nil, nil, nil))
	for _, duplicate := range []bool{false, true} {
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		if err = writer.WriteField("previewId", "expired"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 1+map[bool]int{true: 1}[duplicate]; i++ {
			part, err := writer.CreateFormFile(strings.Repeat("a", 32), "a.ass")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write(bytes.Repeat([]byte("s"), 600<<10)); err != nil {
				t.Fatal(err)
			}
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "/api/admin/p115/download-organize/subtitles/attachments", &payload)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		want := 409 // The >512 KiB upload reaches preview validation.
		if duplicate {
			want = 400
		}
		if recorder.Code != want || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("duplicate=%v status=%d body=%s", duplicate, recorder.Code, recorder.Body.String())
		}
	}
}
