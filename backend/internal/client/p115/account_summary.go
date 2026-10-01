package p115

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

type summaryEntry struct {
	done    chan struct{}
	value   model.P115Summary
	valid   bool
	err     error
	expires time.Time
}

// QuerySummary coalesces callers without detached goroutines. The initiating
// request owns all I/O. Entries are isolated by configuration and credential.
func (a *AccountClient) QuerySummary(ctx context.Context, id, cookie string) (model.P115Summary, bool, error) {
	key := sha256.Sum256([]byte(id + "\x00" + cookie))
	a.summaryMu.Lock()
	if a.summaries == nil {
		a.summaries = make(map[[32]byte]*summaryEntry)
	}
	if entry := a.summaries[key]; entry != nil && (entry.expires.IsZero() || time.Now().Before(entry.expires)) {
		a.summaryMu.Unlock()
		select {
		case <-ctx.Done():
			return model.P115Summary{}, false, ctx.Err()
		case <-entry.done:
			return entry.value, entry.valid, entry.err
		}
	}
	for k, entry := range a.summaries {
		if !entry.expires.IsZero() && time.Now().After(entry.expires) {
			delete(a.summaries, k)
		}
	}
	// Bound memory even if credentials are frequently changed.
	if len(a.summaries) >= 128 {
		for k, entry := range a.summaries {
			if !entry.expires.IsZero() {
				delete(a.summaries, k)
				break
			}
		}
	}
	if len(a.summaries) >= 128 {
		a.summaryMu.Unlock()
		return model.P115Summary{}, false, errors.New("账号查询繁忙，请重试")
	}
	entry := &summaryEntry{done: make(chan struct{})}
	a.summaries[key] = entry
	a.summaryMu.Unlock()
	value, valid, err := a.querySummary(ctx, cookie)
	a.summaryMu.Lock()
	entry.value, entry.valid, entry.err = value, valid, err
	entry.expires = time.Now().Add(30 * time.Second)
	if err != nil || !valid {
		delete(a.summaries, key)
	}
	close(entry.done)
	a.summaryMu.Unlock()
	return value, valid, err
}

func (a *AccountClient) summaryJSON(ctx context.Context, address, cookie string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("创建账号资料请求: %w", err)
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", PlaybackUserAgent)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("查询账号资料: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("账号资料接口返回 HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<10)).Decode(out); err != nil {
		return fmt.Errorf("解析账号资料: %w", err)
	}
	return nil
}

func (a *AccountClient) querySummary(ctx context.Context, cookie string) (model.P115Summary, bool, error) {
	var summary model.P115Summary
	if CookieUserID(cookie) == "" || strings.ContainsAny(cookie, "\r\n") {
		return summary, false, nil
	}
	var info struct {
		State json.RawMessage `json:"state"`
		Data  struct {
			UID json.RawMessage `json:"user_id"`
			VIP json.RawMessage `json:"vip"`
		} `json:"data"`
	}
	if err := a.summaryJSON(ctx, a.InfoURL, cookie, &info); err != nil {
		return summary, false, err
	}
	state := strings.TrimSpace(string(info.State))
	if state == "false" || state == "0" {
		return summary, false, nil
	}
	if !deviceFlag(info.State) {
		return summary, false, errors.New("账号资料缺少登录状态")
	}
	uid := strings.Trim(string(info.Data.UID), `"`)
	if uid != CookieUserID(cookie) {
		return summary, false, errors.New("账号资料身份不一致")
	}
	switch strings.TrimSpace(string(info.Data.VIP)) {
	case "1", `"1"`, "true":
		summary.VIP = "vip"
	case "0", `"0"`, "false":
		summary.VIP = "normal"
	}
	summary.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	var space struct {
		State json.RawMessage `json:"state"`
		Data  struct {
			Space struct {
				Remaining struct {
					Size json.RawMessage `json:"size"`
				} `json:"all_remain"`
			} `json:"space_info"`
		} `json:"data"`
	}
	// Space errors don't invalidate the independently verified login or VIP data.
	if err := a.summaryJSON(ctx, a.SpaceURL, cookie, &space); err == nil && deviceFlag(space.State) {
		value, err := strconv.ParseFloat(strings.Trim(string(space.Data.Space.Remaining.Size), `"`), 64)
		if err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < math.MaxInt64 {
			summary.RemainingBytes, summary.SpaceKnown = int64(value), true
		}
	}
	return summary, true, nil
}
