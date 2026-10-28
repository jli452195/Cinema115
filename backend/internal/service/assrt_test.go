package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"strm115/backend/internal/client/assrt"
)

type assrtTransport func(*http.Request) (*http.Response, error)

func (f assrtTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAssrtConfigCacheAndRollingLimit(t *testing.T) {
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "subtitles.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewAssrtService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var calls atomic.Int32
	s.newClient = func(token string) *assrt.Client {
		c := assrt.New(token)
		c.HTTP.Transport = assrtTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			body := `{"status":0,"user":{"quota":20}}`
			if r.Header.Get("Authorization") == "Bearer "+strings.Repeat("b", 32) {
				body = `{"status":20001}`
			}
			if strings.HasSuffix(r.URL.Path, "/search") {
				body = `{"status":0,"sub":{"subs":[{"id":1,"native_name":"Show"}]}}`
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		return c
	}
	if result, err := s.Search(ctx, "Show", 0); err != nil || result.Configured || calls.Load() != 0 {
		t.Fatal(result, err)
	}
	if err := s.Configure(ctx, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(ctx, strings.Repeat("b", 32)); !errors.Is(err, assrt.ErrToken) || store.Value() != strings.Repeat("a", 32) {
		t.Fatal("failed config replaced token", err)
	}
	before := calls.Load()
	for i := 0; i < 3; i++ {
		if !s.View().Configured {
			t.Fatal("missing config")
		}
	}
	if calls.Load() != before {
		t.Fatal("local view called API")
	}
	var group errgroup.Group
	for i := 0; i < 8; i++ {
		group.Go(func() error { _, err := s.Search(ctx, "Show", 0); return err })
	}
	if err := group.Wait(); err != nil || calls.Load() != before+1 {
		t.Fatal("concurrent duplicate search", calls.Load(), err)
	}
	before = calls.Load()
	for i := 0; i < 20; i++ {
		s.requests = append(s.requests, time.Now())
	}
	if _, err := s.Search(ctx, "Another", 0); !errors.Is(err, assrt.ErrRateLimited) || calls.Load() != before {
		t.Fatal("quota not bounded", err)
	}
	if _, err := s.Search(ctx, "Show", 0); err != nil {
		t.Fatal("cached result consumed quota", err)
	}
	if err := s.Clear(ctx); err != nil || s.View().Configured || store.Value() != "" || len(s.cache) != 0 {
		t.Fatal("clear left state", err)
	}
}
