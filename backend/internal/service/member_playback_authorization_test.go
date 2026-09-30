package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"strm115/backend/internal/client/emby"
)

type failingPlaybackIdentity struct {
	*memberTestRemote
	err    error
	cancel context.CancelFunc
}

func (f failingPlaybackIdentity) ManagedUser(context.Context, string) (emby.ManagedUser, error) {
	if f.cancel != nil {
		f.cancel()
	}
	return emby.ManagedUser{}, f.err
}

func TestPlaybackAuthorizationPreservesLookupFailures(t *testing.T) {
	s, remote, id := memberFixture(t)
	memberScan(t, s, id)
	original := s.record(id)
	for _, tc := range []struct {
		name        string
		cause, want error
		cancel      bool
	}{
		{"canceled during lookup", errors.New("transport interrupted"), context.Canceled, true},
		{"wrapped cancellation", context.Canceled, context.Canceled, false},
		{"timeout", context.DeadlineExceeded, context.DeadlineExceeded, false},
		{"upstream unavailable", errors.New("private-url?token=secret"), ErrPlaybackIdentityUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := failingPlaybackIdentity{memberTestRemote: remote, err: tc.cause}
			if tc.cancel {
				f.cancel = cancel
			}
			s.NewEmby = func(string, string) memberEmbyClient { return f }
			_, err := s.Authorize(ctx, original.EmbyURL, original.EmbyID)
			if !errors.Is(err, tc.want) || errors.Is(err, ErrLogin) {
				t.Fatal("lookup misclassified", err)
			}
			if !reflect.DeepEqual(original, s.record(id)) {
				t.Fatal("lookup failure changed binding")
			}
		})
	}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	u := remote.users[original.EmbyID]
	u.Policy["IsDisabled"] = true
	remote.users[original.EmbyID] = u
	if _, err := s.Authorize(context.Background(), original.EmbyURL, original.EmbyID); !errors.Is(err, ErrLogin) {
		t.Fatal("disabled user accepted", err)
	}
	u.Policy["IsDisabled"] = false
	remote.users[original.EmbyID] = u
	if _, err := s.Authorize(context.Background(), original.EmbyURL, original.EmbyID); err != nil {
		t.Fatal("subsequent healthy request rejected", err)
	}
}
