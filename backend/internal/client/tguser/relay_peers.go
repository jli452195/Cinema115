package tguser

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type RelayPeer struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username,omitempty"`
	AvatarID string `json:"avatarId,omitempty"`
	Kind     string `json:"kind"`
	Source   bool   `json:"source"`
	Target   bool   `json:"target"`
}

type RelayPeerPage struct {
	Peers          []RelayPeer `json:"peers"`
	SelectionToken string      `json:"selectionToken"`
	NextCursor     string      `json:"nextCursor"`
}

type RelayIdentity struct {
	OwnerID                                      int64
	SourceID, SourceTitle, TargetID, TargetTitle string
	SourcePts                                    int
}

type relayPeer struct {
	RelayPeer
	AccessHash int64 `json:"accessHash"`
	Pts        int   `json:"pts,omitempty"`
	PhotoID    int64 `json:"photoId,omitempty"`
	DCID       int   `json:"dcId,omitempty"`
}

type relaySettings struct {
	OwnerID int64       `json:"ownerId,omitempty"`
	Peers   []relayPeer `json:"peers,omitempty"`
}

type relayCatalog struct {
	ownerID                      int64
	expires                      time.Time
	token, cursor                string
	folder, offsetID, offsetDate int
	offsetPeer                   tg.InputPeerClass
	peers                        map[string]relayPeer
}

func (c *Client) SavedRelayPeers() []RelayPeer {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]RelayPeer, 0, len(c.relayPeers.Peers))
	if c.relayPeers.OwnerID == 0 || c.relayPeers.OwnerID != c.displayOwnerLocked() {
		return result
	}
	for _, peer := range c.relayPeers.Peers {
		result = append(result, peer.RelayPeer)
	}
	return result
}

// The relay owns a separate picker and saved peer set. Changing resource-search
// channels cannot add, remove or retarget a forwarding rule.
func (c *Client) ListRelayPeers(ctx context.Context, cursor string) (RelayPeerPage, error) {
	release, err := c.read(ctx)
	if err != nil {
		return RelayPeerPage{}, err
	}
	defer release()
	unlock, err := c.acquire(ctx, c.relayPicker, 1)
	if err != nil {
		return RelayPeerPage{}, err
	}
	defer unlock()
	if c.sessionStore.Value() == "" {
		return RelayPeerPage{}, ErrLogin
	}
	var result RelayPeerPage
	err = c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		c.mu.Lock()
		if !c.authorized || c.userID == 0 {
			c.mu.Unlock()
			return ErrLogin
		}
		catalog := c.relayCatalog
		if cursor == "" {
			// Two source/target pickers may be opened consecutively. Keep their
			// selection token valid while restarting the dialog-page cursor.
			if catalog == nil || catalog.ownerID != c.userID || !time.Now().Before(catalog.expires) {
				token, err := channelToken()
				if err != nil {
					c.mu.Unlock()
					return err
				}
				catalog = &relayCatalog{ownerID: c.userID, expires: time.Now().Add(30 * time.Minute), token: token, peers: make(map[string]relayPeer)}
			}
			copy := *catalog
			catalog = &copy
			catalog.folder = 0
			catalog.offsetID = 0
			catalog.offsetDate = 0
			catalog.offsetPeer = &tg.InputPeerEmpty{}
		} else if catalog == nil || catalog.ownerID != c.userID || !time.Now().Before(catalog.expires) || cursor != catalog.cursor || catalog.folder > 1 {
			c.mu.Unlock()
			return ErrChannelSelection
		}
		next := *catalog
		next.peers = make(map[string]relayPeer, len(catalog.peers))
		for key, value := range catalog.peers {
			next.peers[key] = value
		}
		c.mu.Unlock()
		request := &tg.MessagesGetDialogsRequest{Limit: 100, OffsetID: next.offsetID, OffsetDate: next.offsetDate, OffsetPeer: next.offsetPeer, ExcludePinned: next.offsetID != 0}
		request.SetFolderID(next.folder)
		response, err := client.API().MessagesGetDialogs(ctx, request)
		if err != nil {
			return err
		}
		modified, ok := response.AsModified()
		if !ok {
			return errors.New("Telegram 会话列表未返回完整响应")
		}
		_, full := response.(*tg.MessagesDialogs)
		entries := relayDialogPeers(modified.GetDialogs(), modified.GetChats(), modified.GetUsers())
		// Reuse the tested dialog pagination logic, including archive handling
		// and top-message peer checks, without sharing the search catalog.
		paging := channelCatalog{offsetID: next.offsetID, offsetDate: next.offsetDate, offsetPeer: next.offsetPeer}
		if _, err := channelBatch(&paging, modified, !full); err != nil {
			return err
		}
		next.offsetID, next.offsetDate, next.offsetPeer = paging.offsetID, paging.offsetDate, paging.offsetPeer
		if full || len(modified.GetDialogs()) == 0 {
			next.folder++
			next.offsetID, next.offsetDate, next.offsetPeer = 0, 0, &tg.InputPeerEmpty{}
		}
		next.cursor = ""
		if next.folder <= 1 {
			next.cursor, err = channelToken()
			if err != nil {
				return err
			}
		}
		result = RelayPeerPage{Peers: make([]RelayPeer, 0, len(entries)), SelectionToken: next.token, NextCursor: next.cursor}
		for _, entry := range entries {
			next.peers[entry.ID] = entry
			result.Peers = append(result.Peers, entry.RelayPeer)
		}
		c.mu.Lock()
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return err
		}
		c.relayCatalog = &next
		c.mu.Unlock()
		return nil
	})
	if errors.Is(err, ErrChannelSelection) {
		return RelayPeerPage{}, err
	}
	return result, relayPublicError(err)
}

func relayDialogPeers(dialogs []tg.DialogClass, chats []tg.ChatClass, users []tg.UserClass) []relayPeer {
	available := make(map[string]relayPeer, len(chats)+len(users))
	for _, raw := range chats {
		switch chat := raw.(type) {
		case *tg.Channel:
			if chat.Left || chat.Min {
				continue
			}
			hash, ok := chat.GetAccessHash()
			if !ok {
				continue
			}
			kind := "channel"
			if chat.Megagroup {
				kind = "group"
			}
			photo := telegramAvatarPhoto(chat.Photo)
			p := RelayPeer{ID: "channel:" + strconv.FormatInt(chat.ID, 10), Title: chat.Title, Username: chat.Username, AvatarID: photo.id(), Kind: kind, Source: chat.Broadcast, Target: relayChannelWritable(chat)}
			if p.Source || p.Target {
				available[p.ID] = relayPeer{RelayPeer: p, AccessHash: hash, PhotoID: photo.photoID, DCID: photo.dcID}
			}
		case *tg.Chat:
			if chat.Left || chat.Deactivated {
				continue
			}
			_, admin := chat.GetAdminRights()
			ban, restricted := chat.GetDefaultBannedRights()
			if !chat.Creator && !admin && restricted && (ban.SendMessages || ban.SendPlain) {
				continue
			}
			photo := telegramAvatarPhoto(chat.Photo)
			p := RelayPeer{ID: "chat:" + strconv.FormatInt(chat.ID, 10), Title: chat.Title, AvatarID: photo.id(), Kind: "group", Target: true}
			available[p.ID] = relayPeer{RelayPeer: p, PhotoID: photo.photoID, DCID: photo.dcID}
		}
	}
	for _, raw := range users {
		user, ok := raw.(*tg.User)
		if !ok || !user.Bot || user.Deleted || user.Min {
			continue
		}
		hash, ok := user.GetAccessHash()
		if !ok {
			continue
		}
		photo := telegramAvatarPhoto(user.Photo)
		p := RelayPeer{ID: "user:" + strconv.FormatInt(user.ID, 10), Title: strings.TrimSpace(user.FirstName + " " + user.LastName), Username: user.Username, AvatarID: photo.id(), Kind: "bot", Target: true}
		available[p.ID] = relayPeer{RelayPeer: p, AccessHash: hash, PhotoID: photo.photoID, DCID: photo.dcID}
	}
	result := make([]relayPeer, 0, len(available))
	for _, raw := range dialogs {
		dialog, ok := raw.(*tg.Dialog)
		if !ok {
			continue
		}
		id := relayPeerID(dialog.Peer)
		p, ok := available[id]
		if !ok {
			continue
		}
		p.Pts, _ = dialog.GetPts()
		result = append(result, p)
		delete(available, id)
	}
	return result
}

func relayChannelWritable(channel *tg.Channel) bool {
	if channel.Creator {
		return true
	}
	admin, hasAdmin := channel.GetAdminRights()
	if channel.Broadcast {
		return hasAdmin && admin.PostMessages
	}
	if !channel.Megagroup {
		return false
	}
	if ban, ok := channel.GetBannedRights(); ok && (ban.SendMessages || ban.SendPlain) {
		return false
	}
	if ban, ok := channel.GetDefaultBannedRights(); ok && !hasAdmin && (ban.SendMessages || ban.SendPlain) {
		return false
	}
	return true
}

func relayPeerID(p tg.PeerClass) string {
	switch p := p.(type) {
	case *tg.PeerChannel:
		return "channel:" + strconv.FormatInt(p.ChannelID, 10)
	case *tg.PeerChat:
		return "chat:" + strconv.FormatInt(p.ChatID, 10)
	case *tg.PeerUser:
		return "user:" + strconv.FormatInt(p.UserID, 10)
	}
	return ""
}

func (p relayPeer) input() (tg.InputPeerClass, error) {
	kind, id, ok := strings.Cut(p.ID, ":")
	if !ok {
		return nil, ErrInput
	}
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil || number <= 0 {
		return nil, ErrInput
	}
	switch kind {
	case "channel":
		return &tg.InputPeerChannel{ChannelID: number, AccessHash: p.AccessHash}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: number}, nil
	case "user":
		return &tg.InputPeerUser{UserID: number, AccessHash: p.AccessHash}, nil
	}
	return nil, ErrInput
}

func (c *Client) PrepareRelay(ctx context.Context, sourceID, targetID, sourceToken, targetToken string) (RelayIdentity, error) {
	if sourceID == targetID {
		return RelayIdentity{}, ErrInput
	}
	release, err := c.read(ctx)
	if err != nil {
		return RelayIdentity{}, err
	}
	defer release()
	unlock, err := c.acquire(ctx, c.relayPicker, 1)
	if err != nil {
		return RelayIdentity{}, err
	}
	defer unlock()
	if c.sessionStore.Value() == "" {
		return RelayIdentity{}, ErrLogin
	}
	var result RelayIdentity
	err = c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		c.mu.Lock()
		if !c.authorized || c.userID == 0 {
			c.mu.Unlock()
			return ErrLogin
		}
		owner := c.userID
		selected := make([]relayPeer, 0, 2)
		for _, value := range []struct{ id, token string }{{sourceID, sourceToken}, {targetID, targetToken}} {
			var entry relayPeer
			found := false
			if value.token != "" {
				catalog := c.relayCatalog
				if catalog == nil || catalog.ownerID != owner || catalog.token != value.token || !time.Now().Before(catalog.expires) {
					c.mu.Unlock()
					return ErrChannelSelection
				}
				entry, found = catalog.peers[value.id]
			} else if c.relayPeers.OwnerID == owner {
				for _, p := range c.relayPeers.Peers {
					if p.ID == value.id {
						entry = p
						found = true
						break
					}
				}
			}
			if !found {
				c.mu.Unlock()
				return ErrChannelSelection
			}
			selected = append(selected, entry)
		}
		c.mu.Unlock()
		if !selected[0].Source || !selected[1].Target {
			return ErrInput
		}
		request := &tg.MessagesGetPeerDialogsRequest{Peers: make([]tg.InputDialogPeerClass, 0, 2)}
		for _, entry := range selected {
			input, err := entry.input()
			if err != nil {
				return err
			}
			request.Peers = append(request.Peers, &tg.InputDialogPeer{Peer: input})
		}
		// Refresh both permissions and the initial channel pts before saving a
		// rule. On first connect, difference starts before any eligible new post.
		response, err := client.API().MessagesGetPeerDialogs(ctx, request.Peers)
		if err != nil {
			return err
		}
		fresh := relayDialogPeers(response.Dialogs, response.Chats, response.Users)
		byID := make(map[string]relayPeer, len(fresh))
		for _, entry := range fresh {
			byID[entry.ID] = entry
		}
		source, ok := byID[sourceID]
		if !ok || !source.Source || source.Pts <= 0 {
			return errors.New("来源频道不可读取，请重新选择已加入的频道")
		}
		target, ok := byID[targetID]
		if !ok || !target.Target {
			return errors.New("目标不可写入，请检查频道、群组或 Bot 权限")
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.userID != owner || !c.authorized {
			return ErrLogin
		}
		settings := relaySettings{OwnerID: owner, Peers: make([]relayPeer, 0, len(c.relayPeers.Peers)+2)}
		if c.relayPeers.OwnerID == owner {
			for _, p := range c.relayPeers.Peers {
				if p.ID != sourceID && p.ID != targetID {
					settings.Peers = append(settings.Peers, p)
				}
			}
		}
		if len(settings.Peers) >= 2000 {
			return errors.New("Telegram 转发配置数量已达上限")
		}
		settings.Peers = append(settings.Peers, source, target)
		previous := c.relayPeers
		c.relayPeers = settings
		if err := c.saveSettingsLocked(c.config, c.channels); err != nil {
			c.relayPeers = previous
			return err
		}
		result = RelayIdentity{OwnerID: owner, SourceID: source.ID, SourceTitle: source.Title, TargetID: target.ID, TargetTitle: target.Title, SourcePts: source.Pts}
		return nil
	})
	if errors.Is(err, ErrChannelSelection) {
		return result, err
	}
	return result, relayPublicError(err)
}
