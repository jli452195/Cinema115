package p115

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const MaxSubtitleSourceBytes int64 = 64 << 20

// ReadSubtitleSource reads one exact own-drive file. Cookies only reach the
// authenticated API, never the CDN or its redirects. Nothing is written to disk.
func (c *ShareClient) ReadSubtitleSource(ctx context.Context, parent string, expected ShareFile, cookie string) ([]byte, error) {
	if expected.Directory || expected.Size <= 0 || expected.Size > MaxSubtitleSourceBytes || !validDriveID(parent, true) {
		return nil, fmt.Errorf("字幕来源文件或大小无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	file, err := c.playbackFileInfo(ctx, expected.ID, cookie)
	if err != nil {
		return nil, fmt.Errorf("核验字幕来源: %w", err)
	}
	if file.CID != parent || file.Name != expected.Name || file.Size != expected.Size {
		return nil, fmt.Errorf("字幕来源已移动或发生变化，请重新选择")
	}
	link, err := c.VerifiedPlaybackURL(ctx, file, cookie, PlaybackUserAgent)
	if err != nil {
		return nil, fmt.Errorf("获取字幕来源下载地址: %w", err)
	}
	return c.readSubtitleSourceURL(ctx, link, file.Size, file.SHA1)
}

func (c *ShareClient) readSubtitleSourceURL(ctx context.Context, link string, size int64, hash string) ([]byte, error) {
	if !AllowedMediaURL(link) || size <= 0 || size > MaxSubtitleSourceBytes || !instantHash.MatchString(hash) {
		return nil, fmt.Errorf("字幕来源下载地址或大小无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, fmt.Errorf("创建字幕下载请求失败")
	}
	req.Header.Set("User-Agent", PlaybackUserAgent)
	client := &http.Client{Transport: c.HTTP.Transport, Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !AllowedMediaURL(req.URL.String()) {
			return fmt.Errorf("字幕下载重定向不被允许")
		}
		return nil
	}}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("字幕来源下载连接失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("字幕来源下载返回 HTTP %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != size {
		return nil, fmt.Errorf("字幕来源下载大小已变化，请重新选择")
	}
	data := make([]byte, size)
	_, err = io.ReadFull(response.Body, data)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("读取字幕来源内容失败")
	}
	var extra [1]byte
	if count, readErr := io.ReadFull(response.Body, extra[:]); count != 0 || readErr != io.EOF {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("字幕来源内容大小不一致")
	}
	digest := sha1.Sum(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), hash) {
		return nil, fmt.Errorf("字幕来源完整性校验失败")
	}
	return data, nil
}
