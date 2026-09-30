package p115

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRecoveryDirectoryReusesOnlyRecentRealChecks(t *testing.T) {
	c := newTestShareClient()
	calls := 0
	missing := false
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/files" {
			t.Fatal("unexpected request")
		}
		cid := r.URL.Query().Get("cid")
		if missing {
			cid = "0"
		}
		return instantResponse(200, []byte(`{"state":true,"cid":"`+cid+`","data":[],"count":0}`)), nil
	})
	ctx := context.Background()
	key := playbackPreparationKey("account\x009", "UID=22_A")
	if err := c.ValidatePlaybackRecoveryDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
		t.Fatal(err)
	}
	validated := c.playbackPreparation.directories[key]
	var observed PlaybackTiming
	ctx = WithPlaybackTiming(ctx, func(v PlaybackTiming) { observed = v })
	for i := 0; i < 3; i++ {
		if err := c.ValidatePlaybackRecoveryDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || observed.Requests != 0 || c.playbackPreparation.directories[key] != validated {
		t.Fatal("cache hit queried 115 or extended freshness")
	}
	// A normal 10-minute cache entry older than 30 seconds is not fresh enough
	// for recovery. A root fallback response must discard that old success.
	c.playbackPreparation.directories[key] = time.Now().Add(playbackDirectoryTTL - playbackRecoveryDirectoryFreshness - time.Second)
	missing = true
	if err := c.ValidatePlaybackRecoveryDirectory(ctx, "account", "9", "UID=22_A"); err == nil {
		t.Fatal("deleted directory accepted")
	}
	if _, ok := c.playbackPreparation.directories[key]; ok {
		t.Fatal("failed validation left stale success")
	}
	if err := c.ValidatePlaybackDirectory(ctx, "account", "9", "UID=22_A"); err == nil {
		t.Fatal("ordinary upload bypassed failed validation")
	}
	if calls != 3 {
		t.Fatal("failed validations cached", calls)
	}
	missing = false
	if err := c.ValidatePlaybackRecoveryDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
		t.Fatal(err)
	}
	for _, v := range [][3]string{{"account", "9", "UID=22_B"}, {"other", "9", "UID=22_A"}, {"account", "8", "UID=22_A"}, {"account", "9", "UID=33_A"}} {
		if err := c.ValidatePlaybackRecoveryDirectory(ctx, v[0], v[1], v[2]); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 8 {
		t.Fatal("credentials or directory shared validation", calls)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.ValidatePlaybackRecoveryDirectory(cancelled, "account", "9", "UID=22_A"); !errors.Is(err, context.Canceled) {
		t.Fatal("cached check ignored cancellation")
	}
	if calls != 8 {
		t.Fatal("cancelled request reached upstream")
	}
}

func TestRecycledDirectoryUploadRejectionInvalidatesFreshCache(t *testing.T) {
	c := newTestShareClient()
	checks, submits := 0, 0
	deleted := false
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/files":
			checks++
			if deleted {
				return instantResponse(200, []byte(`{"state":true,"cid":"0"}`)), nil
			}
			return instantResponse(200, []byte(`{"state":true,"cid":"9"}`)), nil
		case "/files/uploadinfo":
			return instantResponse(200, []byte(`{"user_id":22,"userkey":"test-key"}`)), nil
		case "/4.0/initupload.php":
			submits++
			return instantResponse(200, encryptedInstantResponse(t, `{"status":0,"statuscode":10014}`)), nil
		default:
			t.Fatal("unexpected request")
			return nil, nil
		}
	})
	ctx := context.Background()
	ticket, err := c.PreparePlaybackAccount(ctx, "account", "UID=22_A")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidatePlaybackRecoveryDirectory(ctx, "account", "9", "UID=22_A"); err != nil {
		t.Fatal(err)
	}
	deleted = true
	result, err := c.UploadPlayback(ctx, testInstantInfo(), "account", "9", "UID=22_A", ticket, nil)
	if err == nil || result != nil || submits != 1 || checks != 1 {
		t.Fatal("rejected upload retried or reported success", err)
	}
	if len(c.playbackPreparation.directories) != 0 || len(c.playbackPreparation.accounts) != 0 {
		t.Fatal("upload rejection kept preflight cache")
	}
	if err := c.ValidatePlaybackRecoveryDirectory(ctx, "account", "9", "UID=22_A"); err == nil || checks != 2 {
		t.Fatal("did not revalidate after rejection")
	}
}
