package tguser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestRelayAvatarMetadataDoesNotDownloadPreviews(t *testing.T) {
	c, f := relayClient(t)
	ctx := context.Background()
	dialogs := relayDialogs()
	f.photo = &tg.UserProfilePhoto{PhotoID: 888, DCID: 2}
	for _, raw := range dialogs.Chats {
		raw.(*tg.Channel).Photo = &tg.ChatPhoto{PhotoID: 999, DCID: 2}
	}
	dialogs.Users[0].(*tg.User).Photo = &tg.UserProfilePhoto{PhotoID: 777, DCID: 2}
	f.list = func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		return &tg.MessagesDialogs{Dialogs: dialogs.Dialogs, Chats: dialogs.Chats, Users: dialogs.Users}, nil
	}
	f.dialogs = func([]tg.InputDialogPeerClass) (*tg.MessagesPeerDialogs, error) { return dialogs, nil }
	if err := c.Check(ctx); err != nil || c.View().AvatarID != "888" {
		t.Fatal("account photo metadata missing", err)
	}
	page, err := c.ListRelayPeers(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range page.Peers {
		if peer.AvatarID == "" {
			t.Fatal("peer photo metadata missing", peer.ID)
		}
	}
	if _, err := c.PrepareRelay(ctx, "channel:100", "user:300", page.SelectionToken, page.SelectionToken); err != nil {
		t.Fatal(err)
	}
	if f.starts.Load() != 3 {
		t.Fatal("metadata query downloaded photos", f.starts.Load())
	}
	if saved := c.SavedRelayPeers(); len(saved) != 2 || saved[0].AvatarID == "" || f.starts.Load() != 3 {
		t.Fatal("saved photo metadata missing or made an extra Telegram request")
	}
	restored, err := New(c.configStore, c.sessionStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	if len(restored.relayPeers.Peers) != 2 || restored.relayPeers.Peers[0].PhotoID == 0 || restored.relayPeers.Peers[1].DCID != 2 {
		t.Fatal("saved photo metadata missing")
	}
	encoded, err := json.Marshal(page)
	if err != nil || strings.Contains(string(encoded), "data:image") || strings.Contains(string(encoded), "accessHash") || strings.Contains(string(encoded), "dcId") {
		t.Fatal("private peer metadata or inline bytes leaked", err)
	}
	f.photo = nil
	if err := c.Check(ctx); err != nil || c.View().AvatarID != "" {
		t.Fatal("removed photo stayed visible", err)
	}
	f.user.Store(0)
	if err := c.Check(ctx); err != nil || c.View().AvatarID != "" {
		t.Fatal("invalid login retained photo", err)
	}
	if len(c.SavedRelayPeers()) != 0 {
		t.Fatal("invalid login retained saved peer metadata")
	}
}

type avatarTestInvoker struct {
	telegram.InvokeFunc
	closed *atomic.Int32
}

func (i avatarTestInvoker) Close() error { i.closed.Add(1); return nil }

func avatarTestClient(t *testing.T, crossDC, shared bool, download func(context.Context) ([]byte, error)) (*Client, *atomic.Int32, *atomic.Int32, []byte) {
	t.Helper()
	c, _ := relayClient(t)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 160, 160))); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	calls, closed := &atomic.Int32{}, &atomic.Int32{}
	invoke := telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		request, ok := in.(*tg.UploadGetFileRequest)
		if !ok {
			t.Errorf("unexpected non-download request %T", in)
			return ErrInput
		}
		location, ok := request.Location.(*tg.InputPeerPhotoFileLocation)
		if !ok || location.Big || location.PhotoID != 123 || request.Limit != avatarMaxBytes || request.Offset != 0 {
			t.Error("incorrect small-avatar request")
			return ErrInput
		}
		calls.Add(1)
		payload := data
		if download != nil {
			var err error
			payload, err = download(ctx)
			if err != nil {
				return err
			}
		}
		out.(*tg.UploadFileBox).File = &tg.UploadFile{Bytes: payload}
		return nil
	})
	client := telegram.NewClient(1, "test", telegram.Options{Middlewares: []telegram.Middleware{telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc { return invoke })}})
	dcID := client.Config().ThisDC
	if crossDC {
		dcID++
	}
	c.authorized, c.userID, c.avatar = true, 42, avatarPhoto{photoID: 123, dcID: dcID}
	c.avatarDC = func(_ context.Context, _ *telegram.Client, id int) (telegram.CloseInvoker, error) {
		if !crossDC || id != dcID {
			t.Error("same-DC authorization transfer or incorrect DC")
		}
		return avatarTestInvoker{invoke, closed}, nil
	}
	c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
		return operation(ctx, client)
	}
	if shared {
		ctx, cancel := context.WithCancel(context.Background())
		connection := &relayConnection{client: client, ctx: ctx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), connected: true}
		close(connection.ready)
		c.relayConnection = connection
		t.Cleanup(func() { c.mu.Lock(); c.relayConnection = nil; c.mu.Unlock(); cancel(); close(connection.done) })
	}
	return c, calls, closed, data
}

func TestRelayAvatarCrossDCCache(t *testing.T) {
	c, calls, closed, expected := avatarTestClient(t, true, false, nil)
	data, mime, err := c.RelayAvatar(context.Background(), "self", "123")
	if err != nil || mime != "image/png" || !bytes.Equal(data, expected) {
		t.Fatal("clear avatar download failed", mime, err)
	}
	data[0] = 0
	cached, _, err := c.RelayAvatar(context.Background(), "self", "123")
	if err != nil || !bytes.Equal(cached, expected) || calls.Load() != 1 || closed.Load() != 1 {
		t.Fatal("cache changed, redownloaded or wrong connection closed", err)
	}
	key := avatarKey{owner: 42, peer: "self", photo: 123}
	entry := c.avatarCache[key]
	entry.expires = time.Now().Add(-time.Second)
	c.avatarCache[key] = entry
	if _, _, err := c.RelayAvatar(context.Background(), "self", "123"); err != nil || calls.Load() != 2 {
		t.Fatal("expired avatar did not reload", err)
	}
	c.avatar = avatarPhoto{}
	if _, _, err := c.RelayAvatar(context.Background(), "self", "123"); !errors.Is(err, ErrAvatarUnavailable) {
		t.Fatal("removed photo accessible through cache", err)
	}
	if _, _, err := c.RelayAvatar(context.Background(), "user:999", "123"); !errors.Is(err, ErrAvatarUnavailable) {
		t.Fatal("unknown peer accepted", err)
	}
	c.authorized = false
	if _, _, err := c.RelayAvatar(context.Background(), "self", "123"); !errors.Is(err, ErrLogin) {
		t.Fatal("unauthorized cache access", err)
	}
}

func TestRelayAvatarSharedConnectionAndOwnerChange(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c, calls, _, _ := avatarTestClient(t, true, true, func(ctx context.Context) ([]byte, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, nil
		}
	})
	c.gate <- struct{}{}
	defer c.unlock()
	result := make(chan error, 1)
	go func() { _, _, err := c.RelayAvatar(context.Background(), "self", "123"); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("shared avatar waited for the search gate")
	}
	c.mu.Lock()
	c.userID = 99
	c.mu.Unlock()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrLogin) {
			t.Fatal("identity change returned old photo", err)
		}
	case <-time.After(time.Second):
		t.Fatal("download did not finish")
	}
	if calls.Load() != 1 || len(c.avatarCache) != 0 || len(c.avatarFlights) != 0 {
		t.Fatal("owner change leaked image or in-flight request")
	}
}

func TestRelayAvatarRejectsInvalidImagesAndInputs(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("<svg></svg>"), []byte{0xff, 0xd8, 0xff}, make([]byte, avatarMaxBytes)} {
		if _, err := avatarMIME(data); !errors.Is(err, ErrAvatarUnavailable) {
			t.Fatal("invalid image accepted", err)
		}
	}
	c, calls, _, _ := avatarTestClient(t, true, false, nil)
	for _, photo := range []string{"", "0", "x", "0123", strings.Repeat("9", 32)} {
		if _, _, err := c.RelayAvatar(context.Background(), "self", photo); !errors.Is(err, ErrInput) {
			t.Fatal("invalid photo ID accepted", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached Telegram")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	c.gate <- struct{}{}
	defer c.unlock()
	if _, _, err := c.RelayAvatar(ctx, "self", "123"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("standalone request ignored cancellation", err)
	}
}
