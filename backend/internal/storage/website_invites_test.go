package storage

import (
	"context"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
	"sync"
	"testing"
	"time"
)

func TestWebsiteInviteAtomicClaimAndRollback(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	invites := []model.WebsiteInvite{
		{ID: "one", CodeHash: "one", CreatedAt: now, ExpiresAt: now + 3600},
		{ID: "expired", CodeHash: "expired", CreatedAt: now - 3600, ExpiresAt: now - 1},
		{ID: "revoked", CodeHash: "revoked", CreatedAt: now, ExpiresAt: now + 3600},
	}
	if err := s.CreateWebsiteInvites(ctx, invites); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeWebsiteInvite(ctx, "revoked"); err != nil {
		t.Fatal(err)
	}
	user := model.WebsiteUser{ID: "first", Username: "taken", PasswordHash: "hash", CreatedAt: "date"}
	if err := s.CreateWebsiteUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	user.ID = "duplicate"
	if err := s.RegisterWebsiteUser(ctx, "one", user); !errors.Is(err, ErrWebsiteUserExists) {
		t.Fatal(err)
	}
	if err := s.CheckWebsiteInvite(ctx, "one"); err != nil {
		t.Fatal("username conflict consumed invite", err)
	}
	for _, code := range []string{"expired", "revoked", "missing"} {
		if err := s.RegisterWebsiteUser(ctx, code, user); !errors.Is(err, ErrWebsiteInviteInvalid) {
			t.Fatal(code, err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("reader%d", i)
			results <- s.RegisterWebsiteUser(ctx, "one", model.WebsiteUser{ID: name, Username: name, PasswordHash: "hash", CreatedAt: "date"})
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrWebsiteInviteInvalid) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("concurrent successful claims", success)
	}
	records, err := s.WebsiteInvites(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.ID == "one" && (record.UsedAt == 0 || record.UsedBy == "" || record.Username == "") {
			t.Fatal("missing registration association", record)
		}
	}
	users, err := s.WebsiteUsers(ctx, 0)
	if err != nil || len(users) != 2 {
		t.Fatal("unexpected users", users, err)
	}
}
