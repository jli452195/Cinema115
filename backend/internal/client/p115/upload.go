package p115

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const MaxSubtitleBytes = 16 << 20

// UploadSubtitle uses the web sample-upload protocol documented by
// https://github.com/ChenyangGao/p115client (upload_sample_init/upload_file_sample).
// No Python dependency or upload credentials are exposed to the browser.
func (c *ShareClient) UploadSubtitle(ctx context.Context, cid, name string, data []byte, cookie string) error {
	if !validDriveID(cid, true) || name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\\r\n\x00") || len(data) == 0 || len(data) > MaxSubtitleBytes {
		return fmt.Errorf("字幕上传参数无效")
	}
	_, err := c.metadataRequest(ctx, http.MethodPost, func() ([]byte, error) {
		form := url.Values{"filename": {name}, "target": {"U_1_" + cid}, "path": {""}, "filesize": {strconv.Itoa(len(data))}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://uplb.115.com/3.0/sampleinitupload.php", strings.NewReader(form.Encode()))
		if err != nil {
			return nil, fmt.Errorf("创建上传初始化请求: %w", err)
		}
		req.Header.Set("Cookie", cookie)
		req.Header.Set("User-Agent", PlaybackUserAgent)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		raw, err := c.subtitleUploadRequest(req)
		if err != nil {
			return nil, err
		}
		var init struct {
			State     json.RawMessage `json:"state"`
			Host      string          `json:"host"`
			Object    string          `json:"object"`
			Policy    string          `json:"policy"`
			AccessID  string          `json:"accessid"`
			Callback  string          `json:"callback"`
			Signature string          `json:"signature"`
		}
		if json.Unmarshal(raw, &init) != nil || (len(init.State) > 0 && !upstreamOK(init.State)) || init.Object == "" || init.Policy == "" || init.AccessID == "" || init.Callback == "" || init.Signature == "" {
			return nil, fmt.Errorf("115 未返回有效的上传参数，请检查账号登录和可用空间")
		}
		host, err := url.Parse(init.Host)
		if err != nil || host.User != nil || host.Port() != "" || host.RawQuery != "" || host.Fragment != "" || !strings.HasSuffix(strings.ToLower(host.Hostname()), ".aliyuncs.com") || (host.Scheme != "http" && host.Scheme != "https") {
			return nil, fmt.Errorf("115 返回的上传地址不受支持")
		}
		host.Scheme = "https"
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		fields := [][2]string{{"name", name}, {"key", init.Object}, {"policy", init.Policy}, {"OSSAccessKeyId", init.AccessID}, {"success_action_status", "200"}, {"callback", init.Callback}, {"signature", init.Signature}}
		for _, field := range fields {
			if err := writer.WriteField(field[0], field[1]); err != nil {
				return nil, fmt.Errorf("编码上传参数: %w", err)
			}
		}
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			return nil, fmt.Errorf("创建字幕上传表单: %w", err)
		}
		if _, err := part.Write(data); err != nil {
			return nil, fmt.Errorf("写入字幕上传表单: %w", err)
		}
		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("完成字幕上传表单: %w", err)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, host.String(), &body)
		if err != nil {
			return nil, fmt.Errorf("创建字幕上传请求: %w", err)
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		// Object storage receives the signed fields only, never the 115 Cookie.
		raw, err = c.subtitleUploadRequest(req)
		if err != nil {
			return nil, err
		}
		var result struct {
			State json.RawMessage `json:"state"`
		}
		if json.Unmarshal(raw, &result) != nil || !upstreamOK(result.State) {
			return nil, fmt.Errorf("115 未确认字幕上传成功，请核实目标目录")
		}
		return nil, nil
	})
	return err
}

func (c *ShareClient) subtitleUploadRequest(req *http.Request) ([]byte, error) {
	client := *c.HTTP
	client.Timeout = 90 * time.Second
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, fmt.Errorf("115 字幕上传请求失败，结果可能尚未确认")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("115 字幕上传返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("读取字幕上传响应失败")
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("字幕上传响应过大")
	}
	return raw, nil
}
