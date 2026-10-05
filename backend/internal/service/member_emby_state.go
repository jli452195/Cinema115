package service

import (
	"context"
	"strm115/backend/internal/client/emby"
)

// MemberEmbyState describes an observation, independently of drive health and
// persistent business identity. Empty means no remote observation was made.
type MemberEmbyState string

const (
	MemberEmbyActive      MemberEmbyState = "active"
	MemberEmbyDisabled    MemberEmbyState = "disabled"
	MemberEmbyMissing     MemberEmbyState = "missing"
	MemberEmbyDeleted     MemberEmbyState = "deleted"
	MemberEmbyOtherServer MemberEmbyState = "other_server"
)

func missingMemberEmbyState(ctx context.Context, client memberEmbyClient, id string, users map[string]emby.User) MemberEmbyState {
	if u, ok := users[id]; ok {
		if u.Policy.IsDisabled {
			return MemberEmbyDisabled
		}
		return MemberEmbyActive
	}
	verifier, ok := client.(interface {
		ManagedUserAbsent(context.Context, string) (bool, error)
	})
	if !ok || id == "" || ctx.Err() != nil {
		return MemberEmbyMissing
	}
	absent, err := verifier.ManagedUserAbsent(ctx, id)
	if err == nil && absent {
		return MemberEmbyDeleted
	}
	return MemberEmbyMissing
}
