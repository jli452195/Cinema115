package service

import (
	"context"
	"strm115/backend/internal/client/emby"
	"time"
)

// Creation waits briefly for an in-flight member operation instead of reporting
// a misleading permanent lock. Cancellation never starts an upstream write.
func (s *MemberPlaybackService) lockCreation(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-wait.Done():
		return memberError("账号操作繁忙，请稍后使用原邀请或原任务重试")
	case s.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.unlock()
			return err
		}
		return nil
	}
}

// Only reads are retried. A lost creation response must never trigger another
// create, and an unrelated account with the requested final name is not evidence.
func confirmProvisioning(ctx context.Context, check func(context.Context) bool) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		if check(ctx) {
			return true
		}
		if attempt == 3 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return false
}

func confirmReservedUser(ctx context.Context, client memberEmbyClient, name string) string {
	var id string
	confirmProvisioning(ctx, func(ctx context.Context) bool {
		users, err := client.Users(ctx)
		if err != nil {
			return false
		}
		id = ""
		for _, u := range users {
			if u.Name == name {
				if id != "" || u.Policy.IsAdministrator {
					id = ""
					return false
				}
				id = u.ID
			}
		}
		return id != ""
	})
	return id
}

func confirmProvisionedUser(ctx context.Context, client memberEmbyClient, id string, matches func(emby.ManagedUser) bool) bool {
	return confirmProvisioning(ctx, func(ctx context.Context) bool {
		u, err := client.ManagedUser(ctx, id)
		return err == nil && u.ID == id && u.Policy["IsAdministrator"] != true && matches(u)
	})
}

func saveProvisioningPolicy(ctx context.Context, client memberEmbyClient, id string, policy map[string]any) bool {
	// An unsuccessful write response can still mean that Emby committed it.
	writeErr := client.SetManagedPolicy(ctx, id, policy)
	confirmed := confirmProvisionedUser(ctx, client, id, func(u emby.ManagedUser) bool {
		return memberPolicyMatchesExpected(u.Policy, policy) && childRatingMatches(u.Policy, policy)
	})
	return confirmed && (writeErr == nil || ctx.Err() == nil)
}

func renameProvisionedUser(ctx context.Context, client memberEmbyClient, id, name string) bool {
	writeErr := client.RenameManagedUser(ctx, id, name)
	confirmed := confirmProvisionedUser(ctx, client, id, func(u emby.ManagedUser) bool { return u.Name == name })
	return confirmed && (writeErr == nil || ctx.Err() == nil)
}

// Bound cleanup separately when the caller timed out after remote activation.
// Local provisioning remains denied even if Emby cannot confirm the rollback.
func disableUncommittedUser(ctx context.Context, client memberEmbyClient, id string, policy map[string]any) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	denied := playbackRestrictions(policy)
	denied["IsDisabled"], denied["EnableMediaPlayback"] = true, false
	// The durable pending record and original request remain the recovery path.
	_ = client.SetManagedPolicy(cleanup, id, denied)
}
