package p115

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlaybackSourcePrefetchOverlapsSubmitAndWaitsForRange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := NewShareClient()
	started, submitted := make(chan struct{}), make(chan struct{})
	var challenged atomic.Bool
	var loads, ranges atomic.Int32
	source := testInstantInfo()
	source.URL = ""
	source.loadPlaybackURL = func(ctx context.Context) (string, error) {
		loads.Add(1)
		close(started)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-submitted:
			return "https://cdn.115cdn.net/video?private-signed-url", nil
		}
	}
	info := testInstantInfo()
	info.PrefetchRangeSource = func(ctx context.Context) func() {
		var stop func()
		source, stop = StartPlaybackSourcePrefetch(ctx, source)
		return stop
	}
	info.ResolveRangeSHA1 = func(ctx context.Context, start, end int64) (string, error) {
		return client.PlaybackRangeSHA1(ctx, source, start, end)
	}
	posts := 0
	var mu sync.Mutex
	var timings []PlaybackTiming
	ctx = WithPlaybackTiming(ctx, func(v PlaybackTiming) { mu.Lock(); timings = append(timings, v); mu.Unlock() })
	client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/files":
			return instantResponse(200, []byte(`{"state":true,"cid":"0","path":[{"cid":"0"}]}`)), nil
		case "/files/uploadinfo":
			return instantResponse(200, []byte(`{"user_id":22,"userkey":"fresh-key"}`)), nil
		case "/4.0/initupload.php":
			posts++
			if posts == 1 {
				select {
				case <-r.Context().Done():
					return nil, r.Context().Err()
				case <-started:
				}
				close(submitted)
				challenged.Store(true)
				return instantResponse(200, encryptedInstantResponse(t, `{"status":7,"sign_key":"private-challenge","sign_check":"1-3"}`)), nil
			}
			return instantResponse(200, encryptedInstantResponse(t, `{"status":2,"pickcode":"abc12345","file_id":"999"}`)), nil
		case "/video":
			ranges.Add(1)
			if !challenged.Load() || r.Header.Get("Range") != "bytes=1-3" || r.Header.Get("Cookie") != "" {
				t.Error("speculative file read or credential leak")
			}
			if trace := httptrace.ContextClientTrace(r.Context()); trace != nil {
				trace.GotConn(httptrace.GotConnInfo{Reused: true})
				trace.GotFirstResponseByte()
			}
			resp := instantResponse(206, []byte("abc"))
			resp.Header.Set("Content-Range", "bytes 1-3/100")
			return resp, nil
		default:
			return nil, errors.New("unexpected request")
		}
	})
	result, err := client.InstantUpload(ctx, info, "0", "UID=22_A; SEID=private-cookie", nil)
	if err != nil || result == nil || !result.Challenged || posts != 2 || loads.Load() != 1 || ranges.Load() != 1 {
		t.Fatal("prefetch/submit did not overlap", err)
	}
	mu.Lock()
	defer mu.Unlock()
	stages := make(map[string]bool)
	for _, v := range timings {
		stages[v.Stage] = true
		if strings.Contains(v.Stage, "private") {
			t.Fatal("secret in timing")
		}
		if v.Stage == "校验片段 CDN" && (!v.Reused || !v.Connected) {
			t.Fatal("connection reuse not traced")
		}
	}
	for _, stage := range []string{"校验源直链预取（与目标查找及首次提交重叠）", "获取校验源直链（预取时为剩余等待）", "校验片段 CDN", "片段读取及哈希", "片段 SHA1 计算（包含在读取耗时内）", "秒传提交 HTTP"} {
		if !stages[stage] {
			t.Fatal("missing timing", stage)
		}
	}
}

func TestPlaybackPrefetchJoinsOnEveryUploadExit(t *testing.T) {
	for _, mode := range []string{"success", "parse-error", "network-error", "cancelled", "source-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			started, exited := make(chan struct{}), make(chan struct{})
			source := testInstantInfo()
			source.loadPlaybackURL = func(ctx context.Context) (string, error) {
				close(started)
				defer close(exited)
				if mode == "source-error" {
					return "", errors.New("private source failure")
				}
				<-ctx.Done()
				return "", ctx.Err()
			}
			info := testInstantInfo()
			info.PrefetchRangeSource = func(ctx context.Context) func() { _, stop := StartPlaybackSourcePrefetch(ctx, source); return stop }
			client := NewShareClient()
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/files":
					return instantResponse(200, []byte(`{"state":true,"cid":"0","path":[{"cid":"0"}]}`)), nil
				case "/files/uploadinfo":
					return instantResponse(200, []byte(`{"user_id":22,"userkey":"key"}`)), nil
				case "/4.0/initupload.php":
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-started:
					}
					switch mode {
					case "cancelled":
						cancel()
						return nil, ctx.Err()
					case "network-error":
						return nil, errors.New("network failure")
					case "parse-error":
						return instantResponse(200, []byte("bad")), nil
					}
					return instantResponse(200, encryptedInstantResponse(t, `{"status":2,"file_id":"999","pickcode":"abc12345"}`)), nil
				default:
					t.Error("unexpected file read")
					return nil, errors.New("unexpected request")
				}
			})
			result, err := client.InstantUpload(ctx, info, "0", "UID=22_A", nil)
			if (mode == "success" || mode == "source-error") && (err != nil || result == nil) {
				t.Fatal("unused prefetch affected success", err)
			}
			if mode != "success" && mode != "source-error" && err == nil {
				t.Fatal("failure hidden")
			}
			select {
			case <-exited:
			default:
				t.Fatal("prefetch worker outlived upload")
			}
		})
	}
}

func TestPlaybackPrefetchDoesNotStartBeforePreflight(t *testing.T) {
	info := testInstantInfo()
	info.PrefetchRangeSource = func(context.Context) func() { t.Fatal("prefetch ran before validating target"); return nil }
	if _, err := NewShareClient().InstantUpload(context.Background(), info, "invalid", "UID=22_A", nil); err == nil {
		t.Fatal("invalid target accepted")
	}
}
