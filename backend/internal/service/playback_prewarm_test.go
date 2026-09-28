package service

import (
	"context"
	"testing"
)

func TestPrewarmDoesNotQueueForPlaybackGate(t *testing.T) {
	s := &PlaybackCache{gate: make(chan struct{}, 1)}
	ctx := PlaybackPrewarmContext(context.Background())
	if err := s.enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.enter(ctx); err == nil {
		t.Fatal("prewarm entered busy gate")
	}
	<-s.gate
	s.foregroundWaiting.Add(1)
	if err := s.enter(ctx); err == nil {
		t.Fatal("prewarm passed foreground waiter")
	}
	s.foregroundWaiting.Add(-1)
	if err := s.enter(ctx); err != nil {
		t.Fatal(err)
	}
	<-s.gate
}
