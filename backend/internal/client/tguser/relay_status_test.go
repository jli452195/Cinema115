package tguser

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestRelayMissingSelectionsDoNotInvalidateLogin(t *testing.T) {
	for _, owner := range []int64{0, 42} {
		t.Run(strconv.FormatInt(owner, 10), func(t *testing.T) {
			client, _ := relayClient(t)
			store := seedRelay(client)
			client.relayPeers = relaySettings{OwnerID: owner}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := client.RunRelay(ctx, store, func(context.Context, int64, *tg.Message) error { return nil })
			var detail *OperationError
			if !errors.As(err, &detail) || detail.Code != "selection_required" || errors.Is(err, ErrLogin) {
				t.Fatalf("missing selections reported as authentication failure: %v", err)
			}
			if !client.View().Authorized || client.RelayConnected() {
				t.Fatal("missing selections changed account authorization or started listening")
			}
		})
	}
}

func TestRelayUnauthorizedStillRequiresLogin(t *testing.T) {
	client, transport := relayClient(t)
	store := seedRelay(client)
	transport.user.Store(0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.RunRelay(ctx, store, func(context.Context, int64, *tg.Message) error { return nil })
	if !errors.Is(err, ErrLogin) || client.View().Authorized || client.RelayConnected() {
		t.Fatalf("unauthorized relay was not stopped: %v", err)
	}
}
