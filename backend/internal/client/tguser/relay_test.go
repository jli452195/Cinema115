package tguser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"strm115/backend/internal/model"
)

type relayLockedStore struct {
	mu    sync.Mutex
	value string
}

func (s *relayLockedStore) Value() string { s.mu.Lock(); defer s.mu.Unlock(); return s.value }
func (s *relayLockedStore) Save(value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = value
	return nil
}

type relayMemoryState struct {
	mu     sync.Mutex
	states map[string]string
	rules  []model.TelegramRelayRule
	err    error
}

func (s *relayMemoryState) LoadTelegramRelayState(ctx context.Context, owner int64, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[key], ctx.Err()
}
func (s *relayMemoryState) CommitTelegramRelayUpdate(ctx context.Context, owner int64, key, value string, jobs []model.TelegramRelayJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	s.states[key] = value
	return nil
}
func (s *relayMemoryState) ListTelegramRelayRules(ctx context.Context) ([]model.TelegramRelayRule, error) {
	return s.rules, ctx.Err()
}

type relayFakeTransport struct {
	starts     atomic.Int32
	user       atomic.Int64
	photo      tg.UserProfilePhotoClass
	list       func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error)
	dialogs    func([]tg.InputDialogPeerClass) (*tg.MessagesPeerDialogs, error)
	send       func(bin.Encoder) error
	difference func(*tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error)
}

func relayClient(t *testing.T) (*Client, *relayFakeTransport) {
	t.Helper()
	c, err := New(&relayLockedStore{}, &relayLockedStore{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if err := c.Save(context.Background(), Config{APIID: 123, APIHash: strings.Repeat("a", 32)}); err != nil {
		t.Fatal(err)
	}
	if err := c.sessionStore.Save("saved-session"); err != nil {
		t.Fatal(err)
	}
	f := &relayFakeTransport{}
	f.user.Store(42)
	c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
		f.starts.Add(1)
		client := telegram.NewClient(1, "test", telegram.Options{Middlewares: []telegram.Middleware{telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc {
			return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				switch request := in.(type) {
				case *tg.UsersGetUsersRequest:
					if f.user.Load() == 0 {
						return &tgerr.Error{Code: 401, Type: "AUTH_KEY_UNREGISTERED"}
					}
					out.(*tg.UserClassVector).Elems = []tg.UserClass{&tg.User{ID: f.user.Load(), FirstName: "account", Photo: f.photo}}
				case *tg.MessagesGetDialogsRequest:
					if f.list == nil {
						return fmt.Errorf("unexpected dialogs query")
					}
					response, err := f.list(request)
					if err != nil {
						return err
					}
					out.(*tg.MessagesDialogsBox).Dialogs = response
				case *tg.MessagesGetPeerDialogsRequest:
					if f.dialogs == nil {
						return fmt.Errorf("unexpected selection confirmation")
					}
					response, err := f.dialogs(request.Peers)
					if err != nil {
						return err
					}
					*out.(*tg.MessagesPeerDialogs) = *response
				case *tg.UpdatesGetStateRequest:
					*out.(*tg.UpdatesState) = tg.UpdatesState{Pts: 5, Date: int(time.Now().Unix()), Seq: 1}
				case *tg.UpdatesGetDifferenceRequest:
					out.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifferenceEmpty{Date: int(time.Now().Unix()), Seq: 1}
				case *tg.UpdatesGetChannelDifferenceRequest:
					var response tg.UpdatesChannelDifferenceClass = &tg.UpdatesChannelDifferenceEmpty{Pts: 20, Final: true}
					if f.difference != nil {
						value, err := f.difference(request)
						if err != nil {
							return err
						}
						response = value
					}
					out.(*tg.UpdatesChannelDifferenceBox).ChannelDifference = response
				case *tg.MessagesSendMessageRequest, *tg.MessagesSendMediaRequest:
					if f.send != nil {
						if err := f.send(in); err != nil {
							return err
						}
					}
					out.(*tg.UpdatesBox).Updates = &tg.Updates{}
				case *tg.AuthLogOutRequest:
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

func relayDialogs() *tg.MessagesPeerDialogs {
	source, target := broadcast(100), broadcast(200)
	target.Creator = true
	bot := &tg.User{ID: 300, Bot: true, FirstName: "target bot", Username: "target_bot"}
	bot.SetAccessHash(333)
	sourceDialog := &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 100}}
	sourceDialog.SetPts(20)
	targetDialog := &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 200}}
	targetDialog.SetPts(30)
	return &tg.MessagesPeerDialogs{Dialogs: []tg.DialogClass{sourceDialog, targetDialog, &tg.Dialog{Peer: &tg.PeerUser{UserID: 300}}}, Chats: []tg.ChatClass{source, target}, Users: []tg.UserClass{bot}}
}

func TestRelayPeerSelectionPrivacyAndSearchIsolation(t *testing.T) {
	c, f := relayClient(t)
	ctx := context.Background()
	dialogs := relayDialogs()
	f.list = func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		return &tg.MessagesDialogs{Dialogs: dialogs.Dialogs, Chats: dialogs.Chats, Users: dialogs.Users}, nil
	}
	f.dialogs = func([]tg.InputDialogPeerClass) (*tg.MessagesPeerDialogs, error) { return dialogs, nil }
	c.channels = channelSettings{OwnerID: 42, Peers: []channelPeer{{Channel: Channel{ID: "999", Title: "search-only"}, AccessHash: 999}}}
	first, err := c.ListRelayPeers(ctx, "")
	if err != nil || len(first.Peers) != 3 {
		t.Fatal("relay list", first, err)
	}
	second, err := c.ListRelayPeers(ctx, "")
	if err != nil || second.SelectionToken != first.SelectionToken {
		t.Fatal("target picker invalidated source picker", err)
	}
	identity, err := c.PrepareRelay(ctx, "channel:100", "user:300", first.SelectionToken, second.SelectionToken)
	if err != nil || identity.OwnerID != 42 || identity.SourceID != "channel:100" || identity.TargetID != "user:300" {
		t.Fatal("prepare", identity, err)
	}
	if len(c.channels.Peers) != 1 || c.channels.Peers[0].ID != "999" {
		t.Fatal("relay selection overwrote resource-search channels")
	}
	if len(c.relayPeers.Peers) != 2 || c.relayPeers.Peers[0].Pts != 20 {
		t.Fatal("selected access peers or initial channel pts were not saved")
	}
	if err := c.Save(context.Background(), Config{APIID: 123, Bot: testBotUsername}); err != nil {
		t.Fatal(err)
	}
	restored, err := New(c.configStore, c.sessionStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	if len(restored.relayPeers.Peers) != 2 || len(restored.channels.Peers) != 1 {
		t.Fatal("saving Bot config erased forwarding peers")
	}
	data, _ := json.Marshal(first)
	if strings.Contains(string(data), "accessHash") || strings.Contains(string(data), "apiHash") {
		t.Fatal("peer credentials leaked in picker")
	}
	if _, err := c.PrepareRelay(ctx, "channel:100", "channel:100", first.SelectionToken, first.SelectionToken); !errors.Is(err, ErrInput) {
		t.Fatal("source may target itself", err)
	}
	if _, err := c.PrepareRelay(ctx, "channel:100", "user:300", "bad-token", first.SelectionToken); !errors.Is(err, ErrChannelSelection) {
		t.Fatal("invalid catalog token accepted", err)
	}
	// Permissions are refreshed at save time; a stale picker cannot grant write.
	f.dialogs = func([]tg.InputDialogPeerClass) (*tg.MessagesPeerDialogs, error) {
		copy := relayDialogs()
		copy.Chats[1].(*tg.Channel).Creator = false
		return copy, nil
	}
	if _, err := c.PrepareRelay(ctx, "channel:100", "channel:200", first.SelectionToken, first.SelectionToken); err == nil {
		t.Fatal("stale target permission accepted")
	}
	f.user.Store(43)
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if c.relayPeers.OwnerID != 0 || c.relayCatalog != nil {
		t.Fatal("account switch inherited encrypted forwarding peers")
	}
}

func TestRelayPeerKindsAndPermissions(t *testing.T) {
	response := relayDialogs()
	group := &tg.Channel{ID: 400, Title: "group", Megagroup: true}
	group.SetAccessHash(444)
	denied := *group
	denied.ID = 401
	denied.SetBannedRights(tg.ChatBannedRights{SendMessages: true})
	defaultDenied := *group
	defaultDenied.ID = 402
	defaultDenied.SetDefaultBannedRights(tg.ChatBannedRights{SendPlain: true})
	admin := defaultDenied
	admin.ID = 403
	admin.SetAdminRights(tg.ChatAdminRights{DeleteMessages: true})
	chat := &tg.Chat{ID: 500, Title: "basic group"}
	response.Chats = append(response.Chats, group, &denied, &defaultDenied, &admin, chat, &tg.Chat{ID: 501, Left: true})
	for _, id := range []int64{400, 401, 402, 403} {
		response.Dialogs = append(response.Dialogs, &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: id}})
	}
	response.Dialogs = append(response.Dialogs, &tg.Dialog{Peer: &tg.PeerChat{ChatID: 500}}, &tg.Dialog{Peer: &tg.PeerChat{ChatID: 501}})
	peers := relayDialogPeers(response.Dialogs, response.Chats, response.Users)
	byID := make(map[string]RelayPeer)
	for _, p := range peers {
		byID[p.ID] = p.RelayPeer
	}
	if !byID["channel:100"].Source || byID["channel:100"].Target || !byID["channel:200"].Target {
		t.Fatal("broadcast role mismatch", byID)
	}
	if !byID["channel:400"].Target || byID["channel:400"].Source || !byID["channel:403"].Target || !byID["chat:500"].Target || !byID["user:300"].Target {
		t.Fatal("supported target lost", byID)
	}
	for _, id := range []string{"channel:401", "channel:402", "chat:501"} {
		if _, ok := byID[id]; ok {
			t.Fatal("unwritable target exposed", id)
		}
	}
}

func seedRelay(c *Client) *relayMemoryState {
	dialogs := relayDialogs()
	peers := relayDialogPeers(dialogs.Dialogs, dialogs.Chats, dialogs.Users)
	c.relayPeers = relaySettings{OwnerID: 42, Peers: peers}
	return &relayMemoryState{states: make(map[string]string), rules: []model.TelegramRelayRule{{ID: "rule", OwnerID: 42, SourceID: "channel:100", TargetID: "user:300", Enabled: true}}}
}

func TestRelaySharedConnectionDoesNotHoldGateAndLogoutJoins(t *testing.T) {
	c, f := relayClient(t)
	store := seedRelay(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.RunRelay(ctx, store, func(ctx context.Context, owner int64, message *tg.Message) error {
			if message == nil {
				if owner != 42 {
					return ErrLogin
				}
				close(ready)
			}
			return nil
		})
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("relay did not authenticate")
	}
	if !c.RelayConnected() {
		t.Fatal("ready callback was fired before connection state")
	}
	release, err := c.exclusive(context.Background())
	if err != nil {
		t.Fatal("persistent watcher held normal operation gate", err)
	}
	release()
	if err := c.Check(context.Background()); err != nil {
		t.Fatal("normal operation could not borrow active client", err)
	}
	if f.starts.Load() != 1 {
		t.Fatal("normal operation started a second client sharing one session", f.starts.Load())
	}
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal("logout", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("logout did not join persistent connection")
	}
	if c.RelayConnected() || c.View().HasSession || c.View().Authorized || c.relayPeers.OwnerID != 42 {
		t.Fatal("logout left an active connection or erased forwarding selections")
	}
}

func TestRelayDifferencePersistsCardsBeforePtsAndRetriesFailedDelivery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			c, f := relayClient(t)
			store := seedRelay(c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var handled atomic.Bool
			f.difference = func(request *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
				if request.Channel.(*tg.InputChannel).ChannelID != 100 || request.Pts != 20 {
					return nil, errors.New("restored wrong source or pts")
				}
				return &tg.UpdatesChannelDifference{Final: true, Pts: 21, NewMessages: []tg.MessageClass{&tg.Message{ID: 7, Date: int(time.Now().Unix()), PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "resource"}}}, nil
			}
			done := make(chan error, 1)
			go func() {
				done <- c.RunRelay(ctx, store, func(ctx context.Context, owner int64, message *tg.Message) error {
					if message == nil {
						return nil
					}
					handled.Store(true)
					store.mu.Lock()
					defer store.mu.Unlock()
					if store.states["channel:100"] == "21" {
						return errors.New("pts advanced before outbox")
					}
					if fail {
						return errors.New("outbox write rejected")
					}
					store.states["card"] = "durable"
					return nil
				})
			}()
			if !fail {
				deadline := time.After(time.Second)
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					store.mu.Lock()
					pts := store.states["channel:100"]
					store.mu.Unlock()
					if pts == "21" {
						break
					}
					select {
					case <-deadline:
						t.Fatal("channel difference was not committed")
					case <-ticker.C:
					}
				}
				cancel()
			}
			select {
			case err := <-done:
				if fail && err == nil {
					t.Fatal("handler failure did not stop connection")
				}
			case <-time.After(time.Second):
				t.Fatal("failed callback or cancel did not stop relay")
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if !handled.Load() {
				t.Fatal("difference skipped source message")
			}
			if fail && store.states["channel:100"] == "21" {
				t.Fatal("failed card advanced persisted pts")
			}
			if !fail && store.states["card"] != "durable" {
				t.Fatal("successful card was not committed")
			}
		})
	}
}

func TestRelaySendKeepsRandomIDWithoutOriginalButtons(t *testing.T) {
	c, f := relayClient(t)
	seedRelay(c)
	c.authorized = true
	c.userID = 42
	var ids []int64
	f.send = func(raw bin.Encoder) error {
		switch request := raw.(type) {
		case *tg.MessagesSendMessageRequest:
			ids = append(ids, request.RandomID)
			if _, ok := request.GetReplyMarkup(); ok {
				t.Fatal("copied original interactive buttons")
			}
			if request.Message != "card\nhttps://115.com/s/test" {
				t.Fatal("card text changed")
			}
		case *tg.MessagesSendMediaRequest:
			ids = append(ids, request.RandomID)
			return &tgerr.Error{Code: 400, Type: "FILE_REFERENCE_EXPIRED"}
		}
		return nil
	}
	payload := RelayPayload{Text: "card\nhttps://115.com/s/test", Photo: &RelayPhoto{ID: 1, AccessHash: 2, FileReference: []byte("old")}}
	if err := c.RelaySend(context.Background(), 42, "user:300", payload, 789); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 789 || ids[1] != 789 {
		t.Fatal("fallback changed replay identity", ids)
	}
	f.send = func(bin.Encoder) error { return &tgerr.Error{Code: 420, Type: "FLOOD_WAIT", Argument: 9} }
	err := c.RelaySend(context.Background(), 42, "user:300", RelayPayload{Text: "card"}, 790)
	var operation *OperationError
	if !errors.As(err, &operation) || operation.Code != "flood_wait" || operation.RetryAt == nil || time.Until(*operation.RetryAt) < 8*time.Second {
		t.Fatal("flood wait lost retry deadline", err)
	}
	if err := c.RelaySend(context.Background(), 43, "user:300", payload, 789); !errors.Is(err, ErrLogin) {
		t.Fatal("another account inherited target", err)
	}
	if err := relayPublicError(context.DeadlineExceeded); !errors.As(err, &operation) || operation.Code != "timeout" {
		t.Fatal("ambiguous timeout not recognized", err)
	}
}

func TestRelayStateScopeAndFailureBlocksEveryLaterCheckpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &relayMemoryState{states: make(map[string]string)}
	state := &relayState{owner: 42, channels: map[int64]relayPeer{100: {RelayPeer: RelayPeer{ID: "channel:100", Source: true}, AccessHash: 123, Pts: 20}}, store: store, cancel: cancel}
	if err := state.SetState(ctx, 42, updates.State{Pts: 1}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetChannelPts(ctx, 42, 999, 999); err != nil {
		t.Fatal(err)
	}
	if len(store.states) != 1 {
		t.Fatal("unselected channel got durable state", store.states)
	}
	var restored []int64
	if err := state.ForEachChannels(ctx, 42, func(ctx context.Context, id int64, pts int) error {
		restored = append(restored, id)
		if pts != 20 {
			return errors.New("wrong baseline")
		}
		return nil
	}); err != nil || len(restored) != 1 {
		t.Fatal("restored unselected channels", restored, err)
	}
	raw := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 100}}, Pts: 21, PtsCount: 1}, &tg.UpdateNewChannelMessage{Message: &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 999}}, Pts: 999, PtsCount: 1}}}
	if got := state.filter(raw).(*tg.Updates); len(got.Updates) != 1 {
		t.Fatal("unselected channel entered update manager")
	}
	hint := &tg.UpdateChannelTooLong{ChannelID: 100}
	hint.SetPts(9000)
	short := state.filter(&tg.UpdateShort{Date: 1234, Update: hint}).(*tg.UpdateShort)
	if _, hinted := short.Update.(*tg.UpdateChannelTooLong).GetPts(); hinted || short.Date != 1234 {
		t.Fatal("short update kept large pts hint or lost timestamp")
	}
	rejected := errors.New("storage unavailable")
	if err := state.handle(ctx, raw, func(context.Context, int64, *tg.Message) error { return rejected }); !errors.Is(err, rejected) {
		t.Fatal("handler failure swallowed", err)
	}
	if ctx.Err() == nil {
		t.Fatal("handler error did not cancel persistent client")
	}
	if err := state.SetChannelPts(context.Background(), 42, 100, 21); !errors.Is(err, rejected) {
		t.Fatal("channel pts advanced after handler failure", err)
	}
	if err := state.SetPts(context.Background(), 42, 999); !errors.Is(err, rejected) {
		t.Fatal("global pts advanced after handler failure", err)
	}
}

type relaySignalContext struct {
	context.Context
	once    sync.Once
	arrived chan struct{}
}

func (c *relaySignalContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.arrived) })
	return c.Context.Done()
}

func TestRelaySendNeverBorrowsAReplacementLogin(t *testing.T) {
	c, _ := relayClient(t)
	seedRelay(c)
	c.authorized = true
	c.userID = 42
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := &relayConnection{ctx: base, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), connected: true}
	c.relayConnection = original
	ctx := &relaySignalContext{Context: context.Background(), arrived: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- c.RelaySend(ctx, 42, "user:300", RelayPayload{Text: "old owner card"}, 123) }()
	select {
	case <-ctx.arrived:
	case <-time.After(time.Second):
		t.Fatal("send did not capture its original connection")
	}
	// Simulate logout/relogin while this operation waits for the old handshake.
	newContext, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	replacement := &relayConnection{ctx: newContext, cancel: newCancel, ready: make(chan struct{}), done: make(chan struct{}), connected: true}
	c.mu.Lock()
	c.userID = 43
	c.relayPeers.OwnerID = 43
	c.relayConnection = replacement
	c.mu.Unlock()
	close(original.ready)
	select {
	case err := <-result:
		if !errors.Is(err, ErrLogin) {
			t.Fatal("stale owner reached send transport", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old send waited on replacement login")
	}
	c.mu.Lock()
	c.relayConnection = nil
	c.mu.Unlock()
	close(original.done)
	close(replacement.done)
}

func TestRelaySourceSnapshotAcceptedBeforeGapCheckpoint(t *testing.T) {
	c, f := relayClient(t)
	store := seedRelay(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.difference = func(*tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
		dialog := &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 100}}
		dialog.SetPts(900)
		messages := make([]tg.MessageClass, 0, 3)
		for _, id := range []int{899, 897, 898} {
			messages = append(messages, &tg.Message{ID: id, Date: int(time.Now().Unix()), PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "latest available"})
		}
		return &tg.UpdatesChannelDifferenceTooLong{Dialog: dialog, Messages: messages}, nil
	}
	done := make(chan error, 1)
	var messageIDs []int
	go func() {
		done <- c.RunRelay(ctx, store, func(ctx context.Context, owner int64, message *tg.Message) error {
			if message == nil {
				return nil
			}
			messageIDs = append(messageIDs, message.ID)
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.states["channel:100"] == "900" {
				return errors.New("gap checkpoint skipped latest snapshot")
			}
			store.states["snapshot"] = "saved"
			return nil
		})
	}()
	select {
	case err := <-done:
		var detail *OperationError
		if !errors.As(err, &detail) || detail.Code != "update_gap" {
			t.Fatal("missing gap warning", err)
		}
	case <-time.After(time.Second):
		t.Fatal("gap was not reported")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.states["channel:100"] != "900" || store.states["snapshot"] != "saved" {
		t.Fatal("gap recovery advanced without latest available messages", store.states)
	}
	if !slices.Equal(messageIDs, []int{897, 898, 899}) {
		t.Fatal("snapshot messages were forwarded out of chronological order", messageIDs)
	}
}
