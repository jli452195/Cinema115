package storage

import (
	"context"
	"testing"

	"strm115/backend/internal/model"
)

func TestPlaybackStateTransactionRollback(t *testing.T) {
	for _, method := range []string{"handoff", "changes"} {
		t.Run(method, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			if err := s.SavePlaybackState(ctx, model.UserPlaybackDocument{Generation: 1}, model.PlaybackPoolDocument{Generation: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_pool BEFORE UPDATE ON playback_pool BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
				t.Fatal(err)
			}
			u := model.UserPlaybackDocument{Generation: 2}
			p := model.PlaybackPoolDocument{Generation: 2}
			var err error
			if method == "handoff" {
				err = s.SavePlaybackState(ctx, u, p)
			} else {
				err = s.SavePlaybackStateChanges(ctx, &u, &p)
			}
			if err == nil {
				t.Fatal("expected failure")
			}
			u, err = s.UserPlayback(ctx)
			if err != nil || u.Generation != 1 {
				t.Fatal("partial user commit", err)
			}
			p, err = s.PlaybackPool(ctx)
			if err != nil || p.Generation != 1 {
				t.Fatal("partial pool commit", err)
			}
		})
	}
}

func TestPlaybackStateChangesSavesSelectedDocuments(t *testing.T) {
	for _, tc := range []struct {
		name string
		user bool
		pool bool
	}{
		{name: "user_only", user: true},
		{name: "pool_only", pool: true},
		{name: "both", user: true, pool: true},
		{name: "neither"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			if err := s.SavePlaybackState(ctx, model.UserPlaybackDocument{Generation: 1}, model.PlaybackPoolDocument{Generation: 1}); err != nil {
				t.Fatal(err)
			}
			var user *model.UserPlaybackDocument
			var pool *model.PlaybackPoolDocument
			wantUser, wantPool := uint64(1), uint64(1)
			if tc.user {
				user = &model.UserPlaybackDocument{Generation: 2}
				wantUser = 2
			} else if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_user BEFORE UPDATE ON user_playback_leases BEGIN SELECT RAISE(ABORT,'unexpected user write'); END`); err != nil {
				t.Fatal(err)
			}
			if tc.pool {
				pool = &model.PlaybackPoolDocument{Generation: 2}
				wantPool = 2
			} else if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_pool BEFORE UPDATE ON playback_pool BEGIN SELECT RAISE(ABORT,'unexpected pool write'); END`); err != nil {
				t.Fatal(err)
			}
			if !tc.user && !tc.pool {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if err := s.SavePlaybackStateChanges(ctx, user, pool); err != nil {
				t.Fatal(err)
			}
			u, err := s.UserPlayback(context.Background())
			if err != nil || u.Generation != wantUser {
				t.Fatalf("user generation = %d, want %d: %v", u.Generation, wantUser, err)
			}
			p, err := s.PlaybackPool(context.Background())
			if err != nil || p.Generation != wantPool {
				t.Fatalf("pool generation = %d, want %d: %v", p.Generation, wantPool, err)
			}
		})
	}
}
