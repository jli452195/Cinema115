package subshare

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubTokenErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		remaining string
		want      error
	}{
		{"expired", http.StatusUnauthorized, "", ErrToken},
		{"quota", http.StatusForbidden, "0", ErrRateLimited},
		{"permission", http.StatusForbidden, "20", ErrForbidden},
		{"throttled", http.StatusTooManyRequests, "", ErrRateLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", tc.remaining)
				w.WriteHeader(tc.status)
			}))
			defer upstream.Close()
			client := New("")
			client.API = upstream.URL + "/api"
			if err := client.ValidateToken(context.Background(), "github_pat_test_only_1234567890"); !errors.Is(err, tc.want) {
				t.Fatalf("错误类型不正确：%v", err)
			}
		})
	}
}
