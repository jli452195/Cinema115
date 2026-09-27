package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// DownloadInstantInfoWithUA adds metadata only for source-assisted instant upload.
// Desktop playback responses omit SHA1; share search is read-only and scoped to
// the same share and pinned account. Never use the legacy receiving endpoint.
func (c *ShareClient) DownloadInstantInfoWithUA(ctx context.Context, share *ShareInfo, fileID, cookie, ua string) (*DownloadInfo, error) {
	info, err := c.DownloadInfoWithUA(ctx, share, fileID, cookie, ua)
	if err != nil {
		return nil, err
	}
	if instantHash.MatchString(info.SHA1) {
		return info, nil
	}
	if info.Name == "" || info.Size <= 0 {
		return nil, fmt.Errorf("分享文件元数据不完整，无法查询秒传 SHA1")
	}
	for offset := 0; offset < 1000; offset += 100 {
		q := url.Values{"share_code": {share.Code}, "receive_code": {share.Password}, "cid": {"0"}, "search_value": {info.Name}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		raw, err := c.request(ctx, http.MethodGet, c.WebURL+"/share/search?"+q.Encode(), cookie, nil)
		if err != nil {
			return nil, fmt.Errorf("分享文件 SHA1 查询失败：%s", SharePlaybackFailureReason(err))
		}
		var page struct {
			Count *int `json:"count"`
			List  []struct {
				ID   json.Number `json:"fid"`
				Name string      `json:"n"`
				Size json.Number `json:"s"`
				SHA1 string      `json:"sha"`
			} `json:"list"`
		}
		if json.Unmarshal(raw, &page) != nil || page.Count == nil || *page.Count < 0 || page.List == nil || len(page.List) > 100 || offset+len(page.List) > *page.Count {
			return nil, fmt.Errorf("分享文件 SHA1 查询响应无效")
		}
		for _, f := range page.List {
			if f.ID.String() != fileID {
				continue
			}
			size, err := f.Size.Int64()
			if err != nil || size != info.Size || f.Name != info.Name || !instantHash.MatchString(f.SHA1) {
				return nil, fmt.Errorf("分享文件 SHA1 与取链元数据不一致")
			}
			info.SHA1 = strings.ToUpper(f.SHA1)
			return info, nil
		}
		if offset+len(page.List) >= *page.Count || len(page.List) < 100 {
			break
		}
	}
	return nil, fmt.Errorf("未在分享搜索结果中找到对应文件的 SHA1，未执行秒传")
}
