package storage

import (
	"context"
	"testing"

	"strm115/backend/internal/model"
)

func TestPlaybackStateTransactionRollback(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if err := s.SavePlaybackState(ctx, model.UserPlaybackDocument{Generation: 1}, model.PlaybackPoolDocument{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_pool BEFORE UPDATE ON playback_pool BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlaybackState(ctx, model.UserPlaybackDocument{Generation: 2}, model.PlaybackPoolDocument{Generation: 2}); err == nil {
		t.Fatal("expected failure")
	}
	u, err := s.UserPlayback(ctx)
	if err != nil || u.Generation != 1 {
		t.Fatal("partial user commit", err)
	}
	p, err := s.PlaybackPool(ctx)
	if err != nil || p.Generation != 1 {
		t.Fatal("partial pool commit", err)
	}
}
