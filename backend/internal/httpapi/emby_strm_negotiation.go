package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Query metadata before Emby opens the original STRM URL. Only recognized
// sources bypass opening; ordinary media keeps its original negotiation.
type strmNegotiationTransport struct {
	handler *embyProxyHandler
	next    http.RoundTripper
}

func metadataOnlyQuery(query url.Values) {
	for key := range query {
		if strings.EqualFold(key, "IsPlayback") || strings.EqualFold(key, "AutoOpenLiveStream") || strings.EqualFold(key, "EnableTranscoding") {
			delete(query, key)
		}
	}
	query.Set("IsPlayback", "false")
	query.Set("AutoOpenLiveStream", "false")
	query.Set("EnableTranscoding", "false")
}

func (t *strmNegotiationTransport) RoundTrip(original *http.Request) (*http.Response, error) {
	if playbackItem(original.URL.Path) == "" || (original.Method != http.MethodGet && original.Method != http.MethodPost) {
		return t.next.RoundTrip(original)
	}
	probe := original.Clone(original.Context())
	query := probe.URL.Query()
	metadataOnlyQuery(query)
	probe.URL.RawQuery = query.Encode()
	if original.Method == http.MethodPost && original.Body != nil {
		data, err := io.ReadAll(io.LimitReader(original.Body, (1<<20)+1))
		original.Body.Close()
		if err != nil || len(data) > 1<<20 {
			return nil, fmt.Errorf("播放参数读取失败")
		}
		original.Body = io.NopCloser(bytes.NewReader(data))
		// RoundTrip owns the request body, including when no fallback is needed.
		defer original.Body.Close()
		var body map[string]json.RawMessage
		if len(bytes.TrimSpace(data)) > 0 && json.Unmarshal(data, &body) != nil {
			return nil, fmt.Errorf("播放参数格式无效")
		}
		if body == nil {
			body = make(map[string]json.RawMessage)
		}
		for key := range body {
			if strings.EqualFold(key, "IsPlayback") || strings.EqualFold(key, "AutoOpenLiveStream") || strings.EqualFold(key, "EnableTranscoding") {
				delete(body, key)
			}
		}
		body["IsPlayback"], body["AutoOpenLiveStream"], body["EnableTranscoding"] = json.RawMessage("false"), json.RawMessage("false"), json.RawMessage("false")
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		probe.Body = io.NopCloser(bytes.NewReader(payload))
		probe.ContentLength = int64(len(payload))
		probe.Header.Set("Content-Length", strconv.Itoa(len(payload)))
	}
	response, err := t.next.RoundTrip(probe)
	if err != nil {
		return nil, err
	}
	// Preserve original user authentication and selected-source context for rewriting.
	response.Request = original
	if response.StatusCode != http.StatusOK {
		return response, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	response.Body.Close()
	if err != nil || len(data) > 8<<20 {
		return nil, fmt.Errorf("Emby 播放信息读取失败或超出限制")
	}
	var info struct{ MediaSources []map[string]json.RawMessage }
	if json.Unmarshal(data, &info) != nil {
		return nil, fmt.Errorf("Emby 播放信息格式无效")
	}
	selected := proxyQuery(original.URL.Query(), "MediaSourceId")
	if selected == "" {
		selected, _ = original.Context().Value(playbackSelectedSourceKey{}).(string)
	}
	supported, matched := true, false
	for _, source := range info.MediaSources {
		if selected != "" && jsonString(source["Id"]) != selected {
			continue
		}
		matched = true
		if _, err := t.handler.playbackClaim(source); err != nil {
			supported = false
			break
		}
	}
	if supported && matched {
		response.Body = io.NopCloser(bytes.NewReader(data))
		t.handler.logs.Add(original.Context(), "INFO", "已识别 STRM，使用免探测播放协商；不访问原工具地址，继续校验原文件直放能力")
		return response, nil
	}
	t.handler.logs.Add(original.Context(), "DEBUG", "媒体源未全部识别为受支持 STRM，保留 Emby 原播放协商")
	return t.next.RoundTrip(original)
}
