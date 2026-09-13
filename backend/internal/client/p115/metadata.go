package p115

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const MaxMetadataSize int64 = 32 << 20

// ReadMetadata 读取有权访问的配套文件；登录 Cookie 仅发送给下载地址查询接口。
func (c *ShareClient) ReadMetadata(ctx context.Context, share *ShareInfo, fileID, cookie string) ([]byte, error) {
	link, err := c.DownloadURL(ctx, share, fileID, cookie)
	if err != nil {
		return nil, err
	}
	return c.readMetadataURL(ctx, link)
}

func (c *ShareClient) readMetadataURL(ctx context.Context, link string) ([]byte, error) {
	if !AllowedMediaURL(link) {
		return nil, fmt.Errorf("元数据下载域名不在允许范围内")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, fmt.Errorf("创建元数据下载请求失败")
	}
	req.Header.Set("User-Agent", PlaybackUserAgent)
	// 独立客户端复用传输层，不使用 API 的 Cookie Jar，也不修改共享客户端。
	client := &http.Client{Transport: c.HTTP.Transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !AllowedMediaURL(req.URL.String()) {
			return fmt.Errorf("元数据重定向不被允许")
		}
		return nil
	}}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("元数据下载连接失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("元数据下载返回 HTTP %d", response.StatusCode)
	}
	if response.ContentLength > MaxMetadataSize {
		return nil, fmt.Errorf("元数据文件超过 32 MB")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxMetadataSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取元数据内容: %w", err)
	}
	if int64(len(data)) > MaxMetadataSize {
		return nil, fmt.Errorf("元数据文件超过 32 MB")
	}
	return data, nil
}
