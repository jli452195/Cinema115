package service

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"strm115/backend/internal/client/tguser"
)

type relaySelectionMissing struct{ *relayTransportFake }

func (relaySelectionMissing) RunRelay(context.Context, tguser.RelayStateStore, func(context.Context, int64, *tg.Message) error) error {
	return &tguser.OperationError{Code: "selection_required", Message: "转发频道资料缺失，请编辑规则重新选择来源和目标"}
}

func TestTelegramRelaySelectionStatusRecoversAfterReconnect(t *testing.T) {
	relay, _, transport := relayFixture(t)
	saveRelayRule(t, relay, relayRuleInput("channel:100", "user:200"))
	relay.transport = relaySelectionMissing{transport}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relay.watch(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		view, err := relay.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status.Error != "" {
			if view.Status.Error != "转发频道资料缺失，请编辑规则重新选择来源和目标" || view.Status.Connected {
				t.Fatalf("incorrect missing-selection status: %+v", view.Status)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("watcher did not report missing selection")
		case <-tick.C:
		}
	}
	if err := relay.receive(ctx, 42, nil); err != nil {
		t.Fatal(err)
	}
	view, err := relay.List(ctx)
	if err != nil || view.Status.Error != "" {
		t.Fatalf("reconnection kept obsolete error: %+v, %v", view.Status, err)
	}
}

func TestTelegramRelayReselectingSamePeersRefreshesSavedMetadata(t *testing.T) {
	relay, _, transport := relayFixture(t)
	input := relayRuleInput("channel:100", "user:200")
	rule := saveRelayRule(t, relay, input)
	input.ID = rule.ID
	input.SourceSelectionToken, input.TargetSelectionToken = "source-picked", "target-picked"
	updated := saveRelayRule(t, relay, input)
	if transport.prepareCalls != 2 || updated.ID != rule.ID || updated.OwnerID != rule.OwnerID || !updated.StartedAt.Equal(rule.StartedAt) {
		t.Fatalf("reselecting existing peers did not refresh metadata on the original rule: %+v", updated)
	}
}
