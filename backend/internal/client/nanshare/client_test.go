package nanshare

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseFailuresAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"expired", `{"message":"secret-token"}`, 401, ErrAuth},
		{"login-rejected", `{"ok":false,"message":"secret-password"}`, 200, ErrAuth},
		{"html", `<html>secret-token</html>`, 200, ErrFormat},
		{"missing-data", `{"success":true,"data":{}}`, 200, ErrFormat},
		{"missing-envelope", `{}`, 200, ErrFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := New(server.URL, "")
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = client.Login(context.Background(), "user", "password")
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, _ := New(source.URL, "")
	if _, _, err := client.Login(context.Background(), "user", "password"); err == nil || called {
		t.Fatal("redirect forwarded credentials")
	}
}

func TestInvalidURLAndCancelledRequest(t *testing.T) {
	for _, value := range []string{"file:///x", "http://user:pass@host", "http://host/api", "http://host?token=secret", "http://host/#secret"} {
		if _, err := New(value, ""); !errors.Is(err, ErrInput) {
			t.Fatalf("accepted invalid address %s", value)
		}
	}
	client, err := New("http://127.0.0.1:1", "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Session(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
