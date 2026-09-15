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
	Cached    bool       `json:"cached"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
type storedAction struct {
	expires time.Time
	message int
	command string
	data    []byte
	direct  string
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

func botCommand(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "https" || (u.Host != "t.me" && u.Host != "telegram.me") || !strings.EqualFold(strings.Trim(u.Path, "/"), BotUsername) {
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
func resolveBot(ctx context.Context, api *tg.Client) (*tg.InputPeerUser, error) {
	resolved, err := api.ContactsResolveUsername(ctx, BotUsername)
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
	if len(c.results) >= 200 {
		c.results = make(map[string]storedAction)
	}
	c.results[id] = action
	return Action{ID: id, Title: title, Kind: kind, TMDBID: tmdbID, Size: size}, nil
}
func (c *Client) parse(m *tg.Message) (SearchResult, bool, error) {
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
		if match := passwordPattern.FindStringSubmatch(m.Message); len(match) > 1 {
			u, _ := url.Parse(result.Links[0])
			q := u.Query()
			if q.Get("password") == "" {
				q.Set("password", match[1])
				u.RawQuery = q.Encode()
				result.Links[0] = u.String()
			}
		}
	}
	for _, item := range parsed.Items {
		action := storedAction{message: m.ID, command: botCommand(item.URL), direct: shareLink(item.URL)}
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
					// 不自动发起悬赏、支付、订阅或权限申请，只暴露资源与翻页按钮。
					if !strings.Contains(title, "下一页") && !strings.Contains(title, "上一页") {
						continue
					}
					entry, err := c.addAction(storedAction{message: m.ID, data: append([]byte(nil), button.Data...)}, title, "page", "", "")
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

// 搜索与取链接只向固定的 ayclub_bot 发送；每次先记住聊天快照，忽略历史回复。
func (c *Client) Search(ctx context.Context, title string, refresh bool) (SearchResult, error) {
	title = strings.TrimSpace(title)
	if title == "" || strings.HasPrefix(title, "/") {
		return SearchResult{}, ErrInput
	}
	query := limitBotSearchTitle(title)
	return c.exchange(ctx, storedAction{command: query}, "search:"+query, refresh)
}
func (c *Client) Select(ctx context.Context, id string) (SearchResult, error) {
	return c.exchange(ctx, storedAction{}, "select:"+id, false)
}
func (c *Client) exchange(ctx context.Context, action storedAction, key string, refresh bool) (SearchResult, error) {
	result := SearchResult{Items: []Action{}, Links: []string{}}
	if err := c.lock(); err != nil {
		return result, err
	}
	defer c.unlock()
	if err := ctx.Err(); err != nil {
		return result, err
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
			return cached, nil
		}
	}
	if time.Now().Before(c.cooldown) {
		return result, &searchError{"上一项请求超时，请等待一分钟再搜索，避免迟到的回复混入"}
	}
	dispatched := false
	err := c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		if !c.View().Authorized {
			return ErrLogin
		}
		api := client.API()
		peer, err := resolveBot(ctx, api)
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
		dispatched = true
		if action.data != nil {
			_, err = api.MessagesGetBotCallbackAnswer(ctx, &tg.MessagesGetBotCallbackAnswerRequest{Peer: peer, MsgID: action.message, Data: action.data})
		} else {
			id, e := randomID()
			if e != nil {
				return e
			}
			_, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: action.command, RandomID: id, NoWebpage: true})
		}
		if err != nil {
			return err
		}
		timer := time.NewTimer(800 * time.Millisecond)
		defer timer.Stop()
		var lastChange time.Time
		var recognized bool
		seenMessages := make(map[int][32]byte)
		replies := make(map[int]SearchResult)
		for {
			select {
			case <-ctx.Done():
				c.cooldown = time.Now().Add(time.Minute)
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
				if previous, ok := seenMessages[m.ID]; ok && previous == fp {
					continue
				}
				parsed, ok, e := c.parse(m)
				if e != nil {
					return e
				}
				seenMessages[m.ID] = fp
				if !ok {
					continue
				}
				replies[m.ID] = parsed
				result = combineReplies(replies)
				recognized = true
				lastChange = time.Now()
			}
			// 等待短暂静默，将 Bot 连续发出的多个分享链接一起收齐。
			if recognized && time.Since(lastChange) >= 2*time.Second {
				return nil
			}
			timer.Reset(time.Second)
		}
	})
	if dispatched && err != nil {
		c.cooldown = time.Now().Add(time.Minute)
	}
	var expected *searchError
	if errors.As(err, &expected) {
		return result, err
	}
	if err == nil {
		result = c.cacheResult(key, result, time.Now())
	}
	return result, publicError(err)
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
