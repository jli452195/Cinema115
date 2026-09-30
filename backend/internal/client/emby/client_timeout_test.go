package emby

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type identityTimeoutTransport struct{}

func (identityTimeoutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("private upstream URL: %w", context.DeadlineExceeded)
}

func TestManagedUserPreservesHTTPTimeoutWithoutLeakingURL(t *testing.T) {
	c := New("http://emby.invalid", "private-api-key")
	c.HTTP.Transport = identityTimeoutTransport{}
	_, err := c.ManagedUser(context.Background(), strings.Repeat("a", 32))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout identity lost", err)
	}
	if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "emby.invalid") {
		t.Fatal("transport details leaked")
	}
}
