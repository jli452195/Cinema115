package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Canonicalize only parameters interpreted by the proxy. Values remain case
// sensitive; repeated equal values are harmless, conflicting values fail closed.
var playbackParameters = []string{"ItemId", "MediaSourceId", "PlaySessionId", "UserId", "Static", "api_key", "X-Emby-Token"}

func uniqueProxyValue(q url.Values, name string) (string, bool, error) {
	value, found := "", false
	for key, values := range q {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, candidate := range values {
			if found && candidate != value {
				return "", false, fmt.Errorf("播放参数 %s 存在冲突，请重新选择媒体源或重新发起播放", name)
			}
			value, found = candidate, true
		}
	}
	return value, found, nil
}

func normalizePlaybackQuery(r *http.Request) error {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return fmt.Errorf("播放查询参数编码无效")
	}
	changed := false
	for _, name := range playbackParameters {
		value, found, err := uniqueProxyValue(q, name)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		for key, values := range q {
			if strings.EqualFold(key, name) {
				if key != name || len(values) != 1 {
					changed = true
				}
				delete(q, key)
			}
		}
		q.Set(name, value)
	}
	if changed {
		r.URL.RawQuery = q.Encode()
	}
	return nil
}

// Decode top-level keys before a map can discard exact duplicate JSON keys.
// Only identity parameters are constrained; stream lists and unrelated metadata
// are retained. The caller decides whether the body is forwarded or rewritten.
func playbackBody(data []byte, query url.Values) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("播放参数必须为 JSON 对象")
	}
	body := make(map[string]json.RawMessage)
	seen := make(map[string]string, 4)
	for d.More() {
		keyToken, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("播放参数名称无效")
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, err
		}
		for _, name := range playbackParameters[:4] {
			if !strings.EqualFold(key, name) {
				continue
			}
			var value *string
			if json.Unmarshal(raw, &value) != nil {
				return nil, fmt.Errorf("播放参数 %s 必须为字符串或 null", name)
			}
			// Optional null fields are unspecified, as in Emby's request models.
			// Keep them distinct from empty strings when detecting duplicates.
			canonical := "null"
			if value != nil {
				canonical = "string:" + *value
			}
			if previous, found := seen[name]; found && previous != canonical {
				return nil, fmt.Errorf("播放参数 %s 在请求体中冲突", name)
			}
			seen[name] = canonical
			if q, found, err := uniqueProxyValue(query, name); err != nil {
				return nil, err
			} else if found && value != nil && q != *value {
				return nil, fmt.Errorf("播放参数 %s 在地址与请求体中冲突", name)
			}
			key = name
			break
		}
		body[key] = raw
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("播放参数包含多余内容")
	}
	return body, nil
}

func validatePlaybackEventBody(r *http.Request) error {
	path := strings.TrimPrefix(strings.Trim(strings.ToLower(r.URL.Path), "/"), "emby/")
	if r.Method != http.MethodPost || r.Body == nil || (path != "sessions/playing" && path != "sessions/playing/progress" && path != "sessions/playing/stopped") {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (16<<10)+1))
	r.Body = &embyReplayBody{Reader: io.MultiReader(bytes.NewReader(data), r.Body), closer: r.Body}
	if err != nil || len(data) > 16<<10 {
		return fmt.Errorf("播放事件过大或读取失败")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	_, err = playbackBody(data, r.URL.Query())
	return err
}
