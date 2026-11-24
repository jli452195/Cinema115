package p115

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloudRequestGateSharesCooldownWithMarkedDirectoryReads(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/files" {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte(`{"state":true,"info_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","wp_path_id":"1","display_status":"downloading"}`))
	}))
	defer server.Close()
	cloud := NewCloudClient()
	cloud.HTTP, cloud.BaseURL = server.Client(), server.URL
	share := NewShareClient()
	share.HTTP, share.WebURL = server.Client(), server.URL
	ctx := cloud.WithRequestGate(context.Background())

	_, err := share.ReadDirectory(ctx, "0", "UID=42_A1")
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Status != http.StatusMethodNotAllowed || upstream.RetryAfter != 2*time.Minute {
		t.Fatalf("目录读取应保留原始响应：%v", err)
	}
	cooldown := cloud.RequestCooldown("42")
	if time.Until(cooldown) < 119*time.Second {
		t.Fatalf("共享冷却未遵守 Retry-After：%v", cooldown)
	}
	hash := strings.Repeat("a", 32)
	_, err = cloud.InspectED2KTask(context.Background(), "UID=42_D1", hash)
	var deferred *CloudRequestDeferredError
	if !errors.As(err, &deferred) || !errors.Is(err, ErrShareThrottled) || !deferred.RetryAt.Equal(cooldown) || requests.Load() != 1 {
		t.Fatalf("同 UID 的查询应在发送前延后：请求数=%d，错误=%v", requests.Load(), err)
	}
	_, err = cloud.AddURL(context.Background(), "UID=42_F1", "ed2k://|file|movie.mkv|123|"+hash+"|/", "1")
	if !errors.As(err, &deferred) || requests.Load() != 1 {
		t.Fatalf("同 UID 的写入应在发送前延后：请求数=%d，错误=%v", requests.Load(), err)
	}
	observation, err := cloud.InspectED2KTask(context.Background(), "UID=43_A1", hash)
	if err != nil || !observation.Present || requests.Load() != 2 {
		t.Fatalf("其他 UID 应独立处理：请求数=%d，错误=%v", requests.Load(), err)
	}
}

func TestCloudRequestGateCancellationWhileWaitingDoesNotSend(t *testing.T) {
	gate := &cloudRequestGate{}
	first, err := gate.acquire(context.Background(), "UID=42_A1")
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lease, err := gate.acquire(ctx, "UID=42_D1")
	var deferred *CloudRequestDeferredError
	if lease != nil || !errors.As(err, &deferred) || !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应返回明确的未发送错误：%v", err)
	}
}
