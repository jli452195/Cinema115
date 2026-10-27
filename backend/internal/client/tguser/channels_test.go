package tguser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type channelTransport struct {
	userID    int64
	list      func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error)
	calls     int
	logoutErr error
}

func channelClient(t *testing.T) (*Client, *channelTransport) {
	t.Helper()
	c := testClient(t)
	if err := c.Save(Config{APIID: 123, APIHash: strings.Repeat("a", 32)}); err != nil {
		t.Fatal(err)
	}
	if err := c.sessionStore.Save("session"); err != nil {
		t.Fatal(err)
	}
	f := &channelTransport{userID: 1}
	c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
		client := telegram.NewClient(1, "test", telegram.Options{Middlewares: []telegram.Middleware{telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc {
			return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				switch request := in.(type) {
				case *tg.UsersGetUsersRequest:
					out.(*tg.UserClassVector).Elems = []tg.UserClass{&tg.User{ID: f.userID, FirstName: "test"}}
				case *tg.MessagesGetDialogsRequest:
					f.calls++
					response, err := f.list(request)
					if err != nil {
						return err
					}
					out.(*tg.MessagesDialogsBox).Dialogs = response
				case *tg.AuthLogOutRequest:
					return f.logoutErr
				default:
					return fmt.Errorf("unexpected request %T", request)
				}
				return nil
			}
		})}})
		return operation(ctx, client)
	}
	return c, f
}

func broadcast(id int64) *tg.Channel {
	c := &tg.Channel{ID: id, Title: fmt.Sprintf("频道 %d", id), Broadcast: true}
	c.SetAccessHash(id + 987654)
	return c
}

func fullChannels(channels ...*tg.Channel) *tg.MessagesDialogs {
	r := &tg.MessagesDialogs{}
	for _, c := range channels {
		r.Dialogs = append(r.Dialogs, &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: c.ID}})
		r.Chats = append(r.Chats, c)
	}
	return r
}

func TestChannelSelectionPersistenceAndAccountIsolation(t *testing.T) {
	c, f := channelClient(t)
	if _, err := c.Search(context.Background(), "test", false); !errors.Is(err, ErrBotConfig) {
		t.Fatal("channel-only account should explain missing Bot", err)
	}
	f.list = func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		return fullChannels(broadcast(f.userID * 100)), nil
	}
	if got := c.ChannelConfig(); len(got.Channels) != 0 || f.calls != 0 {
		t.Fatal("local view started a Telegram request")
	}
	page, err := c.ListChannels(context.Background(), "")
	if err != nil || len(page.Channels) != 1 || page.SelectionToken == "" || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if page.Config == nil || !page.Config.Account.Authorized || len(page.Config.Channels) != 0 {
		t.Fatal("list did not return refreshed account selection")
	}
	for _, ids := range [][]string{{"999"}, {"100", "100"}} {
		if err := c.SaveChannels(page.SelectionToken, ids); !errors.Is(err, ErrInput) {
			t.Fatalf("accepted invalid selection %v: %v", ids, err)
		}
	}
	if err := c.SaveChannels(page.SelectionToken, []string{"100"}); err != nil {
		t.Fatal(err)
	}
	// A Bot configuration update must not silently erase saved channels.
	if err := c.Save(Config{APIID: 123, Bot: testBotUsername}); err != nil {
		t.Fatal(err)
	}
	restored, err := New(c.configStore, c.sessionStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	view := restored.ChannelConfig()
	if len(view.Channels) != 1 || view.Channels[0].ID != "100" {
		t.Fatalf("selection lost on restart: %+v", view)
	}
	data, _ := json.Marshal(view)
	if strings.Contains(string(data), "accessHash") || strings.Contains(string(data), strings.Repeat("a", 32)) {
		t.Fatal("credentials leaked in config view")
	}
	// The same channel owner can reopen and save without loading every page.
	page, err = c.ListChannels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	oldToken := page.SelectionToken
	page, err = c.ListChannels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SaveChannels(oldToken, []string{"100"}); !errors.Is(err, ErrChannelSelection) {
		t.Fatal("another picker did not invalidate old token", err)
	}
	f.userID = 2
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.ChannelConfig().Channels) != 0 {
		t.Fatal("another account inherited channels")
	}
	if err := c.SaveChannels(page.SelectionToken, []string{"100"}); !errors.Is(err, ErrChannelSelection) {
		t.Fatal("old selection token survived account switch", err)
	}
	page, err = c.ListChannels(context.Background(), "")
	if err != nil || page.Channels[0].ID != "200" {
		t.Fatal("new account list failed", err)
	}
	if err := c.SaveChannels(page.SelectionToken, []string{"100"}); !errors.Is(err, ErrInput) {
		t.Fatal("old account peer accepted", err)
	}
	if err := c.SaveChannels(page.SelectionToken, []string{}); err != nil {
		t.Fatal("cannot clear selection", err)
	}
}

func TestChannelLogoutRetainsSelectionOnFailure(t *testing.T) {
	c, f := channelClient(t)
	f.list = func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		return fullChannels(broadcast(1)), nil
	}
	page, err := c.ListChannels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SaveChannels(page.SelectionToken, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	f.logoutErr = errors.New("connection failed")
	if err := c.Disconnect(context.Background()); err == nil || len(c.ChannelConfig().Channels) != 1 {
		t.Fatal("failed logout erased selection")
	}
	f.logoutErr = nil
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.View().HasSession || c.View().Authorized || len(c.ChannelConfig().Channels) != 0 || c.channelCatalog != nil {
		t.Fatal("logout left account state")
	}
	if err := c.SaveChannels(page.SelectionToken, []string{"1"}); !errors.Is(err, ErrLogin) {
		t.Fatal("logout allowed stale save", err)
	}
}

func TestChannelPaginationFiltersAndRetry(t *testing.T) {
	c, f := channelClient(t)
	failed := false
	f.list = func(r *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		folder, ok := r.GetFolderID()
		if !ok {
			t.Fatal("folder must be explicit")
		}
		if folder == 0 && r.OffsetID == 0 {
			group := broadcast(2)
			group.Broadcast = false
			group.Megagroup = true
			left := broadcast(3)
			left.Left = true
			min := broadcast(4)
			min.Min = true
			noHash := &tg.Channel{ID: 5, Broadcast: true}
			raw := fullChannels(broadcast(1), group, left, min, noHash)
			// An auxiliary chat that is not an actual dialog must not be selectable.
			raw.Chats = append(raw.Chats, broadcast(999))
			raw.Dialogs = append(raw.Dialogs, &tg.Dialog{Peer: &tg.PeerUser{UserID: 9}, TopMessage: 77})
			raw.Users = []tg.UserClass{&tg.User{ID: 9, AccessHash: 99}}
			raw.Messages = []tg.MessageClass{&tg.Message{ID: 77, Date: 700, PeerID: &tg.PeerUser{UserID: 9}}}
			return &tg.MessagesDialogsSlice{Count: 6, Dialogs: raw.Dialogs, Chats: raw.Chats, Messages: raw.Messages, Users: raw.Users}, nil
		}
		if folder == 0 {
			if !r.ExcludePinned || r.OffsetID != 77 || r.OffsetDate != 700 {
				t.Fatalf("wrong cursor %+v", r)
			}
			if _, ok := r.OffsetPeer.(*tg.InputPeerUser); !ok {
				t.Fatalf("wrong peer %T", r.OffsetPeer)
			}
			if !failed {
				failed = true
				return nil, errors.New("network failure")
			}
			return &tg.MessagesDialogs{}, nil
		}
		return fullChannels(broadcast(8)), nil
	}
	page, err := c.ListChannels(context.Background(), "")
	if err != nil || len(page.Channels) != 1 || page.Channels[0].ID != "1" {
		t.Fatalf("filter failed: %+v %v", page, err)
	}
	if _, err := c.ListChannels(context.Background(), "forged"); !errors.Is(err, ErrChannelSelection) {
		t.Fatal("forged cursor accepted", err)
	}
	if _, err := c.ListChannels(context.Background(), page.NextCursor); err == nil {
		t.Fatal("network failure swallowed")
	}
	second, err := c.ListChannels(context.Background(), page.NextCursor)
	if err != nil || len(second.Channels) != 0 || second.NextCursor == "" {
		t.Fatalf("empty batch stopped archive: %+v %v", second, err)
	}
	archived, err := c.ListChannels(context.Background(), second.NextCursor)
	if err != nil || len(archived.Channels) != 1 || archived.Channels[0].ID != "8" || archived.NextCursor != "" {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	if err := c.SaveChannels(archived.SelectionToken, []string{"1", "8"}); err != nil {
		t.Fatal(err)
	}
	c.channelCatalog.expires = time.Now().Add(-time.Second)
	if err := c.SaveChannels(archived.SelectionToken, nil); !errors.Is(err, ErrChannelSelection) {
		t.Fatal("expired selection accepted", err)
	}
}

type failingChannelStore struct{ Store }

func (failingChannelStore) Save(string) error { return errors.New("disk unavailable") }

func TestChannelSaveFailureAndCancellation(t *testing.T) {
	c, f := channelClient(t)
	f.list = func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		return fullChannels(broadcast(1)), nil
	}
	page, err := c.ListChannels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	c.configStore = failingChannelStore{c.configStore}
	if err := c.SaveChannels(page.SelectionToken, []string{"1"}); err == nil || len(c.ChannelConfig().Channels) != 0 {
		t.Fatal("failed save published new state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListChannels(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if err := c.lock(); err != nil {
		t.Fatal("canceled call retained the account gate", err)
	}
	c.unlock()
}
