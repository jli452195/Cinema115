package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FindPlaybackFileBySHA1 searches the selected account's drive, not a local
// cache inventory. Only an explicitly successful, complete search can miss.
func (c *ShareClient) FindPlaybackFileBySHA1(ctx context.Context, hash, cookie string) (file *DrivePlaybackFile, resultErr error) {
	return c.findPlaybackFileBySHA1(ctx, hash, cookie, nil)
}

// Exclusions must contain only file IDs freshly confirmed missing by this
// request. A complete search is still required before returning a miss.
func (c *ShareClient) FindPlaybackFileBySHA1Excluding(ctx context.Context, hash, cookie string, missing map[string]bool) (*DrivePlaybackFile, error) {
	return c.findPlaybackFileBySHA1(ctx, hash, cookie, missing)
}

func (c *ShareClient) findPlaybackFileBySHA1(ctx context.Context, hash, cookie string, missing map[string]bool) (file *DrivePlaybackFile, resultErr error) {
	if !instantHash.MatchString(hash) || CookieUserID(cookie) == "" {
		return nil, fmt.Errorf("SHA1 或目标账号无效")
	}
	started := time.Now()
	pages, requests, retries := 0, 0, 0
	var httpTime, waitTime time.Duration
	timingCtx := WithRequestTiming(ctx, func(value RequestTiming) {
		reportRequestTiming(ctx, value)
		requests += value.Requests
		retries += value.Retries
		waitTime += value.Wait
		httpTime += value.HTTP
	})
	defer func() {
		emitPlaybackTiming(ctx, PlaybackTiming{Stage: "目标 SHA1 搜索汇总", Duration: time.Since(started), SearchSummary: true, Requests: requests, Pages: pages, Retries: retries, Wait: waitTime, RequestHTTP: httpTime, Failed: resultErr != nil, Canceled: errors.Is(resultErr, context.Canceled)})
	}()
	expectedCount := int64(-1)
	for offset := 0; offset < 10000; offset += 100 {
		pageRequested := false
		q := url.Values{"aid": {"1"}, "cid": {"0"}, "fc": {"2"}, "type": {"99"}, "show_dir": {"0"}, "search_value": {strings.ToUpper(hash)}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		reader := c.playbackReader(cookie)
		raw, err := reader.metadataRequest(timingCtx, http.MethodGet, func() ([]byte, error) {
			if !pageRequested {
				pages++
				pageRequested = true
			}
			httpCtx, finish := tracePlaybackHTTP(timingCtx, "目标 SHA1 搜索 HTTP")
			defer finish()
			return reader.requestEnvelopeOnce(httpCtx, http.MethodGet, c.WebURL+"/files/search?"+q.Encode(), cookie, nil)
		})
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
		for _, f := range response.Data {
			if !strings.EqualFold(f.SHA1, hash) {
				continue
			}
			if missing[f.ID.String()] {
				continue
			}
			if !validDriveID(f.ID.String(), false) || !validDriveID(f.CID.String(), true) || !instantPickCode.MatchString(f.PickCode) {
				return nil, fmt.Errorf("同 SHA1 文件缺少有效的取链标识")
			}
			// Size and name are display metadata, never equality requirements.
			size, _ := f.Size.Int64()
			return &DrivePlaybackFile{ID: f.ID.String(), CID: f.CID.String(), Name: f.Name, Size: size, SHA1: strings.ToUpper(f.SHA1), PickCode: f.PickCode}, nil
		}
		count, err := response.Count.Int64()
		if err != nil || count < 0 || response.Data == nil || len(response.Data) > 100 || int64(offset+len(response.Data)) > count || (expectedCount >= 0 && count != expectedCount) {
			return nil, fmt.Errorf("SHA1 搜索结果不完整")
		}
		expectedCount = count
		if int64(offset+len(response.Data)) == count {
			return nil, nil
		}
		if len(response.Data) != 100 {
			return nil, fmt.Errorf("SHA1 搜索分页不完整")
		}
	}
	return nil, fmt.Errorf("SHA1 搜索超过核查上限")
}
