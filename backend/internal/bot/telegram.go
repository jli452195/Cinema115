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
	Logs         *service.GenerationLogs
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
	ID              int64            `json:"message_id"`
	ThreadID        int64            `json:"message_thread_id"`
	Text            string           `json:"text"`
	Caption         string           `json:"caption"`
	Entities        []telegramEntity `json:"entities"`
	CaptionEntities []telegramEntity `json:"caption_entities"`
	From            struct {
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
	UpdateID      int64  `json:"updateID"`
	ChatID        int64  `json:"chatID"`
	Text          string `json:"text"`
	MessageID     int64  `json:"messageID,omitempty"`
	ThreadID      int64  `json:"threadID,omitempty"`
	PreviewURL    string `json:"previewURL,omitempty"`
	EditMessageID int64  `json:"editMessageID,omitempty"`
}

// 引用当前聊天收到的原消息，转发来源的消息编号不能用于当前聊天的回复。
func (p *pendingReply) request() map[string]any {
	input := map[string]any{"chat_id": p.ChatID, "text": p.Text, "link_preview_options": map[string]any{"is_disabled": true}}
	if p.MessageID > 0 {
		input["reply_parameters"] = map[string]any{"message_id": p.MessageID, "allow_sending_without_reply": true}
	}
	if p.ThreadID > 0 {
		input["message_thread_id"] = p.ThreadID
	}
	if p.PreviewURL != "" {
		input["link_preview_options"] = map[string]any{"is_disabled": false, "url": p.PreviewURL}
	}
	if p.EditMessageID > 0 {
		input["message_id"] = p.EditMessageID
		delete(input, "reply_parameters")
		delete(input, "message_thread_id")
	}
	return input
}

var errTelegramEditMessageMissing = errors.New("待更新的 Telegram 消息已删除")

type telegramState struct {
	Replies []*pendingReply `json:"replies,omitempty"`
	Queue   []*messageBatch `json:"queue,omitempty"`
	Offset  int64           `json:"offset"`
	Pending *pendingReply   `json:"pending,omitempty"`
	Batch   *messageBatch   `json:"batch,omitempty"`
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
	// 编辑成功但本地尚未保存时，重试会返回内容未变化，应视为已经完成。
	if resp.StatusCode == http.StatusBadRequest && method == "editMessageText" {
		var failure struct {
			Description string `json:"description"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&failure) == nil {
			description := strings.ToLower(failure.Description)
			if strings.Contains(description, "message is not modified") {
				return nil
			}
			if strings.Contains(description, "message to edit not found") {
				return errTelegramEditMessageMissing
			}
		}
		return fmt.Errorf("Telegram 更新消息失败（HTTP 400）")
	}
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
	for _, batch := range state.Queue {
		if batch == nil || len(batch.Items) == 0 || batch.Next != 0 {
			return fmt.Errorf("Telegram 等待队列无效")
		}
	}
	for _, reply := range state.Replies {
		if reply == nil {
			return fmt.Errorf("Telegram 批次回复队列无效")
		}
	}
	replyFailed := false
	for ctx.Err() == nil {
		if state.Pending == nil && len(state.Replies) > 0 {
			state.Pending, state.Replies = state.Replies[0], state.Replies[1:]
			if err := b.save(state); err != nil {
				return fmt.Errorf("保存 Telegram 批次回复进度: %w", err)
			}
		}
		if state.Pending != nil {
			pending := state.Pending
			// 白名单变化后，不再向已移除的聊天补发旧回复。
			if !b.AllowedChats[pending.ChatID] {
				state.Offset = max(state.Offset, pending.UpdateID+1)
				state.Pending = nil
				state.Batch = nil
				if err := b.save(state); err != nil {
					return fmt.Errorf("保存 Telegram 处理进度: %w", err)
				}
				continue
			}
			method := "sendMessage"
			var sent telegramMessage
			var output any
			starting := state.Batch != nil && state.Batch.Next == 0
			if pending.EditMessageID > 0 {
				method = "editMessageText"
			} else if starting {
				output = &sent
			}
			if err := b.call(ctx, method, pending.request(), output); err != nil {
				if errors.Is(err, errTelegramEditMessageMissing) {
					b.Logs.Append("Telegram", "", "Bot", "WARNING", "原状态消息已删除，将补发已保存结果")
					// 提示被用户删除时补发已保存结果，不重新生成或转存。
					pending.EditMessageID = 0
					if state.Batch != nil {
						state.Batch.StatusMessageID = 0
					}
					if err := b.save(state); err != nil {
						return fmt.Errorf("保存 Telegram 补发状态: %w", err)
					}
					continue
				}
				log.Printf("Telegram 回复失败，稍后重试（不会重复转存）")
				if !replyFailed && ctx.Err() == nil {
					b.Logs.Append("Telegram", "", "Bot", "WARNING", "回复发送失败，稍后重试，不会重复转存")
					replyFailed = true
				}
				if !botDelay(ctx) {
					break
				}
				continue
			}
			b.Logs.Append("Telegram", "", "Bot", "DEBUG", "处理提示或结果回复已发送／更新")
			if replyFailed {
				b.Logs.Append("Telegram", "", "Bot", "INFO", "回复发送已恢复")
				replyFailed = false
			}
			if starting {
				if sent.ID <= 0 {
					return fmt.Errorf("Telegram 未返回开始提示的消息 ID，已停止生成")
				}
				state.Batch.StatusMessageID = sent.ID
			} else if state.Batch != nil {
				// 首条结果覆盖开始提示；后续链接仍逐条保留结果，避免覆盖已完成链接。
				state.Batch.StatusMessageID = 0
			}
			if state.Batch == nil {
				state.Offset = max(state.Offset, pending.UpdateID+1)
			}
			state.Pending = nil
			if err := b.save(state); err != nil {
				return fmt.Errorf("保存 Telegram 回复进度: %w", err)
			}
			continue
		}
		if batch := state.Batch; batch != nil {
			if !b.AllowedChats[batch.ChatID] || batch.Next >= len(batch.Items) {
				state.Offset = max(state.Offset, batch.UpdateID+1)
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
		if b.startReadyBatch(&state, time.Now()) {
			if err := b.save(state); err != nil {
				return fmt.Errorf("保存 Telegram 批次开始状态: %w", err)
			}
			continue
		}
		if err := b.collectUpdates(ctx, &state); err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
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
