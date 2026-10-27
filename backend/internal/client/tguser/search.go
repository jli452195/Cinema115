package tguser

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/resourcesearch"
)

type Action struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	TMDBID string `json:"tmdbId,omitempty"`
	Size   string `json:"size,omitempty"`
	Kind   string `json:"kind"`
}
type SearchResult struct {
	Text      string     `json:"text"`
	Items     []Action   `json:"items"`
	Links     []string   `json:"links"`
	Remaining *int       `json:"remaining,omitempty"`
	Page      *int       `json:"page,omitempty"`
	Empty     bool       `json:"empty"`
	AllLoaded bool       `json:"allLoaded,omitempty"`
	Cached    bool       `json:"cached"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Partial   bool       `json:"partial,omitempty"`
	Warning   string     `json:"warning,omitempty"`
}
type storedAction struct {
	expires   time.Time
	message   int
	command   string
	data      []byte
	direct    string
	kind      string
	searchKey string
}
type searchError struct{ message string }

func (e *searchError) Error() string { return e.message }

var webURL = regexp.MustCompile(`https?://[^\s<>"\x{3000}]+`)
var startPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var passwordPattern = regexp.MustCompile(`(?:提取码|访问码|密码)\s*[:：]?\s*([A-Za-z0-9]{4})`)

const botSearchTitleLimit = 18

func limitBotSearchTitle(title string) string {
	runes := []rune(title)
	if len(runes) > botSearchTitleLimit {
		return string(runes[:botSearchTitleLimit])
	}
	return title
}

func botCommand(raw, botUsername string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "https" || (u.Host != "t.me" && u.Host != "telegram.me") || !strings.EqualFold(strings.Trim(u.Path, "/"), botUsername) {
		return ""
	}
	payload := u.Query().Get("start")
	if !startPattern.MatchString(payload) {
		return ""
	}
	return "/start " + payload
}
func shareLink(raw string) string {
	raw = strings.TrimRight(raw, "。，、；）)]}")
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	info, err := p115.ParseShareURL(raw)
	if err != nil {
		return ""
	}
	return info.URL
}
func randomID() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(b[:])), nil
}
func history(ctx context.Context, api *tg.Client, peer *tg.InputPeerUser) ([]*tg.Message, error) {
	data, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: 30})
	if err != nil {
		return nil, err
	}
	modified, ok := data.AsModified()
	if !ok {
		return nil, errors.New("没有可用消息")
	}
	messages := make([]*tg.Message, 0, len(modified.GetMessages()))
	for _, raw := range modified.GetMessages() {
		if m, ok := raw.(*tg.Message); ok {
			messages = append(messages, m)
		}
	}
	return messages, nil
}
func fingerprint(m *tg.Message) [32]byte {
	raw, _ := json.Marshal(struct {
		Text     string
		Entities []tg.MessageEntityClass
		Markup   tg.ReplyMarkupClass
	}{m.Message, m.Entities, m.ReplyMarkup})
	return sha256.Sum256(raw)
}
func resolveBot(ctx context.Context, api *tg.Client, botUsername string) (*tg.InputPeerUser, error) {
	resolved, err := api.ContactsResolveUsername(ctx, botUsername)
	if err != nil {
		return nil, err
	}
	target, ok := resolved.Peer.(*tg.PeerUser)
	if !ok {
		return nil, errors.New("目标不是 Bot")
	}
	for _, raw := range resolved.Users {
		if user, ok := raw.(*tg.User); ok && user.ID == target.UserID && user.Bot {
			return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
		}
	}
	return nil, errors.New("无法读取目标 Bot")
}
func (c *Client) addAction(action storedAction, title, kind, tmdbID, size string) (Action, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return Action{}, err
	}
	id := hex.EncodeToString(bytes[:])
	action.expires = time.Now().Add(searchCacheTTL)
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, value := range c.results {
		if time.Now().After(value.expires) {
			delete(c.results, key)
		}
	}
	if len(c.results) >= 2048 {
		c.results = make(map[string]storedAction)
		c.searchCache = make(map[string]cachedSearch)
	}
	action.kind = kind
	c.results[id] = action
	return Action{ID: id, Title: title, Kind: kind, TMDBID: tmdbID, Size: size}, nil
}
func (c *Client) parse(m *tg.Message, botUsername, searchKey string) (SearchResult, bool, error) {
	message := resourcesearch.Message{ID: m.ID, Text: m.Message, Entities: make([]resourcesearch.Entity, 0, len(m.Entities))}
	links := make([]string, 0)
	seen := make(map[string]bool)
	addLink := func(value string) {
		if value = shareLink(value); value != "" && !seen[value] {
			seen[value] = true
			links = append(links, value)
		}
	}
	for _, entity := range m.Entities {
		switch e := entity.(type) {
		case *tg.MessageEntityTextURL:
			message.Entities = append(message.Entities, resourcesearch.Entity{Type: "text_link", Offset: e.Offset, Length: e.Length, URL: e.URL})
			addLink(e.URL)
		case *tg.MessageEntityURL:
			message.Entities = append(message.Entities, resourcesearch.Entity{Type: "url", Offset: e.Offset, Length: e.Length})
		}
	}
	for _, match := range webURL.FindAllString(m.Message, -1) {
		addLink(match)
	}
	parsed := resourcesearch.Parse(message)
	result := SearchResult{Text: m.Message, Items: make([]Action, 0, len(parsed.Items)), Links: links, Remaining: parsed.Remaining, Page: parsed.Page, Empty: parsed.Empty}
	// 单链接消息若把提取码单独列出，可安全地补到该链接；多链接不猜测提取码归属。
	if len(result.Links) == 1 {
		result.Links[0] = shareWithPassword(result.Links[0], m.Message)
	}
	for _, item := range parsed.Items {
		action := storedAction{message: m.ID, command: botCommand(item.URL, botUsername), direct: shareLink(item.URL), searchKey: searchKey}
		if action.command == "" && action.direct == "" {
			continue
		}
		entry, err := c.addAction(action, item.Title, "resource", item.TMDBID, item.Size)
		if err != nil {
			return result, false, err
		}
		result.Items = append(result.Items, entry)
	}
	if markup, ok := m.ReplyMarkup.(*tg.ReplyInlineMarkup); ok {
		for _, row := range markup.Rows {
			for _, raw := range row.Buttons {
				switch button := raw.(type) {
				case *tg.KeyboardButtonCallback:
					title := button.Text
					// 单向追加资源，只暴露下一页；不自动发起其他 Bot 操作。
					if !strings.Contains(title, "下一页") {
						continue
					}
					entry, err := c.addAction(storedAction{message: m.ID, data: append([]byte(nil), button.Data...), searchKey: searchKey}, title, "page", "", "")
					if err != nil {
						return result, false, err
					}
					result.Items = append(result.Items, entry)
				case *tg.KeyboardButtonURL:
					addLink(button.URL)
				}
			}
		}
	}
	// 按钮里的直达分享也属于本次回复。
	for _, value := range links {
		found := false
		for _, existing := range result.Links {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			result.Links = append(result.Links, value)
		}
	}
	recognized := parsed.Recognized || len(result.Links) > 0
	for _, text := range []string{"认证", "权限", "次数不足", "次数已用完", "订阅", "请先加入", "操作频繁", "稍后再试"} {
		if strings.Contains(m.Message, text) {
			recognized = true
		}
	}
	return result, recognized, nil
}

func shareWithPassword(link, text string) string {
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	if match := passwordPattern.FindStringSubmatch(text); len(match) > 1 && u.Query().Get("password") == "" {
		query := u.Query()
		query.Set("password", match[1])
		u.RawQuery = query.Encode()
	}
	return u.String()
}

// 搜索与取链接只向当前配置的 Bot 发送；每次先记住聊天快照，忽略历史回复。
func (c *Client) Search(ctx context.Context, title string, refresh bool) (SearchResult, error) {
	return c.SearchWithUpdates(ctx, title, refresh, nil)
}

// SearchWithUpdates 在 Bot 返回可识别内容时立即回调，最终返回值仍会收齐连续回复并写入缓存。
func (c *Client) SearchWithUpdates(ctx context.Context, title string, refresh bool, onUpdate func(SearchResult)) (SearchResult, error) {
	return c.SearchWithProgress(ctx, title, refresh, onUpdate, nil)
}

func (c *Client) SearchWithProgress(ctx context.Context, title string, refresh bool, onUpdate func(SearchResult), onProgress func(SearchProgress)) (SearchResult, error) {
	title = strings.TrimSpace(title)
	if title == "" || strings.HasPrefix(title, "/") {
		return SearchResult{}, ErrInput
	}
	query := limitBotSearchTitle(title)
	key := "search:" + query
	return c.exchange(ctx, storedAction{command: query, kind: "search", searchKey: key}, key, refresh, onUpdate, onProgress)
}
func (c *Client) Select(ctx context.Context, id string) (SearchResult, error) {
	return c.exchange(ctx, storedAction{kind: "select"}, "select:"+id, false, nil, nil)
}

// The tracked worker owns the connection gate and the operation deadline.
func (c *Client) performExchange(ctx context.Context, action storedAction, key string, refresh bool, onUpdate func(SearchResult)) (SearchResult, error) {
	result := SearchResult{Items: []Action{}, Links: []string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	c.mu.Lock()
	botUsername := c.config.Bot
	c.mu.Unlock()
	if !validBotUsername(botUsername) {
		return result, ErrBotConfig
	}
	if strings.HasPrefix(key, "select:") {
		c.mu.Lock()
		selected, ok := c.results[strings.TrimPrefix(key, "select:")]
		c.mu.Unlock()
		if !ok || !time.Now().Before(selected.expires) {
			return result, &searchError{"搜索结果已过期，请刷新搜索"}
		}
		action = selected
		if action.direct != "" {
			return SearchResult{Items: []Action{}, Links: []string{action.direct}}, nil
		}
	}
	if !refresh {
		if cached, ok := c.cachedResult(key, time.Now()); ok {
			if action.kind == "page" {
				if aggregate, exists := c.cachedResult(action.searchKey, time.Now()); exists {
					return aggregate, nil
				}
			}
			return cached, nil
		}
	}
	c.mu.Lock()
	cooldownErr := c.cooldownErrorLocked()
	c.mu.Unlock()
	if cooldownErr != nil {
		return result, cooldownErr
	}
	dispatched := false
	received := false
	err := c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		if !c.View().Authorized {
			return ErrLogin
		}
		api := client.API()
		peer, err := resolveBot(ctx, api, botUsername)
		if err != nil {
			return err
		}
		before, err := history(ctx, api, peer)
		if err != nil {
			return err
		}
		snapshot := make(map[int][32]byte, len(before))
		maxID := 0
		for _, m := range before {
			snapshot[m.ID] = fingerprint(m)
			if m.ID > maxID {
				maxID = m.ID
			}
		}
		requestID := 0
		if action.data != nil {
			dispatched = true
			_, err = api.MessagesGetBotCallbackAnswer(ctx, &tg.MessagesGetBotCallbackAnswerRequest{Peer: peer, MsgID: action.message, Data: action.data})
		} else {
			id, e := randomID()
			if e != nil {
				return e
			}
			dispatched = true
			var updates tg.UpdatesClass
			updates, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: action.command, RandomID: id, NoWebpage: true})
			requestID = sentMessageID(updates, id)
		}
		if err != nil {
			// A definitive RPC rejection did not start a query. Transport failures are ambiguous.
			if rpcErr, ok := tgerr.As(err); ok && rpcErr.Code >= 400 && rpcErr.Code < 500 && !tgerr.Is(err, "BOT_RESPONSE_TIMEOUT") {
				dispatched = false
			}
			return err
		}
		timer := time.NewTimer(c.searchTiming.firstPoll)
		defer timer.Stop()
		var lastChange time.Time
		var recognized bool
		seenMessages := make(map[int][32]byte)
		replies := make(map[int]SearchResult)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
			messages, e := history(ctx, api, peer)
			if e != nil {
				return e
			}
			sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
			for _, m := range messages {
				if m.Out {
					continue
				}
				if from, ok := m.FromID.(*tg.PeerUser); ok && from.UserID != peer.UserID {
					continue
				}
				fp := fingerprint(m)
				old, exists := snapshot[m.ID]
				if m.ID <= maxID && (!exists || fp == old) {
					continue
				}
				if !belongsToExchange(m, action, requestID, maxID, replies) {
					continue
				}
				if previous, ok := seenMessages[m.ID]; ok && previous == fp {
					continue
				}
				parsed, ok, e := c.parse(m, botUsername, action.searchKey)
				if e != nil {
					return e
				}
				seenMessages[m.ID] = fp
				received = true
				if !ok {
					continue
				}
				replies[m.ID] = parsed
				result = combineReplies(replies)
				if action.kind == "search" || action.kind == "page" {
					result.AllLoaded = !hasNextAction(result)
				}
				recognized = true
				lastChange = time.Now()
				if onUpdate != nil {
					onUpdate(c.previewAppendedResult(action.searchKey, result, time.Now()))
				}
			}
			// 明确的空结果是终态，可以立即返回；资源结果等待短暂静默，
			// 将 Bot 连续发出的多个分享链接一起收齐。
			if searchReplyComplete(result, recognized, lastChange, time.Now(), c.searchTiming.quiet) {
				return nil
			}
			timer.Reset(c.searchTiming.poll)
		}
	})
	if dispatched && err != nil {
		c.mu.Lock()
		c.cooldown = time.Now().Add(c.searchTiming.quarantine)
		until := c.cooldown
		c.mu.Unlock()
		code, message := "interrupted", "Bot 操作未完整结束，正在隔离迟到回复"
		if errors.Is(err, context.DeadlineExceeded) {
			code, message = "reply_timeout", "等待 Bot 回复超时，正在隔离迟到回复"
			if received {
				code, message = "unrecognized_reply", "已收到 Bot 消息，但未能完成资源识别，正在隔离后续回复"
			}
		}
		if len(result.Items) > 0 || len(result.Links) > 0 {
			result.Partial, result.AllLoaded = true, false
			result.Warning = "已保留收到的资源，后续回复未收集完整；稍后可刷新补充"
			code, message = "partial_result", result.Warning
		}
		err = &OperationError{Code: code, Message: message, RetryAt: &until}
	} else if errors.Is(err, context.DeadlineExceeded) {
		err = &OperationError{Code: "connection_timeout", Message: "连接 Telegram 超时，本次 Bot 查询尚未发送"}
	}
	var expected *searchError
	if errors.As(err, &expected) {
		return result, err
	}
	if err == nil || result.Partial {
		now := time.Now()
		switch action.kind {
		case "search":
			result = c.appendSearchResult(action.searchKey, result, now)
		case "page":
			result = c.appendSearchResult(action.searchKey, result, now)
			if err == nil {
				c.cacheResult(key, result, now)
			}
		default:
			result = c.cacheResult(key, result, now)
		}
	}
	return result, publicError(err)
}

func sentMessageID(updates tg.UpdatesClass, randomID int64) int {
	if sent, ok := updates.(*tg.UpdateShortSentMessage); ok {
		return sent.ID
	}
	var items []tg.UpdateClass
	switch value := updates.(type) {
	case *tg.Updates:
		items = value.Updates
	case *tg.UpdatesCombined:
		items = value.Updates
	case *tg.UpdateShort:
		items = []tg.UpdateClass{value.Update}
	}
	for _, item := range items {
		if id, ok := item.(*tg.UpdateMessageID); ok && id.RandomID == randomID {
			return id.ID
		}
	}
	return 0
}

func belongsToExchange(m *tg.Message, action storedAction, requestID, maxID int, replies map[int]SearchResult) bool {
	// Only a callback may edit its selected historical message.
	if m.ID <= maxID {
		return action.data != nil && m.ID == action.message
	}
	if requestID > 0 && m.ID <= requestID {
		return false
	}
	if reply, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok && reply.ReplyToMsgID > 0 {
		if action.data != nil && reply.ReplyToMsgID == action.message {
			return true
		}
		if requestID > 0 && reply.ReplyToMsgID == requestID {
			return true
		}
		_, linked := replies[reply.ReplyToMsgID]
		return linked
	}
	// Some bots omit reply_to. Keep serial execution and quarantine for this fallback.
	return true
}

func hasNextAction(result SearchResult) bool {
	for _, item := range result.Items {
		if item.Kind == "page" && strings.Contains(item.Title, "下一页") {
			return true
		}
	}
	return false
}

func searchReplyComplete(result SearchResult, recognized bool, lastChange, now time.Time, quiet time.Duration) bool {
	if !recognized {
		return false
	}
	if result.Empty {
		return true
	}
	return !lastChange.IsZero() && now.Sub(lastChange) >= quiet
}

// 同一条消息的编辑覆盖旧内容，多条回复则合并，避免丢失后续分享或保留旧链接。
func combineReplies(replies map[int]SearchResult) SearchResult {
	ids := make([]int, 0, len(replies))
	for id := range replies {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	result := SearchResult{Items: []Action{}, Links: []string{}}
	seen := make(map[string]bool)
	for _, id := range ids {
		part := replies[id]
		if result.Text != "" {
			result.Text += "\n\n"
		}
		result.Text += part.Text
		result.Items = append(result.Items, part.Items...)
		for _, link := range part.Links {
			if !seen[link] {
				seen[link] = true
				result.Links = append(result.Links, link)
			}
		}
		if part.Remaining != nil {
			result.Remaining = part.Remaining
		}
		if part.Page != nil {
			result.Page = part.Page
		}
		result.Empty = part.Empty
	}
	if len(result.Items) > 0 || len(result.Links) > 0 {
		result.Empty = false
	}
	return result
}
