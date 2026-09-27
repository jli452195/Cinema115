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

// FindPlaybackFileBySHA1 searches the selected account's drive, not a local
// cache inventory. Only an explicitly successful, complete search can miss.
func (c *ShareClient) FindPlaybackFileBySHA1(ctx context.Context, hash, cookie string) (*DrivePlaybackFile, error) {
	if !instantHash.MatchString(hash) || CookieUserID(cookie) == "" {
		return nil, fmt.Errorf("SHA1 或目标账号无效")
	}
	expectedCount := int64(-1)
	for offset := 0; offset < 10000; offset += 100 {
		q := url.Values{"aid": {"1"}, "cid": {"0"}, "fc": {"0"}, "show_dir": {"0"}, "search_value": {strings.ToUpper(hash)}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files/search?"+q.Encode(), cookie, nil)
		if err != nil {
			return nil, fmt.Errorf("SHA1 搜索请求失败: %w", err)
		}
		var response struct {
			Count json.Number `json:"count"`
			Data  []struct {
				ID       json.Number `json:"fid"`
				CID      json.Number `json:"cid"`
				Name     string      `json:"n"`
				Size     json.Number `json:"s"`
				SHA1     string      `json:"sha"`
				PickCode string      `json:"pc"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &response) != nil {
			return nil, fmt.Errorf("SHA1 搜索响应无效")
		}
		count, err := response.Count.Int64()
		if err != nil || count < 0 || response.Data == nil || len(response.Data) > 100 || int64(offset+len(response.Data)) > count || (expectedCount >= 0 && count != expectedCount) {
			return nil, fmt.Errorf("SHA1 搜索结果不完整")
		}
		expectedCount = count
		for _, f := range response.Data {
			if !strings.EqualFold(f.SHA1, hash) {
				continue
			}
			if !validDriveID(f.ID.String(), false) || !validDriveID(f.CID.String(), true) || !instantPickCode.MatchString(f.PickCode) {
				return nil, fmt.Errorf("同 SHA1 文件缺少有效的取链标识")
			}
			// Size and name are display metadata, never equality requirements.
			size, _ := f.Size.Int64()
			return &DrivePlaybackFile{ID: f.ID.String(), CID: f.CID.String(), Name: f.Name, Size: size, SHA1: strings.ToUpper(f.SHA1), PickCode: f.PickCode}, nil
		}
		if int64(offset+len(response.Data)) == count {
			return nil, nil
		}
		if len(response.Data) != 100 {
			return nil, fmt.Errorf("SHA1 搜索分页不完整")
		}
	}
	return nil, fmt.Errorf("SHA1 搜索超过核查上限")
}
