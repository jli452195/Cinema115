package tguser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"golang.org/x/sync/errgroup"
	"strm115/backend/internal/client/p115"
)

const channelSearchTTL = 10 * time.Minute
const channelSearchPageSize = 20
const channelSearchBatchSize = 6
const channelSearchItemLimit = 1000

type ChannelLink struct {
	Type     string           `json:"type"`
	URL      string           `json:"url"`
	Metadata *ChannelMetadata `json:"metadata,omitempty"`
}
type ChannelResource struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Text      string           `json:"text"`
	Channel   Channel          `json:"channel"`
	SourceURL string           `json:"sourceUrl"`
	PostedAt  time.Time        `json:"postedAt"`
	Links     []ChannelLink    `json:"links"`
	Metadata  *ChannelMetadata `json:"metadata,omitempty"`
}
type ChannelSearchWarning struct {
	Channel Channel `json:"channel"`
	Error   string  `json:"error"`
}
type ChannelSearchResult struct {
	Items     []ChannelResource      `json:"items"`
	Warnings  []ChannelSearchWarning `json:"warnings"`
	Cursor    string                 `json:"cursor"`
	HasMore   bool                   `json:"hasMore"`
	Partial   bool                   `json:"partial"`
	Cached    bool                   `json:"cached"`
	ExpiresAt time.Time              `json:"expiresAt"`
	RetryAt   *time.Time             `json:"retryAt,omitempty"`
	Truncated bool                   `json:"truncated,omitempty"`
}
type channelPosition struct {
	peer    channelPeer
	offset  int
	done    bool
	warning string
}
type channelSearch struct {
	key, fingerprint       string
	ownerID                int64
	expires                time.Time
	positions              []channelPosition
	next                   int
	cursor, previousCursor string
	result                 ChannelSearchResult
}

func channelFingerprint(settings channelSettings) string {
	data, _ := json.Marshal(settings)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// SearchChannels never sends messages or invokes callbacks. Each request reads
// a bounded batch; pagination and failed channels retain their own offsets.
func (c *Client) SearchChannels(ctx context.Context, title, cursor string, refresh bool) (ChannelSearchResult, error) {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 256 || len(cursor) > 64 || (refresh && cursor != "") {
		return ChannelSearchResult{}, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return ChannelSearchResult{}, err
	}
	if err := c.lock(); err != nil {
		return ChannelSearchResult{}, err
	}
	defer c.unlock()
	c.mu.Lock()
	if c.sessionStore.Value() == "" {
		c.mu.Unlock()
		return ChannelSearchResult{}, ErrLogin
	}
	now := time.Now()
	fingerprint := channelFingerprint(c.channels)
	for key, search := range c.channelSearches {
		if now.After(search.expires) || search.fingerprint != fingerprint {
			delete(c.channelSearches, key)
		}
	}
	var search *channelSearch
	for _, candidate := range c.channelSearches {
		if candidate.key != title {
			continue
		}
		if cursor != "" && (candidate.cursor == cursor || candidate.previousCursor == cursor) {
			search = candidate
			break
		}
		if cursor == "" && !refresh {
			search = candidate
			break
		}
	}
	if search != nil && (cursor == "" || cursor == search.previousCursor) {
		result := search.result
		result.Cached = true
		c.mu.Unlock()
		return result, nil
	}
	if cursor != "" && search == nil {
		c.mu.Unlock()
		return ChannelSearchResult{}, &OperationError{Code: "channel_search_expired", Message: "频道搜索已过期或配置已改变，请重新搜索"}
	}
	if now.Before(c.channelRetryAt) {
		retryAt := c.channelRetryAt
		c.mu.Unlock()
		return ChannelSearchResult{}, &OperationError{Code: "channel_search_limited", Message: "Telegram 搜索频率受限，请稍后再试", RetryAt: &retryAt}
	}
	c.mu.Unlock()

	var result ChannelSearchResult
	err := c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		c.mu.Lock()
		if !c.authorized {
			c.mu.Unlock()
			return ErrLogin
		}
		if len(c.channels.Peers) == 0 || c.channels.OwnerID != c.userID {
			c.mu.Unlock()
			return &OperationError{Code: "channels_unconfigured", Message: "请先在 TG 频道配置中选择搜索频道"}
		}
		if search != nil && (search.ownerID != c.userID || search.fingerprint != channelFingerprint(c.channels)) {
			c.mu.Unlock()
			return &OperationError{Code: "channel_search_expired", Message: "频道账号或配置已改变，请重新搜索"}
		}
		if search == nil {
			search = &channelSearch{key: title, fingerprint: channelFingerprint(c.channels), ownerID: c.userID, expires: time.Now().Add(channelSearchTTL), positions: make([]channelPosition, len(c.channels.Peers))}
			for i, p := range c.channels.Peers {
				search.positions[i].peer = p
			}
		}
		// Work on copies: cancellation or a lost connection must leave the cursor retryable.
		next := *search
		next.positions = append([]channelPosition(nil), search.positions...)
		c.mu.Unlock()
		indices := make([]int, 0, channelSearchBatchSize)
		for step := 0; step < len(next.positions) && len(indices) < channelSearchBatchSize; step++ {
			i := (next.next + step) % len(next.positions)
			if !next.positions[i].done {
				indices = append(indices, i)
			}
		}
		type page struct {
			items    []ChannelResource
			position channelPosition
			retry    time.Time
		}
		pages := make([]page, len(indices))
		var limited atomic.Bool
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(3)
		for j, index := range indices {
			group.Go(func() error {
				position := next.positions[index]
				pages[j].position = position
				if limited.Load() {
					return nil
				}
				requestCtx, cancel := context.WithTimeout(groupCtx, 8*time.Second)
				defer cancel()
				items, offset, done, err := searchChannelPage(requestCtx, client.API(), position.peer, title, position.offset)
				if groupCtx.Err() != nil {
					return groupCtx.Err()
				}
				if err != nil {
					if wait, ok := tgerr.AsFloodWait(err); ok {
						limited.Store(true)
						pages[j].retry = time.Now().Add(wait)
					}
					position.warning = publicError(err).Error()
					if errors.Is(err, context.DeadlineExceeded) {
						position.warning = "频道读取超时，可加载更多重试"
					}
					if tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHAT_ADMIN_REQUIRED", "USER_BANNED_IN_CHANNEL") {
						position.done = true
						position.warning = "当前账号无法读取此频道，请检查频道配置"
					}
				} else {
					position.offset, position.done, position.warning = offset, done, ""
					pages[j].items = items
				}
				pages[j].position = position
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		items := make([]ChannelResource, 0, len(next.result.Items)+len(indices)*channelSearchPageSize)
		items = append(items, next.result.Items...)
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			seen[item.ID] = true
		}
		var retryAt time.Time
		for j, page := range pages {
			next.positions[indices[j]] = page.position
			if page.retry.After(retryAt) {
				retryAt = page.retry
			}
			for _, item := range page.items {
				if !seen[item.ID] {
					seen[item.ID] = true
					items = append(items, item)
				}
			}
		}
		// Keep older results fixed; sort only the newly fetched messages.
		sort.SliceStable(items[len(next.result.Items):], func(i, j int) bool {
			offset := len(next.result.Items)
			return items[offset+i].PostedAt.After(items[offset+j].PostedAt)
		})
		if len(indices) > 0 {
			next.next = (indices[len(indices)-1] + 1) % len(next.positions)
		}
		warnings := make([]ChannelSearchWarning, 0)
		hasMore := false
		for _, position := range next.positions {
			hasMore = hasMore || !position.done
			if position.warning != "" {
				warnings = append(warnings, ChannelSearchWarning{Channel: position.peer.Channel, Error: position.warning})
			}
		}
		truncated := len(items) > channelSearchItemLimit || (len(items) == channelSearchItemLimit && hasMore)
		if truncated {
			items = items[:channelSearchItemLimit]
			hasMore = false
		}
		token, err := channelToken()
		if err != nil {
			return err
		}
		next.previousCursor, next.cursor = cursor, ""
		if hasMore {
			next.cursor = token
		}
		next.result = ChannelSearchResult{Items: items, Warnings: warnings, Cursor: next.cursor, HasMore: hasMore, Partial: len(warnings) > 0, ExpiresAt: next.expires, Truncated: truncated}
		if !retryAt.IsZero() {
			next.result.RetryAt = &retryAt
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.channelSearches == nil {
			c.channelSearches = make(map[string]*channelSearch)
		}
		if len(c.channelSearches) >= 16 {
			var oldest string
			var expiry time.Time
			for id, cached := range c.channelSearches {
				if oldest == "" || cached.expires.Before(expiry) {
					oldest, expiry = id, cached.expires
				}
			}
			delete(c.channelSearches, oldest)
		}
		for id, cached := range c.channelSearches {
			if cached == search || (refresh && cached.key == title) {
				delete(c.channelSearches, id)
			}
		}
		c.channelSearches[token] = &next
		if retryAt.After(c.channelRetryAt) {
			c.channelRetryAt = retryAt
		}
		result = next.result
		return nil
	})
	return result, publicError(err)
}

func searchChannelPage(ctx context.Context, api *tg.Client, channel channelPeer, query string, offset int) ([]ChannelResource, int, bool, error) {
	id, err := strconv.ParseInt(channel.ID, 10, 64)
	if err != nil || id <= 0 {
		return nil, offset, false, ErrInput
	}
	response, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: &tg.InputPeerChannel{ChannelID: id, AccessHash: channel.AccessHash}, Q: query, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offset, Limit: channelSearchPageSize})
	if err != nil {
		return nil, offset, false, err
	}
	modified, ok := response.AsModified()
	if !ok {
		return nil, offset, false, errors.New("频道未返回可用消息")
	}
	items := make([]ChannelResource, 0, len(modified.GetMessages()))
	last := 0
	for _, raw := range modified.GetMessages() {
		message, ok := raw.(*tg.Message)
		if !ok || message.ID <= 0 || !samePeer(message.PeerID, &tg.PeerChannel{ChannelID: id}) {
			continue
		}
		if offset > 0 && message.ID >= offset {
			continue
		}
		if last == 0 || message.ID < last {
			last = message.ID
		}
		if item, ok := channelMessage(message, channel.Channel, query); ok {
			items = append(items, item)
		}
	}
	done := len(modified.GetMessages()) < channelSearchPageSize
	if _, full := response.(*tg.MessagesMessages); full {
		done = true
	}
	if len(modified.GetMessages()) > 0 && last == 0 && !done {
		return nil, offset, false, errors.New("频道分页未前进")
	}
	if last > 0 {
		offset = last
	}
	return items, offset, done, nil
}

var channelURLPattern = regexp.MustCompile(`(?i)(?:https?://[^\s<>"\x{3000}]+|ed2k://\|file\|[^\r\n<>]+?\|/)`)
var channelUsernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

func channelMessage(message *tg.Message, channel Channel, query string) (ChannelResource, bool) {
	links := make([]ChannelLink, 0)
	seen := make(map[string]bool)
	add := func(raw string) {
		value, kind := shareLink(raw), "115"
		if value == "" && strings.HasPrefix(strings.ToLower(raw), "ed2k://") {
			if resource, err := p115.ParseCloudResource(raw); err == nil && resource.Protocol == "ed2k" {
				value, kind = raw, "ed2k"
			}
		}
		if value != "" && !seen[value] {
			seen[value] = true
			links = append(links, ChannelLink{Type: kind, URL: value})
		}
	}
	for _, raw := range channelURLPattern.FindAllString(message.Message, -1) {
		add(raw)
	}
	for _, raw := range message.Entities {
		if entity, ok := raw.(*tg.MessageEntityTextURL); ok {
			add(entity.URL)
		}
	}
	if markup, ok := message.ReplyMarkup.(*tg.ReplyInlineMarkup); ok {
		for _, row := range markup.Rows {
			for _, raw := range row.Buttons {
				if button, ok := raw.(*tg.KeyboardButtonURL); ok {
					add(button.URL)
				}
			}
		}
	}
	if len(links) == 0 {
		return ChannelResource{}, false
	}
	if query != "" && !channelRelevant(message.Message, links, query) {
		return ChannelResource{}, false
	}
	// Only one share can safely inherit a separate password from the message.
	shareCount, shareIndex := 0, 0
	for i, link := range links {
		if link.Type == "115" {
			shareCount++
			shareIndex = i
		}
	}
	if shareCount == 1 {
		links[shareIndex].URL = shareWithPassword(links[shareIndex].URL, message.Message)
	}
	title := "频道资源"
	queryKey := channelTextKey(query)
	for _, line := range strings.Split(message.Message, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(strings.ToLower(line), "http") && !strings.HasPrefix(strings.ToLower(line), "ed2k:") {
			if title == "频道资源" {
				title = line
			}
			if queryKey != "" && strings.Contains(channelTextKey(line), queryKey) {
				title = line
				break
			}
		}
	}
	if runes := []rune(title); len(runes) > 240 {
		title = string(runes[:240])
	}
	metadata := channelMetadata(title, message.Message, links)
	title = channelDisplayTitle(title, metadata)
	path := "c/" + channel.ID
	if channelUsernamePattern.MatchString(channel.Username) {
		path = channel.Username
	}
	return ChannelResource{ID: channel.ID + ":" + strconv.Itoa(message.ID), Title: title, Text: message.Message, Channel: channel, SourceURL: "https://t.me/" + path + "/" + strconv.Itoa(message.ID), PostedAt: time.Unix(int64(message.Date), 0).UTC(), Links: links, Metadata: metadata}, true
}
