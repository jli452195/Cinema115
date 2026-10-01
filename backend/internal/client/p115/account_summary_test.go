package p115

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountSummaryFieldsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, info, space  string
		valid, known, fail bool
		vip                string
		bytes              int64
	}{
		{"vip-decimal", `{"state":true,"data":{"user_id":123,"vip":1}}`, `{"state":true,"data":{"space_info":{"all_remain":{"size":55260545842165.09}}}}`, true, true, false, "vip", 55260545842165},
		{"normal-zero", `{"state":1,"data":{"user_id":"123","vip":"0"}}`, `{"state":true,"data":{"space_info":{"all_remain":{"size":"0"}}}}`, true, true, false, "normal", 0},
		{"unknown", `{"state":true,"data":{"user_id":123}}`, `{"state":true,"data":{}}`, true, false, false, "", 0},
		{"space-error", `{"state":true,"data":{"user_id":123,"vip":1}}`, `{"state":false}`, true, false, false, "vip", 0},
		{"negative", `{"state":true,"data":{"user_id":123,"vip":0}}`, `{"state":true,"data":{"space_info":{"all_remain":{"size":-1}}}}`, true, false, false, "normal", 0},
		{"wrong-uid", `{"state":true,"data":{"user_id":456,"vip":1}}`, `{}`, false, false, true, "", 0},
		{"invalid", `{"state":false}`, `{}`, false, false, false, "", 0},
		{"missing-state", `{"data":{"user_id":123,"vip":1}}`, `{}`, false, false, true, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/info" {
					fmt.Fprint(w, tc.info)
				} else {
					fmt.Fprint(w, tc.space)
				}
			}))
			defer srv.Close()
			c := NewAccountClient()
			c.InfoURL = srv.URL + "/info"
			c.SpaceURL = srv.URL + "/space"
			got, valid, err := c.QuerySummary(context.Background(), "config", "UID=123_A; SEID=test")
			if valid != tc.valid || (err != nil) != tc.fail || got.VIP != tc.vip || got.SpaceKnown != tc.known || got.RemainingBytes != tc.bytes {
				t.Fatalf("unexpected metadata: %+v valid=%v err=%v", got, valid, err)
			}
			if !valid && calls != 1 {
				t.Fatal("invalid identity queried space")
			}
		})
	}
}

func TestAccountSummaryCoalescesAndSeparatesCredentials(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		if r.URL.Path == "/info" {
			fmt.Fprint(w, `{"state":true,"data":{"user_id":123,"vip":1}}`)
		} else {
			fmt.Fprint(w, `{"state":true,"data":{"space_info":{"all_remain":{"size":0}}}}`)
		}
	}))
	defer srv.Close()
	c := NewAccountClient()
	c.InfoURL = srv.URL + "/info"
	c.SpaceURL = srv.URL + "/space"
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := c.QuerySummary(context.Background(), "one", "UID=123_A"); err != nil || !ok {
				t.Error("query failed")
			}
		}()
	}
	<-started
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.QuerySummary(cancelled, "one", "UID=123_A"); err == nil {
		t.Error("cancelled waiter did not return")
	}
	close(release)
	wg.Wait()
	if calls.Load() != 2 {
		t.Fatalf("duplicate requests: %d", calls.Load())
	}
	for _, pair := range [][2]string{{"one", "UID=123_A"}, {"two", "UID=123_A"}, {"one", "UID=123_B"}} {
		if _, _, err := c.QuerySummary(context.Background(), pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 6 {
		t.Fatalf("cache crossed configuration/credential: %d", calls.Load())
	}
	c.summaryMu.Lock()
	c.summaries[sha256.Sum256([]byte("one\x00UID=123_A"))].expires = time.Now().Add(-time.Second)
	c.summaryMu.Unlock()
	if _, _, err := c.QuerySummary(context.Background(), "one", "UID=123_A"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 8 {
		t.Fatal("expired metadata was reused")
	}
}
