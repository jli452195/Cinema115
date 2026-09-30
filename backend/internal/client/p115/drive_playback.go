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

type DrivePlaybackFile struct {
	ID       string `json:"id"`
	CID      string `json:"cid"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SHA1     string `json:"sha1"`
	PickCode string `json:"pickCode"`
}

func (c *ShareClient) ValidateCacheParent(ctx context.Context, cid, cookie string) error {
	if !validDriveID(cid, true) {
		return fmt.Errorf("缓存父目录无效")
	}
	q := url.Values{"cid": {cid}, "offset": {"0"}, "limit": {"1"}, "show_dir": {"1"}}
	raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
	if err != nil {
		return fmt.Errorf("无法核查缓存目录：%s", SharePlaybackFailureReason(err))
	}
	var response struct {
		CID  json.Number
		Path []struct{ CID json.Number }
	}
	if json.Unmarshal(raw, &response) != nil {
		return fmt.Errorf("缓存目录响应格式无效，无法核对目录身份")
	}
	if (response.CID.String() != cid && len(response.Path) == 0) || (response.CID != "" && response.CID.String() != cid) || (len(response.Path) > 0 && response.Path[len(response.Path)-1].CID.String() != cid) {
		return fmt.Errorf("115 返回的目录身份与请求目录 %s 不一致或缺失，目录可能已删除或不可访问", cid)
	}
	return nil
}

// CacheDirectoryFiles is bounded and requires positive proof of the requested directory.
// Playback cache directories contain one file; extra entries prevent cleanup.
func (c *ShareClient) CacheDirectoryFiles(ctx context.Context, cid, cookie string) ([]DrivePlaybackFile, error) {
	return c.cacheDirectoryFiles(ctx, cid, cookie, false)
}

func (c *ShareClient) SharedCacheDirectoryFiles(ctx context.Context, cid, cookie string) ([]DrivePlaybackFile, error) {
	return c.cacheDirectoryFiles(ctx, cid, cookie, true)
}

func (c *ShareClient) cacheDirectoryFiles(ctx context.Context, cid, cookie string, shared bool) ([]DrivePlaybackFile, error) {
	if !validDriveID(cid, false) {
		return nil, fmt.Errorf("缓存目录编号无效")
	}
	all := make([]DrivePlaybackFile, 0)
	seen := make(map[string]bool)
	expectedCount := int64(-1)
	for offset := 0; offset < 10000; offset += 100 {
		q := url.Values{"cid": {cid}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}, "show_dir": {"1"}, "o": {"file_id"}, "asc": {"1"}}
		raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
		if err != nil {
			return nil, fmt.Errorf("无法核查缓存目录")
		}
		var response struct {
			CID   json.Number
			Count json.Number
			Path  []struct{ CID json.Number }
			Data  []struct {
				FID  json.Number `json:"fid"`
				CID  json.Number `json:"cid"`
				Name string      `json:"n"`
				Size json.Number `json:"s"`
				SHA  string      `json:"sha"`
				PC   string      `json:"pc"`
			}
		}
		if json.Unmarshal(raw, &response) != nil {
			return nil, fmt.Errorf("缓存目录响应无效")
		}
		if (response.CID.String() != cid && len(response.Path) == 0) || (response.CID != "" && response.CID.String() != cid) || (len(response.Path) > 0 && response.Path[len(response.Path)-1].CID.String() != cid) {
			return nil, fmt.Errorf("缓存目录已变化")
		}
		count, err := response.Count.Int64()
		if err != nil || count < 0 || count > 10000 || len(response.Data) > 100 || int64(offset+len(response.Data)) > count || response.Data == nil || (expectedCount >= 0 && expectedCount != count) || (!shared && count != int64(len(response.Data))) {
			return nil, fmt.Errorf("缓存目录内容不完整")
		}
		expectedCount = count
		for _, f := range response.Data {
			if shared && f.FID == "" && validDriveID(f.CID.String(), false) {
				continue
			}
			if !validDriveID(f.FID.String(), false) || f.CID.String() != cid {
				return nil, fmt.Errorf("缓存目录存在其他目录或条目")
			}
			size, err := f.Size.Int64()
			if err != nil || size < 0 || !instantHash.MatchString(f.SHA) || !instantPickCode.MatchString(f.PC) {
				return nil, fmt.Errorf("缓存文件信息不完整")
			}
			if seen[f.FID.String()] {
				return nil, fmt.Errorf("缓存目录分页条目重复")
			}
			seen[f.FID.String()] = true
			all = append(all, DrivePlaybackFile{ID: f.FID.String(), CID: cid, Name: f.Name, Size: size, SHA1: strings.ToUpper(f.SHA), PickCode: f.PC})
		}
		if int64(offset+len(response.Data)) == count {
			return all, nil
		}
		if len(response.Data) != 100 {
			return nil, fmt.Errorf("缓存目录分页不完整")
		}
	}
	return nil, fmt.Errorf("缓存目录超过核查上限")
}

func (c *ShareClient) DriveDownloadURLWithUA(ctx context.Context, fileID, pickCode, cookie, ua string) (string, error) {
	if !validDriveID(fileID, false) || !instantPickCode.MatchString(pickCode) || CookieUserID(cookie) == "" {
		return "", fmt.Errorf("目标文件或账号无效")
	}
	ctx, finish := tracePlaybackHTTP(ctx, "目标账号取链 HTTP")
	defer finish()
	decoded, err := c.driveDownloadResponse(ctx, pickCode, cookie, ua)
	if err != nil {
		return "", err
	}
	return parseDrivePlaybackURL(decoded, fileID, pickCode)
}

func (c *ShareClient) driveDownloadResponse(ctx context.Context, pickCode, cookie, ua string) ([]byte, error) {
	if !instantPickCode.MatchString(pickCode) || CookieUserID(cookie) == "" {
		return nil, fmt.Errorf("pickcode 或所选账号无效")
	}
	if ua == "" {
		ua = PlaybackUserAgent
	}
	if len(ua) > 2048 || strings.ContainsAny(ua, "\r\n") {
		return nil, fmt.Errorf("播放器 User-Agent 无效")
	}
	payload, _ := json.Marshal(map[string]string{"pickcode": pickCode, "user_id": CookieUserID(cookie)})
	raw, err := c.requestEnvelopeWithUA(ctx, http.MethodPost, c.AppURL+"/app/chrome/downurl", cookie, url.Values{"data": {RSAEncrypt(payload)}}, ua)
	if err != nil {
		return nil, fmt.Errorf("网盘直链读取失败: %w", err)
	}
	var envelope struct{ Data string }
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, ErrDriveInvalidResponse
	}
	decoded, err := RSADecrypt(envelope.Data)
	if err != nil {
		return nil, ErrDriveInvalidResponse
	}
	return decoded, nil
}

func parseDrivePlaybackURL(raw []byte, fileID, pickCode string) (string, error) {
	var files map[string]struct {
		ID  json.Number `json:"file_id"`
		PC  string      `json:"pick_code"`
		URL struct {
			URL string `json:"url"`
		} `json:"url"`
	}
	if json.Unmarshal(raw, &files) != nil || len(files) != 1 {
		return "", ErrDriveInvalidResponse
	}
	file, ok := files[fileID]
	if !ok || (file.ID != "" && file.ID.String() != fileID) || (file.PC != "" && file.PC != pickCode) || !AllowedMediaURL(file.URL.URL) {
		return "", ErrDriveInvalidResponse
	}
	u, _ := url.Parse(file.URL.URL)
	if flag := u.Query().Get("f"); flag != "" && flag != "0" && flag != "1" {
		return "", ErrDriveLinkRestricted
	}
	return file.URL.URL, nil
}
