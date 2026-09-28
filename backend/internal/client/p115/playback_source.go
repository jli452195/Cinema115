package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type playbackSourceMetadata struct {
	File    DrivePlaybackFile
	Expires time.Time
}
type playbackSourceCache struct {
	mu      sync.Mutex
	entries map[string]playbackSourceMetadata
}

// Cache metadata only. Every request gets its own closure with fixed credentials;
// source URLs are fetched only if 115 actually requests a byte-range challenge.
func (c *ShareClient) PlaybackPickCodeSource(ctx context.Context, accountID, pc, cookie string) (*DownloadInfo, error) {
	if accountID == "" || CookieUserID(cookie) == "" || !instantPickCode.MatchString(pc) {
		return nil, fmt.Errorf("源账号或 pickcode 配置无效")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := playbackPreparationKey(accountID+"\x00"+pc, cookie)
	c.playbackSources.mu.Lock()
	cached, ok := c.playbackSources.entries[key]
	c.playbackSources.mu.Unlock()
	f := cached.File
	if !ok || time.Now().After(cached.Expires) {
		raw, err := c.playbackReader(cookie).requestEnvelope(ctx, http.MethodPost, c.WebURL+"/files/supervision", cookie, url.Values{"pickcode": {pc}, "preview_type": {"file"}, "module": {"10"}})
		if err != nil {
			return nil, fmt.Errorf("源 pickcode 元数据查询失败：%s", SharePlaybackFailureReason(err))
		}
		var resp struct {
			Data struct {
				ID   json.Number `json:"file_id"`
				PC   string      `json:"pick_code"`
				Name string      `json:"file_name"`
				SHA  string      `json:"file_sha1"`
				Size json.Number `json:"file_size"`
			}
		}
		if json.Unmarshal(raw, &resp) != nil {
			return nil, fmt.Errorf("源元数据响应格式无效")
		}
		d := resp.Data
		size, err := d.Size.Int64()
		if err != nil || !validDriveID(d.ID.String(), false) || (d.PC != "" && d.PC != pc) || !instantHash.MatchString(d.SHA) || size <= 0 {
			return nil, fmt.Errorf("源元数据缺少 SHA1、大小或文件标识")
		}
		f = DrivePlaybackFile{ID: d.ID.String(), PickCode: pc, Name: d.Name, Size: size, SHA1: strings.ToUpper(d.SHA)}
		c.playbackSources.mu.Lock()
		if c.playbackSources.entries == nil || len(c.playbackSources.entries) >= 2000 {
			c.playbackSources.entries = make(map[string]playbackSourceMetadata)
		}
		c.playbackSources.entries[key] = playbackSourceMetadata{File: f, Expires: time.Now().Add(10 * time.Minute)}
		c.playbackSources.mu.Unlock()
	}
	info := &DownloadInfo{Name: f.Name, Size: f.Size, SHA1: f.SHA1}
	info.loadPlaybackURL = func(readCtx context.Context) (string, error) {
		got, id, err := c.pickCodeInfo(readCtx, pc, cookie, PlaybackUserAgent)
		if err != nil {
			return "", err
		}
		if got == nil || id != f.ID || got.Size != f.Size || (got.SHA1 != "" && !strings.EqualFold(got.SHA1, f.SHA1)) {
			return "", fmt.Errorf("源直链文件身份或 SHA1 已变化")
		}
		return got.URL, nil
	}
	if err := ValidateInstantSource(info); err != nil {
		return nil, err
	}
	return info, nil
}
