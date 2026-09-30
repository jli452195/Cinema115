package service

import (
	"context"
	"testing"
)

func TestPrewarmDoesNotQueueForPlaybackGate(t *testing.T) {
	s := &PlaybackCache{}
	ctx := PlaybackPrewarmContext(context.Background())
	release, err := s.acquirePlayback(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.acquirePlayback(ctx, ""); err == nil {
		t.Fatal("prewarm entered busy gate")
	}
	release()
	s.foregroundWaiting.Add(1)
	if _, err := s.acquirePlayback(ctx, ""); err == nil {
		t.Fatal("prewarm passed foreground waiter")
	}
	s.foregroundWaiting.Add(-1)
	release, err = s.acquirePlayback(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
