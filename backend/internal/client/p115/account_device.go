package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CurrentLoginClient identifies only the device explicitly marked current by
// 115. An absent/ambiguous device does not imply the Cookie is invalid.
func (a *AccountClient) CurrentLoginClient(ctx context.Context, cookie string) (string, error) {
	if strings.TrimSpace(cookie) == "" || strings.ContainsAny(cookie, "\r\n") {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.DevicesURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建登录设备请求: %w", err)
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", PlaybackUserAgent)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询登录设备: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("登录设备接口返回 HTTP %d", resp.StatusCode)
	}
	var result struct {
		State json.RawMessage `json:"state"`
		Data  struct {
			List []struct {
				Current json.RawMessage `json:"is_current"`
				Icon    string          `json:"icon"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&result); err != nil {
		return "", fmt.Errorf("解析登录设备: %w", err)
	}
	if !deviceFlag(result.State) {
		return "", nil
	}
	current, count := "", 0
	for _, device := range result.Data.List {
		if !deviceFlag(device.Current) {
			continue
		}
		count++
		current = device.Icon
	}
	if count != 1 || !KnownLoginApp(current) {
		return "", nil
	}
	return current, nil
}

func deviceFlag(value json.RawMessage) bool {
	switch strings.TrimSpace(string(value)) {
	case "true", "1", `"1"`:
		return true
	}
	return false
}
