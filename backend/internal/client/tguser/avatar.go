package tguser

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strconv"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var ErrAvatarUnavailable = errors.New("Telegram 头像不可用")

const avatarMaxBytes = 128 << 10
const avatarCacheLimit = 128

type avatarPhoto struct {
	photoID int64
	dcID    int
}

func (p avatarPhoto) id() string {
	if p.photoID == 0 || p.dcID <= 0 {
		return ""
	}
	return strconv.FormatInt(p.photoID, 10)
}

func telegramAvatarPhoto(raw any) avatarPhoto {
	switch p := raw.(type) {
	case *tg.UserProfilePhoto:
		if p != nil {
			return avatarPhoto{photoID: p.PhotoID, dcID: p.DCID}
		}
	case *tg.ChatPhoto:
		if p != nil {
			return avatarPhoto{photoID: p.PhotoID, dcID: p.DCID}
		}
	}
	return avatarPhoto{}
}

type avatarKey struct {
	owner int64
	peer  string
	photo int64
}

type avatarEntry struct {
	data    []byte
	mime    string
	expires time.Time
}

type avatarFlight struct {
	done  chan struct{}
	entry avatarEntry
	err   error
}

// Only accept metadata from this account's own dialogs or saved selections.
// Caller-provided IDs cannot fetch arbitrary peers.
func (c *Client) avatarPeerLocked(peerID string, photoID int64) (tg.InputPeerClass, avatarPhoto, error) {
	if c.closed || !c.authorized || c.userID == 0 || c.sessionStore.Value() == "" {
		return nil, avatarPhoto{}, ErrLogin
	}
	if peerID == "self" {
		if c.avatar.photoID != photoID || c.avatar.id() == "" {
			return nil, avatarPhoto{}, ErrAvatarUnavailable
		}
		return &tg.InputPeerSelf{}, c.avatar, nil
	}
	var selected relayPeer
	found := false
	if cat := c.relayCatalog; cat != nil && cat.ownerID == c.userID && time.Now().Before(cat.expires) {
		selected, found = cat.peers[peerID]
	}
	if !found && c.relayPeers.OwnerID == c.userID {
		for _, peer := range c.relayPeers.Peers {
			if peer.ID == peerID {
				selected, found = peer, true
				break
			}
		}
	}
	if !found || selected.PhotoID != photoID || selected.DCID <= 0 {
		return nil, avatarPhoto{}, ErrAvatarUnavailable
	}
	peer, err := selected.input()
	return peer, avatarPhoto{photoID: selected.PhotoID, dcID: selected.DCID}, err
}

// RelayAvatar downloads the small, clear Telegram profile photo, never the
// stripped preview. Cache and in-flight requests are bounded and owner scoped.
func (c *Client) RelayAvatar(ctx context.Context, peerID, avatarID string) ([]byte, string, error) {
	photoID, err := strconv.ParseInt(avatarID, 10, 64)
	if err != nil || photoID == 0 || strconv.FormatInt(photoID, 10) != avatarID || len(peerID) == 0 || len(peerID) > 80 {
		return nil, "", ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	c.mu.Lock()
	peer, photo, err := c.avatarPeerLocked(peerID, photoID)
	if err != nil {
		c.mu.Unlock()
		return nil, "", err
	}
	key := avatarKey{owner: c.userID, peer: peerID, photo: photoID}
	if cached, ok := c.avatarCache[key]; ok && time.Now().Before(cached.expires) {
		c.mu.Unlock()
		return bytes.Clone(cached.data), cached.mime, nil
	}
	if flight := c.avatarFlights[key]; flight != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-flight.done:
		}
		c.mu.Lock()
		_, _, valid := c.avatarPeerLocked(peerID, photoID)
		ownerMatches := c.userID == key.owner
		c.mu.Unlock()
		if !ownerMatches {
			return nil, "", ErrLogin
		}
		if valid != nil {
			return nil, "", valid
		}
		return bytes.Clone(flight.entry.data), flight.entry.mime, flight.err
	}
	if c.avatarFlights == nil {
		c.avatarFlights = make(map[avatarKey]*avatarFlight)
	}
	if len(c.avatarFlights) >= avatarCacheLimit {
		c.mu.Unlock()
		return nil, "", ErrBusy
	}
	if c.avatarGate == nil {
		c.avatarGate = make(chan struct{}, 2)
	}
	flight := &avatarFlight{done: make(chan struct{})}
	c.avatarFlights[key] = flight
	connection := c.relayConnection
	c.mu.Unlock()
	entry, err := c.loadAvatar(ctx, key, peer, photo, connection)
	c.mu.Lock()
	_, _, valid := c.avatarPeerLocked(peerID, photoID)
	if c.userID != key.owner {
		err = ErrLogin
	} else if valid != nil {
		err = valid
	}
	if err == nil {
		if c.avatarCache == nil {
			c.avatarCache = make(map[avatarKey]avatarEntry)
		}
		for candidate, value := range c.avatarCache {
			if !time.Now().Before(value.expires) {
				delete(c.avatarCache, candidate)
			}
		}
		if len(c.avatarCache) >= avatarCacheLimit {
			var oldest avatarKey
			var expires time.Time
			for candidate, value := range c.avatarCache {
				if expires.IsZero() || value.expires.Before(expires) {
					oldest, expires = candidate, value.expires
				}
			}
			delete(c.avatarCache, oldest)
		}
		c.avatarCache[key] = entry
	} else {
		entry = avatarEntry{}
	}
	flight.entry, flight.err = entry, err
	delete(c.avatarFlights, key)
	close(flight.done)
	c.mu.Unlock()
	return bytes.Clone(entry.data), entry.mime, err
}

func (c *Client) loadAvatar(ctx context.Context, key avatarKey, peer tg.InputPeerClass, photo avatarPhoto, connection *relayConnection) (avatarEntry, error) {
	select {
	case <-ctx.Done():
		return avatarEntry{}, ctx.Err()
	case c.avatarGate <- struct{}{}:
	}
	defer func() { <-c.avatarGate }()
	var entry avatarEntry
	operation := func(ctx context.Context, client *telegram.Client) error {
		c.mu.Lock()
		_, _, err := c.avatarPeerLocked(key.peer, key.photo)
		ownerMatches, connectionMatches := c.userID == key.owner, c.relayConnection == connection
		dc := c.avatarDC
		c.mu.Unlock()
		if !ownerMatches {
			return ErrLogin
		}
		if err != nil {
			return err
		}
		if !connectionMatches {
			return ErrBusy
		}
		api := client.API()
		// DC() exports authorization and must only be used across data centers.
		// Same-DC avatars can use the existing connection without another RPC.
		if photo.dcID != client.Config().ThisDC {
			if dc == nil {
				dc = func(ctx context.Context, client *telegram.Client, id int) (telegram.CloseInvoker, error) {
					return client.DC(ctx, id, 1)
				}
			}
			invoker, err := dc(ctx, client, photo.dcID)
			if err != nil {
				return err
			}
			defer invoker.Close()
			api = tg.NewClient(invoker)
		}
		response, err := api.UploadGetFile(ctx, &tg.UploadGetFileRequest{Location: &tg.InputPeerPhotoFileLocation{Peer: peer, PhotoID: photo.photoID}, Limit: avatarMaxBytes})
		if err != nil {
			if tgerr.Is(err, "FILE_ID_INVALID", "LOCATION_INVALID", "PHOTO_ID_INVALID", "CHANNEL_PRIVATE", "USER_PRIVACY_RESTRICTED") {
				return ErrAvatarUnavailable
			}
			return err
		}
		file, ok := response.(*tg.UploadFile)
		if !ok {
			return ErrAvatarUnavailable
		}
		mime, err := avatarMIME(file.Bytes)
		if err != nil {
			return err
		}
		entry = avatarEntry{data: bytes.Clone(file.Bytes), mime: mime, expires: time.Now().Add(24 * time.Hour)}
		return nil
	}
	var err error
	if connection != nil {
		err = c.runShared(ctx, connection, operation)
	} else {
		// Avoid a second session writer during search/login or relay startup.
		select {
		case <-ctx.Done():
			return avatarEntry{}, ctx.Err()
		case c.gate <- struct{}{}:
		}
		err = c.run(ctx, operation)
		c.unlock()
	}
	if errors.Is(err, ErrAvatarUnavailable) || errors.Is(err, ErrLogin) || errors.Is(err, ErrInput) || errors.Is(err, ErrBusy) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return avatarEntry{}, err
	}
	return entry, publicError(err)
}

func avatarMIME(data []byte) (string, error) {
	if len(data) == 0 || len(data) >= avatarMaxBytes {
		return "", ErrAvatarUnavailable
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/jpeg", "image/png":
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 1024 || config.Height > 1024 {
			return "", ErrAvatarUnavailable
		}
	case "image/webp":
		// No image is decoded or expanded in the server process.
	default:
		return "", ErrAvatarUnavailable
	}
	return mime, nil
}
