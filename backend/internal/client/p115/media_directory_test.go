package p115

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestMediaDirectoryFreshAuthorizationSeedsPreflightOnly(t *testing.T) {
	c := newTestShareClient()
	calls := 0
	response := `{"state":true,"cid":"9"}`
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/files" || r.URL.Query().Get("cid") != "9" || r.Header.Get("Cookie") != "UID=22_A" {
			t.Fatal("wrong authorization request")
		}
		return instantResponse(200, []byte(response)), nil
	})
	ctx := context.Background()
	for i := 1; i <= 2; i++ {
		if err := c.ValidateMediaPlaybackDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
			t.Fatal(err)
		}
		if err := c.ValidatePlaybackDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
			t.Fatal(err)
		}
		if err := c.ValidatePlaybackRecoveryDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
			t.Fatal(err)
		}
		if calls != i {
			t.Fatalf("authorization must be fresh, preflight must reuse it: got %d want %d", calls, i)
		}
	}
	for _, bad := range []string{`{"state":false,"errno":990001}`, `{"state":true,"cid":"0"}`, `{"state":true}`, `{"cid":"9"}`, `not-json`} {
		response = bad
		if err := c.ValidateMediaPlaybackDirectory(ctx, "account", "9", "UID=22_A"); err == nil {
			t.Fatal("bad credentials/directory/response accepted", bad)
		}
		if len(c.playbackPreparation.directories) != 0 {
			t.Fatal("failed fresh check kept earlier success")
		}
	}
	before := calls
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.ValidateMediaPlaybackDirectory(ctx, "account", "9", "UID=22_A"); !errors.Is(err, context.Canceled) || calls != before {
		t.Fatal("canceled authorization reached upstream", err)
	}
}

func TestMediaDirectoryDoesNotQueueBehindScansOrDelaySHA1(t *testing.T) {
	c := newTestShareClient()
	c.RequestInterval = time.Hour
	// A background scan is in cooldown. Authorization must have its own lane.
	c.nextRequest = time.Now().Add(time.Hour)
	calls := 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == "/files" {
			return instantResponse(200, []byte(`{"state":true,"cid":"9"}`)), nil
		}
		if r.URL.Path == "/files/shasearch" {
			return instantResponse(200, []byte(`{"state":false,"error":"文件错误"}`)), nil
		}
		t.Fatal("unexpected endpoint", r.URL.Path)
		return nil, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.ValidateMediaPlaybackDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
		t.Fatal("authorization queued behind scan", err)
	}
	if _, err := c.FindPlaybackFileBySHA1Quick(ctx, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "UID=22_A"); err != nil {
		t.Fatal("authorization introduced metadata cooldown", err)
	}
	if calls != 2 {
		t.Fatal("unexpected extra requests", calls)
	}
}

func TestMediaDirectoryPreflightScopeIsolation(t *testing.T) {
	c := newTestShareClient()
	calls := 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return instantResponse(200, []byte(`{"state":true,"cid":"`+r.URL.Query().Get("cid")+`"}`)), nil
	})
	ctx := context.Background()
	if err := c.ValidateMediaPlaybackDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
		t.Fatal(err)
	}
	for _, changed := range [][3]string{{"other", "9", "UID=22_A"}, {"account", "8", "UID=22_A"}, {"account", "9", "UID=22_B"}, {"account", "9", "UID=33_A"}} {
		before := calls
		if err := c.ValidatePlaybackDirectory(ctx, changed[0], changed[1], changed[2]); err != nil {
			t.Fatal(err)
		}
		if calls != before+1 {
			t.Fatal("reused check across account/directory/credentials")
		}
	}
}
