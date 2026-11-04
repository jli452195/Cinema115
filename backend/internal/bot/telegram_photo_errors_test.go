package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramPhotoErrorsUseSafeClassifications(t *testing.T) {
	for _, tc := range []struct {
		description string
		want        string
	}{
		{"failed to get HTTP URL content", "Telegram 无法读取图片地址"},
		{"PHOTO_INVALID_DIMENSIONS", "图片格式或尺寸不受支持"},
		{"file is too big", "图片超过大小限制"},
		{"can't parse entities", "图片说明文字或格式无效"},
		{"unknown failure", "图片请求未被接受"},
	} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(tc.want+"/"+http.StatusText(status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"ok": false, "error_code": 400,
						"description": tc.description + " https://images.example.com/private.jpg?credential=private-secret token=123:test-token",
					})
				}))
				defer server.Close()
				worker := &Telegram{Token: "123:test-token", HTTP: server.Client(), BaseURL: server.URL}
				err := worker.call(context.Background(), "sendPhoto", map[string]any{"chat_id": 42, "photo": "https://images.example.com/poster.jpg"}, nil)
				if !errors.Is(err, errTelegramBadRequest) || !strings.Contains(err.Error(), tc.want) {
					t.Fatal("图片错误分类丢失", err)
				}
				for _, secret := range []string{"private-secret", "test-token", "https://", "credential", tc.description} {
					if strings.Contains(err.Error(), secret) {
						t.Fatal("原始错误详情泄露", secret, err)
					}
				}
			})
		}
	}
}
