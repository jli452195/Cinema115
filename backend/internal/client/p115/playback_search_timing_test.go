package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPlaybackSearchTimingSeparatesRetryWaitAndHTTP(t *testing.T) {
	c := newTestShareClient()
	c.ReadRetryDelays = []time.Duration{15 * time.Millisecond}
	calls, forwarded := 0, 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/files/search" || r.Header.Get("Cookie") != "UID=22_A" {
			t.Fatal("wrong request")
		}
		time.Sleep(2 * time.Millisecond)
		if calls == 1 {
			return instantResponse(429, []byte("private response")), nil
		}
		return instantResponse(200, []byte(`{"state":true,"count":0,"data":[]}`)), nil
	})
	var summary PlaybackTiming
	httpSamples := 0
	ctx := WithRequestTiming(context.Background(), func(v RequestTiming) { forwarded += v.Requests })
	ctx = WithPlaybackTiming(ctx, func(v PlaybackTiming) {
		if v.SearchSummary {
			summary = v
		}
		if v.HTTP {
			httpSamples++
		}
	})
	file, err := c.FindPlaybackFileBySHA1(ctx, strings.Repeat("A", 40), "UID=22_A")
	if err != nil || file != nil {
		t.Fatal(file, err)
	}
	if summary.Requests != 2 || summary.Pages != 1 || summary.Retries != 1 || forwarded != 2 || httpSamples != 2 {
		t.Fatal("wrong counters", summary, forwarded, httpSamples)
	}
	if summary.Wait < 15*time.Millisecond || summary.RequestHTTP < 4*time.Millisecond || summary.Duration < summary.Wait+summary.RequestHTTP || summary.Failed {
		t.Fatal("timings overlap or omit work", summary)
	}
}

func TestPlaybackSearchTimingCountsPaginationAndCanceledQueue(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			c := newTestShareClient()
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if blocked {
					t.Fatal("canceled wait reached network")
				}
				if calls == 1 {
					return instantResponse(200, []byte(`{"state":true,"count":101,"data":[`+strings.Repeat(`{"sha":"other"},`, 99)+`{"sha":"other"}]}`)), nil
				}
				if r.URL.Query().Get("offset") != "100" {
					t.Fatal("incorrect offset")
				}
				return instantResponse(200, []byte(`{"state":true,"count":101,"data":[{"sha":"other"}]}`)), nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if blocked {
				c.playbackReader("UID=22_A").nextRequest = time.Now().Add(time.Minute)
				cancel()
			}
			var summary PlaybackTiming
			ctx = WithPlaybackTiming(ctx, func(v PlaybackTiming) {
				if v.SearchSummary {
					summary = v
				}
			})
			_, err := c.FindPlaybackFileBySHA1(ctx, strings.Repeat("A", 40), "UID=22_A")
			if blocked {
				if !errors.Is(err, context.Canceled) || summary.Requests != 0 || summary.Pages != 0 || !summary.Canceled || !summary.Failed {
					t.Fatal("cancellation counted as query", summary, err)
				}
			} else if err != nil || summary.Pages != 2 || summary.Requests != 2 || summary.Retries != 0 {
				t.Fatal("pagination counted as retry", summary, err)
			}
		})
	}
}
