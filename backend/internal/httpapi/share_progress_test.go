package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

type progressGenerator struct{ fail bool }

func (g progressGenerator) Generate(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
	result := &service.GenerationResult{Written: 3, ReshareURL: "https://115cdn.com/s/own?password=abcd"}
	if g.fail {
		return result, errors.New("新分享缺少子文件，已保留临时目录")
	}
	return result, nil
}

func TestGenerationStreamEndsWithResultOrError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fail := range []bool{false, true} {
		api := NewShareAPI("secret", func() string { return "" })
		api.BaseURL = func() string { return "http://nas:8115" }
		api.Workflow = progressGenerator{fail: fail}
		r := gin.New()
		api.Register(r, r.Group("/api/admin"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/generate-stream", strings.NewReader(`{"url":"https://115.com/s/source"}`)))
		if !w.Flushed || !strings.Contains(w.Header().Get("Content-Type"), "application/x-ndjson") || w.Header().Get("X-Accel-Buffering") != "no" {
			t.Fatal("进度响应未及时刷新")
		}
		decoder := json.NewDecoder(w.Body)
		var event struct {
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		if err := decoder.Decode(&event); err != nil || event.Event != "progress" {
			t.Fatal("没有起始进度", err)
		}
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		want := "result"
		if fail {
			want = "error"
		}
		if event.Event != want || event.Data["reshareUrl"] == "" {
			t.Fatal("终态或分享链接丢失")
		}
		if err := decoder.Decode(&event); !errors.Is(err, io.EOF) {
			t.Fatal("响应没有正确结束", err)
		}
	}
}
