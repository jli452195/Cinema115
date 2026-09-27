package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserAvatarKeepsCredentialsServerSideAndBoundsResponse(t *testing.T) {
	body := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Emby-Token") != "private-key" || strings.Contains(r.URL.String(), "private-key") {
			t.Error("wrong credential transport")
		}
		w.Write([]byte(body))
	}))
	defer server.Close()
	client := New(server.URL, "private-key")
	id := strings.Repeat("a", 32)
	if data, kind, err := client.UserImage(context.Background(), id); err != nil || kind != "image/png" || len(data) != len(body) {
		t.Fatal("avatar failed", err)
	}
	if _, _, err := client.UserImage(context.Background(), "../Users"); err == nil || calls != 1 {
		t.Fatal("invalid user ID reached upstream")
	}
	body = "<svg xmlns='http://www.w3.org/2000/svg'></svg>"
	if _, _, err := client.UserImage(context.Background(), id); err == nil {
		t.Fatal("active image accepted")
	}
	body = strings.Repeat("x", (1<<20)+1)
	if _, _, err := client.UserImage(context.Background(), id); err == nil {
		t.Fatal("oversize image accepted")
	}
}
