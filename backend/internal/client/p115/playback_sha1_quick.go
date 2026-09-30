package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Accept only the observed, documented two-field miss. Unknown flags/codes
// must not turn an authentication or upstream failure into permission to write.
func playbackSHAMiss(raw []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || string(fields["state"]) != "false" {
		return false
	}
	var message string
	return json.Unmarshal(fields["error"], &message) == nil && message == "文件错误"
}

// The quick endpoint returns one candidate without SHA1. Revalidate its actual
// identity with the same target credentials before exposing a reusable file.
func (c *ShareClient) FindPlaybackFileBySHA1Quick(ctx context.Context, hash, cookie string) (file *DrivePlaybackFile, resultErr error) {
	return c.findPlaybackSHA1(ctx, hash, cookie, true)
}

// A candidate is NOT a verified copy. Only the subsequent download response
// may prove its identity; preparation without a URL keeps using Quick above.
func (c *ShareClient) FindPlaybackCandidateBySHA1(ctx context.Context, hash, cookie string) (*DrivePlaybackFile, error) {
	return c.findPlaybackSHA1(ctx, hash, cookie, false)
}

func (c *ShareClient) findPlaybackSHA1(ctx context.Context, hash, cookie string, verify bool) (file *DrivePlaybackFile, resultErr error) {
	if !instantHash.MatchString(hash) || CookieUserID(cookie) == "" {
		return nil, fmt.Errorf("SHA1 或目标账号无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	var total RequestTiming
	queryCtx := WithRequestTiming(ctx, func(v RequestTiming) {
		reportRequestTiming(ctx, v)
		total.Requests += v.Requests
		total.Retries += v.Retries
		total.Wait += v.Wait
		total.HTTP += v.HTTP
	})
	defer func() {
		pages := 0
		if total.Requests > 0 {
			pages = 1
		}
		stage := "目标 SHA1 候选查询汇总"
		if verify {
			stage = "目标 SHA1 专用查询汇总（含命中核验）"
		}
		emitPlaybackTiming(ctx, PlaybackTiming{Stage: stage, Duration: time.Since(started), SearchSummary: true, Requests: total.Requests, Pages: pages, Retries: total.Retries, Wait: total.Wait, RequestHTTP: total.HTTP, Failed: resultErr != nil, Canceled: errors.Is(resultErr, context.Canceled)})
	}()
	reader := c.playbackReader(cookie)
	raw, err := reader.metadataRequest(queryCtx, http.MethodGet, func() ([]byte, error) {
		httpCtx, finish := tracePlaybackHTTP(queryCtx, "目标 SHA1 专用查询 HTTP")
		defer finish()
		return reader.requestEnvelopeOnce(httpCtx, http.MethodGet, c.WebURL+"/files/shasearch?"+url.Values{"sha1": {strings.ToUpper(hash)}}.Encode(), cookie, nil)
	})
	if err != nil {
		return nil, fmt.Errorf("SHA1 专用查询失败: %w", err)
	}
	if playbackSHAMiss(raw) {
		return nil, nil
	}
	var response struct {
		State json.RawMessage `json:"state"`
		Data  struct {
			ID   json.Number `json:"file_id"`
			CID  json.Number `json:"category_id"`
			Size json.Number `json:"file_size"`
			PC   string      `json:"pick_code"`
			Name string      `json:"file_name"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || !upstreamOK(response.State) {
		return nil, fmt.Errorf("SHA1 专用查询响应无效")
	}
	d := response.Data
	size, err := d.Size.Int64()
	if err != nil || size <= 0 || !validDriveID(d.ID.String(), false) || !validDriveID(d.CID.String(), true) || !instantPickCode.MatchString(d.PC) {
		return nil, fmt.Errorf("SHA1 专用查询缺少有效文件标识")
	}
	if !verify {
		return &DrivePlaybackFile{ID: d.ID.String(), CID: d.CID.String(), PickCode: d.PC, Name: d.Name, Size: size, SHA1: strings.ToUpper(hash)}, nil
	}
	f, err := c.playbackPickCodeMetadata(queryCtx, "sha-query", d.PC, cookie)
	if err != nil {
		if errors.Is(err, ErrDriveRejected) {
			// An unverified candidate is only useful for fresh existence checks;
			// callers must never reuse it while err is non-nil.
			return &DrivePlaybackFile{ID: d.ID.String(), CID: d.CID.String(), Size: size, PickCode: d.PC, SHA1: strings.ToUpper(hash)}, err
		}
		return nil, err
	}
	if f.ID != d.ID.String() || f.Size != size || !strings.EqualFold(f.SHA1, hash) {
		return nil, fmt.Errorf("SHA1 专用查询文件身份核验失败")
	}
	copy := *f
	copy.CID = d.CID.String()
	return &copy, nil
}
