package emby

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// RefreshLibrary 请求普通递归刷新，不强制替换已有元数据和图片。
func (c *Client) RefreshLibrary(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("Emby 媒体库 ID 为空")
	}
	query := url.Values{
		"Recursive": {"true"}, "MetadataRefreshMode": {"Default"}, "ImageRefreshMode": {"Default"},
		"ReplaceAllMetadata": {"false"}, "ReplaceAllImages": {"false"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/emby/Items/"+url.PathEscape(id)+"/Refresh?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("创建 Emby 媒体库刷新请求: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Emby 媒体库刷新取消: %w", ctx.Err())
		}
		return fmt.Errorf("无法连接 Emby 刷新媒体库")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode}
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		return fmt.Errorf("读取 Emby 媒体库刷新响应: %w", err)
	}
	return nil
}
