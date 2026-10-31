package tguser

import (
	"context"
	"errors"
	"time"
	"unicode/utf16"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"strm115/backend/internal/model"
	"strm115/backend/internal/netproxy"
)

type RelayStateStore interface {
	LoadTelegramRelayState(context.Context, int64, string) (string, error)
	CommitTelegramRelayUpdate(context.Context, int64, string, string, []model.TelegramRelayJob) error
	ListTelegramRelayRules(context.Context) ([]model.TelegramRelayRule, error)
}

type RelayPayload struct {
	Text  string      `json:"text"`
	Photo *RelayPhoto `json:"photo,omitempty"`
}

type RelayPhoto struct {
	ID            int64  `json:"id"`
	AccessHash    int64  `json:"accessHash"`
	FileReference []byte `json:"fileReference"`
}

type relayConnection struct {
	client      *telegram.Client
	ctx         context.Context
	cancel      context.CancelFunc
	ready, done chan struct{}
	connected   bool // protected by Client.mu
}

func (c *Client) RelayConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.relayConnection != nil && c.relayConnection.connected
}

// A live relay owns the encrypted MTProto session. Ordinary operations borrow
// its client but retain their existing short-lived operation/search gate.
func (c *Client) runShared(ctx context.Context, connection *relayConnection, operation func(context.Context, *telegram.Client) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-connection.done:
		return ErrBusy
	case <-connection.ready:
	}
	c.mu.Lock()
	ready := connection.connected && !c.closed
	c.mu.Unlock()
	if !ready {
		return ErrBusy
	}
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(connection.ctx, cancel)
	defer stop()
	return operation(operationCtx, connection.client)
}

func (c *Client) stopRelay() {
	c.mu.Lock()
	connection := c.relayConnection
	if connection != nil {
		connection.cancel()
	}
	c.mu.Unlock()
	if connection != nil {
		<-connection.done
	}
}

func (c *Client) RunRelay(ctx context.Context, store RelayStateStore, handle func(context.Context, int64, *tg.Message) error) error {
	if store == nil || handle == nil {
		return ErrInput
	}
	// Wait for an in-flight standalone operation to close its MTProto client.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.gate <- struct{}{}:
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.unlock()
		return context.Canceled
	}
	if c.relayConnection != nil {
		c.mu.Unlock()
		c.unlock()
		return ErrBusy
	}
	cfg := c.config
	if cfg.APIID <= 0 || cfg.APIHash == "" {
		c.mu.Unlock()
		c.unlock()
		return ErrConfig
	}
	if c.sessionStore.Value() == "" {
		c.mu.Unlock()
		c.unlock()
		return ErrLogin
	}
	runCtx, cancel := context.WithCancel(ctx)
	connection := &relayConnection{ctx: runCtx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
	c.relayConnection = connection
	c.mu.Unlock()
	c.unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		connection.connected = false
		if c.relayConnection == connection {
			c.relayConnection = nil
		}
		close(connection.done)
		c.mu.Unlock()
	}()
	state := &relayState{store: store, cancel: cancel, channels: make(map[int64]relayPeer)}
	handler := telegram.UpdateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
		return state.handle(ctx, u, handle)
	})
	manager := updates.New(updates.Config{Storage: state, AccessHasher: state, Handler: handler, OnChannelTooLong: func(id int64) {
		state.fail(&OperationError{Code: "update_gap", Message: "Telegram 频道积压超出可恢复范围，请检查频道转发记录"})
	}})
	operation := func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		c.mu.Lock()
		owner := c.userID
		authorized := c.authorized
		saved := c.relayPeers
		c.mu.Unlock()
		if !authorized || owner == 0 || saved.OwnerID != owner {
			return ErrLogin
		}
		rules, err := store.ListTelegramRelayRules(ctx)
		if err != nil {
			return err
		}
		selected := make(map[string]bool, len(rules))
		for _, rule := range rules {
			if rule.OwnerID == owner && rule.Enabled {
				selected[rule.SourceID] = true
			}
		}
		state.mu.Lock()
		state.owner = owner
		for _, entry := range saved.Peers {
			if selected[entry.ID] {
				input, err := entry.input()
				if err != nil {
					state.mu.Unlock()
					return err
				}
				channel, ok := input.(*tg.InputPeerChannel)
				if !ok {
					state.mu.Unlock()
					return ErrInput
				}
				state.channels[channel.ChannelID] = entry
			}
		}
		count := len(state.channels)
		state.mu.Unlock()
		if count != len(selected) {
			return &OperationError{Code: "selection_required", Message: "部分来源频道尚未确认，请编辑规则重新选择频道"}
		}
		c.mu.Lock()
		connection.client = client
		c.mu.Unlock()
		return manager.Run(ctx, relayUpdatesAPI{Client: client.API(), state: state, handle: handle}, owner, updates.AuthOptions{OnStart: func(ctx context.Context) {
			c.mu.Lock()
			connection.connected = true
			c.mu.Unlock()
			if err := handle(ctx, owner, nil); err != nil {
				state.fail(err)
				return
			}
			close(connection.ready)
		}})
	}
	var err error
	if c.runOperation != nil {
		err = c.runOperation(runCtx, operation)
	} else {
		client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{SessionStorage: encryptedSession{c.sessionStore}, Resolver: dcs.Plain(dcs.PlainOptions{Dial: netproxy.Default.Dialer("telegram_user")}), UpdateHandler: telegram.UpdateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
			return manager.Handle(ctx, state.filter(u))
		})})
		err = client.Run(runCtx, func(ctx context.Context) error { return operation(ctx, client) })
	}
	if failed := state.failure(); failed != nil {
		return failed
	}
	return relayPublicError(err)
}

func (c *Client) RelaySend(ctx context.Context, ownerID int64, targetID string, payload RelayPayload, randomID int64) error {
	if randomID == 0 || payload.Text == "" || len(utf16.Encode([]rune(payload.Text))) > 4096 {
		return ErrInput
	}
	c.mu.Lock()
	if !c.authorized || c.userID != ownerID || c.relayPeers.OwnerID != ownerID {
		c.mu.Unlock()
		return ErrLogin
	}
	var target relayPeer
	found := false
	for _, entry := range c.relayPeers.Peers {
		if entry.ID == targetID && entry.Target {
			target = entry
			found = true
			break
		}
	}
	connection, testOperation := c.relayConnection, c.runOperation
	c.mu.Unlock()
	if !found {
		return &OperationError{Code: "target_unavailable", Message: "转发目标未确认，请重新选择目标"}
	}
	if connection == nil && testOperation == nil {
		return ErrBusy
	}
	peer, err := target.input()
	if err != nil {
		return err
	}
	operation := func(ctx context.Context, client *telegram.Client) error {
		c.mu.Lock()
		valid := c.authorized && c.userID == ownerID && c.relayPeers.OwnerID == ownerID && c.relayConnection == connection
		c.mu.Unlock()
		if !valid {
			return ErrLogin
		}
		if photo := payload.Photo; photo != nil && photo.ID != 0 && len(utf16.Encode([]rune(payload.Text))) <= 1024 {
			_, err := client.API().MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer, Media: &tg.InputMediaPhoto{ID: &tg.InputPhoto{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference}}, Message: payload.Text, RandomID: randomID})
			if err == nil {
				return nil
			}
			// Expired original thumbnails are optional. A rejected media send did
			// not publish a card, so text can reuse the same stable random ID.
			if !tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID", "PHOTO_INVALID", "CHAT_SEND_PHOTOS_FORBIDDEN", "CHAT_SEND_MEDIA_FORBIDDEN") {
				return err
			}
		}
		_, err := client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: payload.Text, RandomID: randomID, NoWebpage: true})
		return err
	}
	// A queued card must never borrow a replacement login's connection after
	// logout. Its owner, target credentials and transport snapshot stay fixed.
	if connection != nil {
		err = c.runShared(ctx, connection, operation)
	} else {
		err = testOperation(ctx, operation)
	}
	return relayPublicError(err)
}

func relayPublicError(err error) error {
	if err == nil {
		return nil
	}
	var operation *OperationError
	if errors.As(err, &operation) {
		return err
	}
	if wait, ok := tgerr.AsFloodWait(err); ok {
		retry := time.Now().Add(wait)
		return &OperationError{Code: "flood_wait", Message: "Telegram 限流，已延后重试", RetryAt: &retry}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &OperationError{Code: "timeout", Message: "Telegram 转发请求超时，将使用相同任务标识重试"}
	}
	if auth.IsUnauthorized(err) {
		return ErrLogin
	}
	if tgerr.Is(err, "CHAT_WRITE_FORBIDDEN", "USER_BANNED_IN_CHANNEL", "CHAT_ADMIN_REQUIRED", "PEER_ID_INVALID", "USER_IS_BLOCKED", "CHANNEL_PRIVATE") {
		return &OperationError{Code: "target_unavailable", Message: "Telegram 目标不可用或没有发送权限，请检查规则"}
	}
	return publicError(err)
}
