package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type playbackAccountPreparerFunc func(context.Context, string, string) (*p115.PlaybackUploadTicket, error)

func (f playbackAccountPreparerFunc) PreparePlaybackAccount(ctx context.Context, scope, cookie string) (*p115.PlaybackUploadTicket, error) {
	return f(ctx, scope, cookie)
}

func TestPlaybackWritePreparationOverlap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	directoryStarted, ticketStarted := make(chan struct{}), make(chan struct{})
	ticket := &p115.PlaybackUploadTicket{}
	prepare := playbackAccountPreparerFunc(func(ctx context.Context, scope, cookie string) (*p115.PlaybackUploadTicket, error) {
		if scope != "fixed-account" || cookie != "fixed-cookie" {
			return nil, errors.New("target changed")
		}
		close(ticketStarted)
		select {
		case <-directoryStarted:
			return ticket, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	result, err := preparePlaybackWrite(ctx, prepare, "fixed-account", "fixed-cookie", func(ctx context.Context) error {
		close(directoryStarted)
		select {
		case <-ticketStarted:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil || result.ticket != ticket {
		t.Fatal("preflight did not overlap or changed ticket", err)
	}
}

func TestPlaybackWritePreparationCancelsAndJoins(t *testing.T) {
	for _, mode := range []string{"directory-fails", "ticket-fails", "parent-canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			directoryStarted, ticketStarted, ticketFinished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			failure := errors.New("preflight rejected")
			prepare := playbackAccountPreparerFunc(func(ctx context.Context, _, _ string) (*p115.PlaybackUploadTicket, error) {
				defer close(ticketFinished)
				close(ticketStarted)
				select {
				case <-directoryStarted:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if mode == "ticket-fails" {
					return nil, failure
				}
				<-ctx.Done()
				return nil, ctx.Err()
			})
			_, err := preparePlaybackWrite(ctx, prepare, "fixed", "cookie", func(ctx context.Context) error {
				close(directoryStarted)
				select {
				case <-ticketStarted:
				case <-ctx.Done():
					return ctx.Err()
				}
				if mode == "directory-fails" {
					return failure
				}
				if mode == "parent-canceled" {
					cancel()
				}
				<-ctx.Done()
				return ctx.Err()
			})
			want := failure
			if mode == "parent-canceled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("lost failure cause: %v", err)
			}
			select {
			case <-ticketFinished:
			default:
				t.Fatal("returned before ticket worker finished")
			}
		})
	}
}
