package tguser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type memoryStore struct{ value string }

func (s *memoryStore) Value() string           { return s.value }
func (s *memoryStore) Save(value string) error { s.value = value; return nil }
func testClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(&memoryStore{}, &memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestAccountConfigAndSession(t *testing.T) {
	legacyConfig := `{"apiId":123,"apiHash":"` + strings.Repeat("b", 32) + `"}`
	legacy, err := New(&memoryStore{value: legacyConfig}, &memoryStore{})
	if err != nil || legacy.View().Bot != DefaultBotUsername || !legacy.View().Configured {
		t.Fatal("旧配置没有迁移默认 Bot", err)
	}
	c := testClient(t)
	secret := strings.Repeat("a", 32)
	if err := c.Save(Config{APIID: 123, APIHash: secret}); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(Config{APIID: 123}); err != nil {
		t.Fatal("留空应保留 hash", err)
	}
	data, _ := json.Marshal(c.View())
	if strings.Contains(string(data), secret) {
		t.Fatal("配置回显 API Hash")
	}
	restored, err := New(c.configStore, c.sessionStore)
	if err != nil || !restored.View().Configured || restored.View().Bot != DefaultBotUsername {
		t.Fatal("配置不能恢复")
	}
	if err := c.Save(Config{APIID: 123, Bot: "@replacement_bot"}); err != nil || c.View().Bot != "replacement_bot" {
		t.Fatal("Bot 用户名不能独立修改", err)
	}
	if err := c.Save(Config{APIID: 123, Bot: "not-a-bot"}); !errors.Is(err, ErrInput) {
		t.Fatal("无效 Bot 用户名未被拒绝")
	}
	store := encryptedSession{c.sessionStore}
	if err := store.StoreSession(context.Background(), []byte("session-bytes")); err != nil {
		t.Fatal(err)
	}
	value, err := store.LoadSession(context.Background())
	if err != nil || string(value) != "session-bytes" {
		t.Fatal("会话不能恢复")
	}
	if err := c.Save(Config{APIID: 456, APIHash: secret}); err == nil {
		t.Fatal("带会话不能更换 API 身份")
	}
	if err := c.lock(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(c.lock(), ErrBusy) {
		t.Fatal("没有阻止并发操作")
	}
	c.unlock()
	if !errors.Is(publicError(context.Canceled), context.Canceled) {
		t.Fatal("取消错误丢失")
	}
	if strings.Contains(publicError(errors.New("private "+secret)).Error(), secret) {
		t.Fatal("底层错误泄密")
	}
	if publicError(&tgerr.Error{Type: "PHONE_CODE_INVALID"}).Error() != "验证码不正确" {
		t.Fatal("验证码错误映射失败")
	}
}
func TestBotLinksAndMultipleReplies(t *testing.T) {
	c := testClient(t)
	title := "[动画电影] 天气之子 (2019) {tmdb-568160} 4K 48.43G"
	text := "📁 本月剩余次数：734\n资源列表\n🔗 " + title + "\n第 1 页"
	offset := len(utf16.Encode([]rune(text[:strings.Index(text, title)])))
	message := &tg.Message{ID: 10, Message: text, Entities: []tg.MessageEntityClass{&tg.MessageEntityTextURL{Offset: offset, Length: len(utf16.Encode([]rune(title))), URL: "https://t.me/ayclub_bot?start=resource_1"}}, ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "下一页", Data: []byte("page2")}, &tg.KeyboardButtonCallback{Text: "悬赏资源", Data: []byte("pay")}}}}}}
	result, ok, err := c.parse(message, DefaultBotUsername, "search:天气之子")
	if err != nil || !ok || len(result.Items) != 2 {
		t.Fatal("资源/分页识别失败", err)
	}
	action := c.results[result.Items[0].ID]
	if action.command != "/start resource_1" || result.Items[0].TMDBID != "568160" {
		t.Fatal("Bot 命令或 TMDB ID 错误")
	}
	if result.Items[1].Kind != "page" || result.Items[1].Title != "下一页" {
		t.Fatal("未过滤悬赏按钮")
	}
	reply, ok, err := c.parse(&tg.Message{ID: 11, Message: "https://115cdn.com/s/one?password=abcd\nhttps://115.com/s/two?password=efgh"}, DefaultBotUsername, "")
	if err != nil || !ok || len(reply.Links) != 2 {
		t.Fatal("多分享解析失败")
	}
	single, _, _ := c.parse(&tg.Message{ID: 12, Message: "https://115.com/s/one\n提取码：abcd"}, DefaultBotUsername, "")
	if len(single.Links) != 1 || !strings.Contains(single.Links[0], "password=abcd") {
		t.Fatal("独立提取码没有补齐")
	}
	denied, ok, _ := c.parse(&tg.Message{ID: 13, Message: "请先完成认证"}, DefaultBotUsername, "")
	if !ok || len(denied.Links) != 0 {
		t.Fatal("认证失败回复不应变成资源")
	}
	for _, u := range []string{"https://t.me/other_bot?start=x", "https://evil.example/ayclub_bot?start=x", "https://t.me/ayclub_bot?start=%2Fpay", "https://t.me/ayclub_bot?start="} {
		if botCommand(u, DefaultBotUsername) != "" {
			t.Fatal("允许了非授权命令")
		}
	}
	if botCommand("https://t.me/replacement_bot?start=resource_1", "replacement_bot") != "/start resource_1" {
		t.Fatal("自定义 Bot 深链接未被接受")
	}
	for _, u := range []string{"javascript:alert(1)", "https://115.com.evil.example/s/x", "https://user@115.com/s/x"} {
		if shareLink(u) != "" {
			t.Fatal("非法分享链接未过滤")
		}
	}
	c.results["expired"] = storedAction{expires: time.Now().Add(-time.Minute)}
	if _, err := c.Select(context.Background(), "expired"); err == nil {
		t.Fatal("过期动作未拦截")
	}
}

func TestEditedAndMultipleReplies(t *testing.T) {
	replies := map[int]SearchResult{1: {Text: "第一条", Links: []string{"https://115.com/s/one"}}, 2: {Text: "第二条", Links: []string{"https://115.com/s/two"}}}
	if result := combineReplies(replies); len(result.Links) != 2 || result.Text != "第一条\n\n第二条" {
		t.Fatal("多条 Bot 回复未完整合并")
	}
	replies[1] = SearchResult{Text: "编辑后", Links: []string{"https://115.com/s/two"}}
	if result := combineReplies(replies); len(result.Links) != 1 || strings.Contains(result.Text, "第一条") {
		t.Fatal("消息编辑后残留旧内容")
	}
}

func TestParseOnlyExposesNextPage(t *testing.T) {
	c := testClient(t)
	message := &tg.Message{ID: 20, Message: "资源列表", ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{
		&tg.KeyboardButtonCallback{Text: "上一页", Data: []byte("previous")},
		&tg.KeyboardButtonCallback{Text: "下一页", Data: []byte("next")},
	}}}}}
	result, _, err := c.parse(message, DefaultBotUsername, "search:测试")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Title != "下一页" {
		t.Fatalf("应只暴露下一页操作：%+v", result.Items)
	}
	stored := c.results[result.Items[0].ID]
	if stored.kind != "page" || stored.searchKey != "search:测试" {
		t.Fatalf("分页动作没有关联原搜索：%+v", stored)
	}
}

func TestSearchReplyComplete(t *testing.T) {
	now := time.Now()
	if searchReplyComplete(SearchResult{}, false, time.Time{}, now) {
		t.Fatal("未识别的回复不能结束搜索")
	}
	if !searchReplyComplete(SearchResult{Empty: true}, true, now, now) {
		t.Fatal("明确的空结果应立即结束搜索")
	}
	result := SearchResult{Links: []string{"https://115.com/s/demo"}}
	if searchReplyComplete(result, true, now.Add(-time.Second), now) {
		t.Fatal("资源回复仍应等待连续消息")
	}
	if !searchReplyComplete(result, true, now.Add(-2*time.Second), now) {
		t.Fatal("资源回复静默两秒后应结束搜索")
	}
}
