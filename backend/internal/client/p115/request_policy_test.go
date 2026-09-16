package p115

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestShareClient() *ShareClient {
	c := NewShareClient()
	c.RequestInterval = 0
	c.ReadRetryDelays = nil
	return c
}

func TestShareClientDefaultRequestPolicy(t *testing.T) {
	c := NewShareClient()
	if c.RequestInterval != 250*time.Millisecond {
		t.Fatalf("默认请求间隔 = %s，期望 250ms", c.RequestInterval)
	}
	if len(c.ReadRetryDelays) != 2 || c.ReadRetryDelays[0] != 5*time.Second || c.ReadRetryDelays[1] != 15*time.Second {
		t.Fatalf("默认只读重试间隔异常：%v", c.ReadRetryDelays)
	}
}

func TestMetadataReadRecoversAfter405(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Query().Get("receive_code") != "secret" {
			t.Error("重试改变了原请求")
		}
		if calls == 1 {
			w.WriteHeader(405)
			return
		}
		w.Write([]byte(`{"state":true,"data":{"count":1,"list":[{"fid":"1","n":"电影.mkv","s":100}]}}`))
	}))
	defer server.Close()
	c := newTestShareClient()
	c.WebURL = server.URL
	c.ReadRetryDelays = []time.Duration{time.Millisecond}
	reports := 0
	ctx := WithRequestProgress(context.Background(), func(string) { reports++ })
	page, err := c.List(ctx, &ShareInfo{Code: "demo", Password: "secret"}, "0", 0, "")
	if err != nil || len(page.Files) != 1 || calls != 2 || reports != 1 {
		t.Fatal("没有正确恢复目录请求", err, calls, reports)
	}
}

func TestMetadataRetryLimitAndWritesNeverRepeated(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		c := newTestShareClient()
		c.ReadRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
		calls := 0
		_, err := c.metadataRequest(context.Background(), method, func() ([]byte, error) { calls++; return nil, &HTTPError{Status: 405, Method: method} })
		want := 3
		if method == http.MethodPost {
			want = 1
		}
		var upstream *HTTPError
		if calls != want || !errors.As(err, &upstream) {
			t.Fatal("请求次数错误", method, calls, err)
		}
	}
}

func TestMetadataCooldownHonorsRetryAfterAndCancellation(t *testing.T) {
	c := newTestShareClient()
	c.ReadRetryDelays = []time.Duration{time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithRequestProgress(ctx, func(string) { cancel() })
	calls := 0
	_, err := c.metadataRequest(ctx, http.MethodGet, func() ([]byte, error) { calls++; return nil, &HTTPError{Status: 429, RetryAfter: time.Minute} })
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Until(c.nextRequest) < 50*time.Second {
		t.Fatal("取消或服务器冷却时间失效", err, calls)
	}
	// 上一次取消必须释放串行令牌，后续请求可感知自己的超时。
	next, stop := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer stop()
	_, err = c.metadataRequest(next, http.MethodGet, func() ([]byte, error) { t.Error("冷却期不应发出请求"); return nil, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestMetadataRequestsAreSerialAndSpaced(t *testing.T) {
	c := newTestShareClient()
	c.RequestInterval = 10 * time.Millisecond
	var active atomic.Int32
	var wg sync.WaitGroup
	starts := make([]time.Time, 0, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.metadataRequest(context.Background(), http.MethodGet, func() ([]byte, error) {
				if active.Add(1) != 1 {
					t.Error("目录请求发生并发")
				}
				starts = append(starts, time.Now())
				active.Add(-1)
				return nil, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < c.RequestInterval {
			t.Fatal("未应用请求间隔")
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if retryAfter("12") != 12*time.Second || retryAfter("invalid") != 0 {
		t.Fatal("Retry-After 秒数解析错误")
	}
	if got := retryAfter(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)); got < 58*time.Second || got > time.Minute {
		t.Fatal("Retry-After 日期解析错误", got)
	}
}
