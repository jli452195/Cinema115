package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// MediaUpdated 只报告已变化的文件路径，不触发全库扫描或强制重刮削。
func (c *Client) MediaUpdated(ctx context.Context, paths []string) error {
	type update struct{ Path, UpdateType string }
	updates := make([]update, 0, len(paths))
	for _, path := range paths {
		updates = append(updates, update{Path: path, UpdateType: "Created"})
	}
	body, err := json.Marshal(struct{ Updates []update }{updates})
	if err != nil {
		return fmt.Errorf("编码 Emby 路径通知: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/emby/Library/Media/Updated", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 Emby 路径通知: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Emby 路径通知取消: %w", ctx.Err())
		}
		return fmt.Errorf("无法连接 Emby 发送路径通知")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode}
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		return fmt.Errorf("读取 Emby 路径通知响应: %w", err)
	}
	return nil
}
