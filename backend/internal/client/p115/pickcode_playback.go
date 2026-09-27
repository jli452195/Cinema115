package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The pickcode belongs to the explicitly selected source account. No third-party
// STRM host is contacted and the optional filename is never trusted as metadata.
func (c *ShareClient) PickCodeDownloadURLWithUA(ctx context.Context, pickCode, cookie, ua string) (string, error) {
	info, _, err := c.pickCodeInfo(ctx, pickCode, cookie, ua)
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

func (c *ShareClient) pickCodeInfo(ctx context.Context, pickCode, cookie, ua string) (*DownloadInfo, string, error) {
	raw, err := c.driveDownloadResponse(ctx, pickCode, cookie, ua)
	if err != nil {
		return nil, "", err
	}
	return parsePickCodeInfo(raw, pickCode)
}

func parsePickCodeInfo(raw []byte, pickCode string) (*DownloadInfo, string, error) {
	var files map[string]struct {
		ID   json.Number `json:"file_id"`
		PC   string      `json:"pick_code"`
		Name string      `json:"file_name"`
		Size json.Number `json:"file_size"`
		SHA1 string      `json:"sha1"`
	}
	if json.Unmarshal(raw, &files) != nil || len(files) != 1 {
		return nil, "", fmt.Errorf("pickcode 文件响应无效")
	}
	for id, f := range files {
		if !validDriveID(id, false) || f.PC != pickCode || (f.ID != "" && f.ID.String() != id) {
			return nil, "", fmt.Errorf("pickcode 文件身份校验失败")
		}
		link, err := parseDrivePlaybackURL(raw, id, pickCode)
		if err != nil {
			return nil, "", err
		}
		size, err := f.Size.Int64()
		if err != nil || size <= 0 || strings.TrimSpace(f.Name) == "" {
			return nil, "", fmt.Errorf("pickcode 文件名称或大小无效")
		}
		return &DownloadInfo{Name: f.Name, Size: size, SHA1: strings.ToUpper(f.SHA1), URL: link}, id, nil
	}
	return nil, "", fmt.Errorf("pickcode 未返回文件")
}

// Obtain the source link using the same UA used by InstantUpload's bounded range
// reads. Supplement SHA1 with a read-only, exact file lookup if downurl omits it.
func (c *ShareClient) PickCodeInstantInfo(ctx context.Context, pickCode, cookie string) (*DownloadInfo, error) {
	info, id, err := c.pickCodeInfo(ctx, pickCode, cookie, PlaybackUserAgent)
	if err != nil {
		return nil, err
	}
	if !instantHash.MatchString(info.SHA1) {
		q := url.Values{"file_id": {id}}
		raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files/get_info?"+q.Encode(), cookie, nil)
		if err != nil {
			return nil, fmt.Errorf("读取 pickcode 文件 SHA1: %w", err)
		}
		var response struct {
			Data []struct {
				ID   json.Number `json:"file_id"`
				PC   string      `json:"pick_code"`
				Name string      `json:"file_name"`
				Size json.Number `json:"file_size"`
				SHA1 string      `json:"sha1"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &response) != nil || len(response.Data) != 1 {
			return nil, fmt.Errorf("pickcode 文件 SHA1 响应无效")
		}
		f := response.Data[0]
		size, err := f.Size.Int64()
		if err != nil || f.ID.String() != id || f.PC != pickCode || f.Name != info.Name || size != info.Size {
			return nil, fmt.Errorf("pickcode 文件 SHA1 与取链元数据不一致")
		}
		info.SHA1 = strings.ToUpper(f.SHA1)
	}
	if err := ValidateInstantSource(info); err != nil {
		return nil, err
	}
	return info, nil
}
