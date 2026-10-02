package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlaybackSessionsReadOnlyAndBounded(t *testing.T) {
	body := `[{"UserId":"user","PlaySessionId":"play","NowPlayingItem":{"Id":"item"},"LastActivityDate":"2026-10-03T12:00:00Z"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/emby/Sessions" || r.URL.Query().Get("ActiveWithinSeconds") != "90" || r.Header.Get("X-Emby-Token") != "test-key" {
			t.Error("unexpected request")
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := New(server.URL, "test-key")
	sessions, err := client.PlaybackSessions(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].NowPlayingItem.ID != "item" {
		t.Fatal(sessions, err)
	}
	body = `null`
	if _, err := client.PlaybackSessions(context.Background()); err == nil {
		t.Fatal("null accepted")
	}
	body = `[]`
	if sessions, err := client.PlaybackSessions(context.Background()); err != nil || len(sessions) != 0 {
		t.Fatal(err)
	}
}
