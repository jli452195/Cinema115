package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/service"
)

type Telegram struct {
	Token        string
	AllowedChats map[int64]bool
	HTTP         *http.Client
	BaseURL      string
	StatePath    string
	Generator    ShareGenerator
	Settings     *service.AutomationStore
	OnStatus     func(bool, string)
}

func NewTelegram(token, chats, dataDir string, generator ShareGenerator, settings *service.AutomationStore) (*Telegram, error) {
	if token == "" {
		return nil, nil
	}
	if !regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`).MatchString(token) {
		return nil, fmt.Errorf("Bot Token 格式无效，请在 Telegram 配置页面检查")
	}
	allowed := make(map[int64]bool)
	for _, raw := range strings.Split(chats, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("请在 Telegram 配置页面填写允许的数字聊天 ID，以逗号分隔")
		}
		allowed[id] = true
	}
	return &Telegram{Token: token, AllowedChats: allowed, BaseURL: "https://api.telegram.org", HTTP: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, StatePath: filepath.Join(dataDir, "telegram-state-"+strings.SplitN(token, ":", 2)[0]+".json"), Generator: generator, Settings: settings}, nil
}

type telegramMessage struct {
	Text    string `json:"text"`
	Caption string `json:"caption"`
	From    struct {
		IsBot bool `json:"is_bot"`
	} `json:"from"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}
type telegramUpdate struct {
	ID      int64            `json:"update_id"`
	Message *telegramMessage `json:"message"`
}
type pendingReply struct {
	UpdateID int64  `json:"updateID"`
	ChatID   int64  `json:"chatID"`
	Text     string `json:"text"`
}
type telegramState struct {
	Offset  int64         `json:"offset"`
	Pending *pendingReply `json:"pending,omitempty"`
	Batch   *messageBatch `json:"batch,omitempty"`
}

func (b *Telegram) call(ctx context.Context, method string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("编码 Telegram 请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.BaseURL+"/bot"+b.Token+"/"+method, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("创建 Telegram 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	// 网络错误可能包含带令牌的请求 URL，不向日志或页面原样传播。
	if err != nil {
		return fmt.Errorf("Telegram 网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("Bot Token 无效或已被撤销")
		}
		if resp.StatusCode == http.StatusConflict {
			return fmt.Errorf("Bot 被其他接收器占用，或仍启用了 Webhook")
		}
		return fmt.Errorf("Telegram 返回 HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("Telegram 返回无效数据")
	}
	if !envelope.OK {
		return fmt.Errorf("Telegram 拒绝请求")
	}
	if output != nil {
		if err := json.Unmarshal(envelope.Result, output); err != nil {
			return fmt.Errorf("解析 Telegram 消息失败")
		}
	}
	return nil
}

func (b *Telegram) save(state telegramState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.StatePath), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(b.StatePath), ".telegram-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(f.Name(), b.StatePath)
}

// Run 由主进程的退出上下文和 WaitGroup 管理，串行处理消息以限定云端并发。
func (b *Telegram) Run(ctx context.Context) error {
	state := telegramState{}
	data, err := os.ReadFile(b.StatePath)
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("Telegram 处理进度文件损坏: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取 Telegram 处理进度: %w", err)
	}
	if batch := state.Batch; batch != nil && (len(batch.Items) == 0 || batch.Next < 0 || batch.Next > len(batch.Items)) {
		return fmt.Errorf("Telegram 批次进度无效")
	}
	for ctx.Err() == nil {
		if state.Pending != nil {
			pending := state.Pending
			// 白名单变化后，不再向已移除的聊天补发旧回复。
			if !b.AllowedChats[pending.ChatID] {
				state.Offset = pending.UpdateID + 1
				state.Pending = nil
				state.Batch = nil
				if err := b.save(state); err != nil {
					return fmt.Errorf("保存 Telegram 处理进度: %w", err)
				}
				continue
			}
			if err := b.call(ctx, "sendMessage", map[string]any{"chat_id": pending.ChatID, "text": pending.Text, "link_preview_options": map[string]bool{"is_disabled": true}}, nil); err != nil {
				log.Printf("Telegram 回复失败，稍后重试（不会重复转存）")
				if !botDelay(ctx) {
					break
				}
				continue
			}
			if state.Batch == nil {
				state.Offset = pending.UpdateID + 1
			}
			state.Pending = nil
			if err := b.save(state); err != nil {
				return fmt.Errorf("保存 Telegram 回复进度: %w", err)
			}
			continue
		}
		if batch := state.Batch; batch != nil {
			if !b.AllowedChats[batch.ChatID] || batch.Next >= len(batch.Items) {
				state.Offset = batch.UpdateID + 1
				state.Batch = nil
				if err := b.save(state); err != nil {
					return fmt.Errorf("保存 Telegram 批次进度: %w", err)
				}
				continue
			}
			if err := b.processBatchLink(ctx, &state); err != nil {
				return err
			}
			continue
		}
		var updates []telegramUpdate
		if err := b.call(ctx, "getUpdates", map[string]any{"offset": state.Offset, "timeout": 25, "limit": 10, "allowed_updates": []string{"message"}}, &updates); err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("Telegram 拉取消息失败，请检查网络、令牌以及是否已设置 Webhook")
			if b.OnStatus != nil {
				b.OnStatus(false, err.Error())
			}
			if !botDelay(ctx) {
				break
			}
			continue
		}
		if b.OnStatus != nil {
			b.OnStatus(true, "")
		}
		for _, update := range updates {
			if update.ID < state.Offset {
				continue
			}
			m := update.Message
			if m == nil || m.From.IsBot || !b.AllowedChats[m.Chat.ID] {
				state.Offset = update.ID + 1
				if err := b.save(state); err != nil {
					return err
				}
				continue
			}
			text := m.Text
			if text == "" {
				text = m.Caption
			}
			links := ParseMessages(text)
			if len(links) == 0 {
				state.Offset = update.ID + 1
				if err := b.save(state); err != nil {
					return err
				}
				continue
			}
			state.Batch = &messageBatch{UpdateID: update.ID, ChatID: m.Chat.ID, Items: links}
			if len(links) > 1 {
				state.Pending = &pendingReply{UpdateID: update.ID, ChatID: m.Chat.ID, Text: fmt.Sprintf("已接收 %d 个 115 分享链接，将按顺序逐个处理并回复结果。", len(links))}
			}
			// 先保存整条消息的队列，服务重启后继续尚未开始的链接。
			if err := b.save(state); err != nil {
				return fmt.Errorf("记录 Telegram 任务: %w", err)
			}
			break
		}
	}
	return nil
}

func botDelay(ctx context.Context) bool {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
