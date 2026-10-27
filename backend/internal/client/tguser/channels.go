package tguser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
)

var ErrChannelSelection = errors.New("频道列表已失效，请重新打开选择频道")

type Channel struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username,omitempty"`
}

// Access hashes stay in the encrypted account store and never enter API views.
type channelPeer struct {
	Channel
	AccessHash int64 `json:"accessHash"`
}

type channelSettings struct {
	OwnerID int64         `json:"ownerId,omitempty"`
	Peers   []channelPeer `json:"peers,omitempty"`
}

type accountSettings struct {
	Config
	Channels channelSettings `json:"channels"`
}

type ChannelConfigView struct {
	Account  View      `json:"account"`
	Channels []Channel `json:"channels"`
}

type ChannelPage struct {
	Channels       []Channel          `json:"channels"`
	SelectionToken string             `json:"selectionToken"`
	NextCursor     string             `json:"nextCursor"`
	Config         *ChannelConfigView `json:"config,omitempty"`
}

type channelCatalog struct {
	ownerID       int64
	expires       time.Time
	token, cursor string
	folder        int
	offsetID      int
	offsetDate    int
	offsetPeer    tg.InputPeerClass
	peers         map[string]channelPeer
}

func (c *Client) saveSettingsLocked(cfg Config, channels channelSettings) error {
	data, err := json.Marshal(accountSettings{Config: cfg, Channels: channels})
	if err != nil {
		return err
	}
	if err := c.configStore.Save(string(data)); err != nil {
		return errors.New("保存 Telegram 配置失败")
	}
	c.config, c.channels = cfg, channels
	return nil
}

// ChannelConfig only reads saved state. It does not connect to Telegram.
func (c *Client) ChannelConfig() ChannelConfigView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channelConfigLocked()
}

func (c *Client) channelConfigLocked() ChannelConfigView {
	account := c.viewLocked()
	channels := make([]Channel, 0, len(c.channels.Peers))
	if account.HasSession && (!c.identityKnown || c.channels.OwnerID == c.userID) {
		for _, entry := range c.channels.Peers {
			channels = append(channels, entry.Channel)
		}
	}
	return ChannelConfigView{Account: account, Channels: channels}
}

func channelToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

// ListChannels reads one batch of dialogs, including archived channels after
// the main folder. The opaque cursor keeps all peer access hashes server-side.
func (c *Client) ListChannels(ctx context.Context, cursor string) (ChannelPage, error) {
	if err := c.lock(); err != nil {
		return ChannelPage{}, err
	}
	defer c.unlock()
	if c.sessionStore.Value() == "" {
		return ChannelPage{}, ErrLogin
	}
	var result ChannelPage
	err := c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		c.mu.Lock()
		if !c.authorized || c.userID == 0 {
			c.mu.Unlock()
			return ErrLogin
		}
		catalog := c.channelCatalog
		if cursor == "" {
			token, err := channelToken()
			if err != nil {
				c.mu.Unlock()
				return err
			}
			catalog = &channelCatalog{ownerID: c.userID, expires: time.Now().Add(30 * time.Minute), token: token, offsetPeer: &tg.InputPeerEmpty{}, peers: make(map[string]channelPeer)}
			if c.channels.OwnerID == c.userID {
				for _, entry := range c.channels.Peers {
					catalog.peers[entry.ID] = entry
				}
			}
		} else if catalog == nil || catalog.ownerID != c.userID || time.Now().After(catalog.expires) || cursor != catalog.cursor || catalog.folder > 1 {
			c.mu.Unlock()
			return ErrChannelSelection
		}
		// Work on a copy so a failed request leaves the previous cursor usable.
		next := *catalog
		c.mu.Unlock()

		request := &tg.MessagesGetDialogsRequest{Limit: 100, OffsetID: next.offsetID, OffsetDate: next.offsetDate, OffsetPeer: next.offsetPeer, ExcludePinned: next.offsetID != 0}
		request.SetFolderID(next.folder)
		response, err := client.API().MessagesGetDialogs(ctx, request)
		if err != nil {
			return err
		}
		modified, ok := response.AsModified()
		if !ok {
			return errors.New("频道列表未返回完整响应")
		}
		_, full := response.(*tg.MessagesDialogs)
		entries, err := channelBatch(&next, modified, !full)
		if err != nil {
			return err
		}
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
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, entry := range entries {
			next.peers[entry.ID] = entry
		}
		c.channelCatalog = &next
		result = ChannelPage{Channels: make([]Channel, 0, len(entries)), SelectionToken: next.token, NextCursor: next.cursor}
		if cursor == "" {
			config := c.channelConfigLocked()
			result.Config = &config
		}
		for _, entry := range entries {
			result.Channels = append(result.Channels, entry.Channel)
		}
		return nil
	})
	if errors.Is(err, ErrChannelSelection) {
		return ChannelPage{}, err
	}
	return result, publicError(err)
}

func channelBatch(next *channelCatalog, response tg.ModifiedMessagesDialogs, more bool) ([]channelPeer, error) {
	channels := make(map[int64]*tg.Channel)
	for _, raw := range response.GetChats() {
		if channel, ok := raw.(*tg.Channel); ok && channel.Broadcast && !channel.Left && !channel.Min {
			if _, valid := channel.GetAccessHash(); valid {
				channels[channel.ID] = channel
			}
		}
	}
	entries := make([]channelPeer, 0, len(channels))
	dialogs := response.GetDialogs()
	for _, raw := range dialogs {
		dialog, ok := raw.(*tg.Dialog)
		if !ok {
			continue
		}
		if p, ok := dialog.Peer.(*tg.PeerChannel); ok {
			if channel := channels[p.ChannelID]; channel != nil {
				entries = append(entries, channelPeer{Channel: Channel{ID: strconv.FormatInt(channel.ID, 10), Title: channel.Title, Username: channel.Username}, AccessHash: channel.AccessHash})
			}
		}
	}
	if !more || len(dialogs) == 0 {
		return entries, nil
	}
	last := dialogs[len(dialogs)-1]
	chats := tg.ChatClassArray(response.GetChats())
	entities := peer.NewEntities(tg.UserClassArray(response.GetUsers()).UserToMap(), chats.ChatToMap(), chats.ChannelToMap())
	input, err := entities.ExtractPeer(last.GetPeer())
	if err != nil {
		return nil, errors.New("无法读取频道列表的下一页")
	}
	for _, raw := range response.GetMessages() {
		if message, ok := raw.AsNotEmpty(); ok && message.GetID() == last.GetTopMessage() && samePeer(message.GetPeerID(), last.GetPeer()) {
			if next.offsetID == message.GetID() && next.offsetDate == message.GetDate() && next.offsetPeer.String() == input.String() {
				return nil, errors.New("频道列表分页未前进，请重新加载")
			}
			next.offsetID, next.offsetDate, next.offsetPeer = message.GetID(), message.GetDate(), input
			return entries, nil
		}
	}
	return nil, errors.New("频道列表缺少分页位置，请重新加载")
}

func samePeer(a, b tg.PeerClass) bool {
	switch a := a.(type) {
	case *tg.PeerUser:
		b, ok := b.(*tg.PeerUser)
		return ok && a.UserID == b.UserID
	case *tg.PeerChat:
		b, ok := b.(*tg.PeerChat)
		return ok && a.ChatID == b.ChatID
	case *tg.PeerChannel:
		b, ok := b.(*tg.PeerChannel)
		return ok && a.ChannelID == b.ChannelID
	}
	return false
}

func (c *Client) SaveChannels(token string, ids []string) error {
	if len(ids) > 100 {
		return ErrInput
	}
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	catalog := c.channelCatalog
	if !c.authorized || c.userID == 0 || c.sessionStore.Value() == "" {
		return ErrLogin
	}
	if catalog == nil || token != catalog.token || catalog.ownerID != c.userID || time.Now().After(catalog.expires) {
		return ErrChannelSelection
	}
	selected := channelSettings{OwnerID: c.userID, Peers: make([]channelPeer, 0, len(ids))}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		entry, exists := catalog.peers[id]
		if !exists || seen[id] {
			return ErrInput
		}
		seen[id] = true
		selected.Peers = append(selected.Peers, entry)
	}
	return c.saveSettingsLocked(c.config, selected)
}
