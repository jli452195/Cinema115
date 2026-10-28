package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
)

func TestPortalSessionRenewalAndAbsoluteExpiry(t *testing.T) {
	auth := NewPortalAuth(nil, "admin", "password", "secret")
	u := model.PortalUser{ID: "admin", Role: "admin"}
	now := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		name          string
		started, last time.Duration
		renew, denied bool
	}{
		{"fresh login", time.Hour, time.Hour, false, false},
		{"near expiry", 6*24*time.Hour + time.Hour, 6*24*time.Hour + time.Hour, true, false},
		{"renewal capped at thirty days", 29 * 24 * time.Hour, 6*24*time.Hour + time.Hour, true, false},
		{"already at absolute expiry", 29 * 24 * time.Hour, 24 * time.Hour, false, false},
		{"expired cannot renew", 8 * 24 * time.Hour, 8 * 24 * time.Hour, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := now.Add(-tc.started)
			value, err := auth.signSession(u, started, now.Add(-tc.last))
			if err != nil {
				t.Fatal(err)
			}
			got, renewed, err := auth.Session(context.Background(), value)
			if tc.denied {
				if !errors.Is(err, ErrLogin) || renewed != "" {
					t.Fatal("expired login renewed", err)
				}
				return
			}
			if err != nil || got.ID != u.ID || (renewed != "") != tc.renew {
				t.Fatal("unexpected renewal result", err)
			}
			if renewed == "" {
				return
			}
			_, claims, err := auth.authenticate(context.Background(), renewed)
			if err != nil || !claims.IssuedAt.Equal(started) {
				t.Fatal("renewal reset password login time", err)
			}
			if claims.ExpiresAt.After(started.Add(portalSessionMaxLifetime)) || claims.ExpiresAt.After(time.Now().Add(PortalSessionLifetime)) || !claims.ExpiresAt.After(now) {
				t.Fatal("renewal exceeded session limits")
			}
		})
	}
	value, _, err := auth.LoginAdmin(context.Background(), "admin", "password")
	if err != nil {
		t.Fatal(err)
	}
	_, claims, err := auth.authenticate(context.Background(), value)
	if err != nil || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != PortalSessionLifetime {
		t.Fatal("login does not use shared session lifetime", err)
	}
	// Even a correctly signed token cannot bypass the password-login ceiling.
	claims.IssuedAt = jwt.NewNumericDate(now.Add(-31 * 24 * time.Hour))
	value, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(auth.secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.Session(context.Background(), value); !errors.Is(err, ErrLogin) {
		t.Fatal("absolute expiry bypassed", err)
	}
}

func TestPortalLookupFailureDoesNotInvalidateLogin(t *testing.T) {
	s, remote, _ := memberFixture(t)
	auth := NewPortalAuth(s.db, "admin", "password", "secret")
	auth.Members = s
	value, user, err := auth.Login(context.Background(), "reader", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"timeout", context.DeadlineExceeded, ErrAuthUnavailable},
		{"network", errors.New("private-url?token=secret"), ErrAuthUnavailable},
		{"upstream failure", &emby.HTTPError{StatusCode: 503}, ErrAuthUnavailable},
		{"API key rejected", &emby.HTTPError{StatusCode: 401}, ErrAuthUnavailable},
		{"user deleted", &emby.HTTPError{StatusCode: http.StatusNotFound}, ErrLogin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.NewEmby = func(string, string) memberEmbyClient {
				return failingPlaybackIdentity{memberTestRemote: remote, err: tc.err}
			}
			if _, renewed, err := auth.Session(context.Background(), value); !errors.Is(err, tc.want) || renewed != "" {
				t.Fatal("lookup failure misclassified or renewed", err)
			}
		})
	}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	if _, err := auth.Authenticate(context.Background(), value); err != nil {
		t.Fatal("transient error permanently invalidated login", err)
	}
	remote.users[user.EmbyID].Policy["IsDisabled"] = true
	if _, _, err := auth.Session(context.Background(), value); !errors.Is(err, ErrLogin) {
		t.Fatal("disabled user renewed", err)
	}
	remote.users[user.EmbyID].Policy["IsDisabled"] = false
	if err := s.db.RevokePortalSessions(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.Session(context.Background(), value); !errors.Is(err, ErrLogin) {
		t.Fatal("revoked user renewed", err)
	}
}
