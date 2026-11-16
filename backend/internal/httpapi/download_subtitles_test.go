package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func downloadSubtitleAPIFixture(t *testing.T) *gin.Engine {
	t.Helper()
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
	return router
}

func TestDownloadSubtitleMultipartLimitAndValidation(t *testing.T) {
	router := downloadSubtitleAPIFixture(t)
	var err error
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

func TestDownloadSubtitleMultipartLongSeries(t *testing.T) {
	router := downloadSubtitleAPIFixture(t)
	for _, count := range []int{112, 224, service.MaxSubtitleImportFiles, service.MaxSubtitleImportFiles + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var payload bytes.Buffer
			writer := multipart.NewWriter(&payload)
			if err := writer.WriteField("previewId", "expired"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				part, err := writer.CreateFormFile(fmt.Sprintf("%032x", i), fmt.Sprintf("Show.%04d.ass", i))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = part.Write([]byte("subtitle")); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "/api/admin/p115/download-organize/subtitles/attachments", &payload)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			want := 409
			if count > service.MaxSubtitleImportFiles {
				want = 400
			}
			if recorder.Code != want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if want == 400 && !strings.Contains(recorder.Body.String(), "1000 个") {
				t.Fatal(recorder.Body.String())
			}
		})
	}
}

func TestDownloadSubtitlePreviewMetadataBudget(t *testing.T) {
	router := downloadSubtitleAPIFixture(t)
	subtitles := make([]service.DownloadSubtitleSelection, service.MaxSubtitleImportFiles)
	for i := range subtitles {
		subtitles[i] = service.DownloadSubtitleSelection{LocalID: fmt.Sprintf("%032x", i), OriginalName: strings.Repeat("字幕", 100) + ".ass", VideoID: fmt.Sprint(i), SHA256: strings.Repeat("a", 64), Size: 8}
	}
	for _, path := range []string{"/preview", "/batch/preview"} {
		t.Run(path, func(t *testing.T) {
			payload := map[string]any{"subtitles": subtitles}
			if path == "/batch/preview" {
				payload["resources"] = []any{map[string]any{"subtitles": subtitles}}
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) <= 512<<10 || len(body) >= 8<<20 {
				t.Fatal("测试元数据未覆盖预期请求预算", len(body))
			}
			request := httptest.NewRequest("POST", "/api/admin/p115/download-organize"+path, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != 400 || !strings.Contains(recorder.Body.String(), "每批最多") {
				t.Fatalf("元数据未到达业务校验：%d %s", recorder.Code, recorder.Body.String())
			}
			oversized := `{"padding":"` + strings.Repeat("a", 8<<20) + `"}`
			request = httptest.NewRequest("POST", "/api/admin/p115/download-organize"+path, strings.NewReader(oversized))
			request.Header.Set("Content-Type", "application/json")
			recorder = httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != 400 || strings.Contains(recorder.Body.String(), "每批最多") {
				t.Fatalf("超限预览未拒绝：%d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
