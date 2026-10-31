package tguser

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestTelegramReadsContinueDuringBotExchange(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "temporary_connection"
		if shared {
			name = "relay_connection"
		}
		t.Run(name, func(t *testing.T) {
			client, transport := transportClient(t)
			client.config.APIID, client.config.APIHash = 1, strings.Repeat("a", 32)
			if err := client.sessionStore.Save("mock-session"); err != nil {
				t.Fatal(err)
			}
			client.searchTiming.operation = 10 * time.Second
			var picture bytes.Buffer
			if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 160, 160))); err != nil {
				t.Fatal(err)
			}
			sent, resume := make(chan struct{}), make(chan struct{})
			var starts, downloads atomic.Int32
			var dc int
			invoke := telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
				switch in.(type) {
				case *tg.MessagesSendMessageRequest:
					close(sent)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-resume:
					}
				case *tg.MessagesGetDialogsRequest:
					channel := &tg.Channel{ID: 100, Title: "channel", Broadcast: true, Photo: &tg.ChatPhoto{PhotoID: 123, DCID: dc}}
					channel.SetAccessHash(200)
					out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{
						Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 100}}},
						Chats:   []tg.ChatClass{channel},
					}
					return nil
				case *tg.MessagesSearchRequest:
					out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{}
					return nil
				case *tg.UploadGetFileRequest:
					downloads.Add(1)
					out.(*tg.UploadFileBox).File = &tg.UploadFile{Bytes: picture.Bytes()}
					return nil
				}
				return transport.invoke(ctx, in, out)
			})
			mock := telegram.NewClient(1, "test", telegram.Options{Middlewares: []telegram.Middleware{telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc { return invoke })}})
			dc = mock.Config().ThisDC + 1
			var closed atomic.Int32
			client.avatarDC = func(_ context.Context, _ *telegram.Client, id int) (telegram.CloseInvoker, error) {
				if id != dc {
					t.Errorf("unexpected avatar data center %d", id)
				}
				return avatarTestInvoker{invoke, &closed}, nil
			}
			client.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
				starts.Add(1)
				return operation(ctx, mock)
			}
			if shared {
				ctx, cancel := context.WithCancel(context.Background())
				connection := &relayConnection{client: mock, ctx: ctx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), connected: true}
				close(connection.ready)
				client.relayConnection = connection
				go func() { <-ctx.Done(); close(connection.done) }()
				defer func() { cancel(); <-connection.done }()
			}
			done := make(chan error, 1)
			go func() { _, err := client.Search(context.Background(), "concurrency", false); done <- err }()
			select {
			case <-sent:
			case <-time.After(3 * time.Second):
				t.Fatal("Bot exchange did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			page, err := client.ListChannels(ctx, "")
			if err != nil || len(page.Channels) != 1 {
				t.Fatalf("channel picker was blocked by Bot exchange: %+v, %v", page, err)
			}
			if err := client.SaveChannels(ctx, page.SelectionToken, []string{"100"}); err != nil {
				t.Fatalf("local channel save was blocked by Bot exchange: %v", err)
			}
			if _, err := client.SearchChannels(ctx, "title", "", false); err != nil {
				t.Fatalf("channel search was blocked by Bot exchange: %v", err)
			}
			if err := client.Check(ctx); err != nil {
				t.Fatalf("connection check was blocked by Bot exchange: %v", err)
			}
			for i := 0; i < 2; i++ {
				data, _, err := client.RelayAvatar(ctx, "100", "123")
				if err != nil || !bytes.Equal(data, picture.Bytes()) {
					t.Fatalf("avatar was blocked by Bot exchange: %v", err)
				}
			}
			if downloads.Load() != 1 || !shared && starts.Load() != 1 || shared && starts.Load() != 0 {
				t.Fatalf("ordinary reads duplicated the session or bypassed avatar cache: starts=%d, downloads=%d", starts.Load(), downloads.Load())
			}
			select {
			case err := <-done:
				t.Fatalf("Bot exchange ended before being released: %v", err)
			default:
			}
			close(resume)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
