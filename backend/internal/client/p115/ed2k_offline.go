package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ED2KTaskObservation 只保留精确任务身份和状态，不持久化上游原始响应。
type ED2KTaskObservation struct {
	Present   bool
	Hash      string
	TargetCID string
	ResultID  string
	State     string
}

// ED2KTaskRejection 仅公开经过解析的数字业务码。
type ED2KTaskRejection struct {
	Code int64
}

func (e *ED2KTaskRejection) Error() string {
	return fmt.Sprintf("115 离线任务查询被拒绝（业务码 %d）", e.Code)
}

func (e *ED2KTaskRejection) Unwrap() error { return ErrAnimeOfflineResponse }

func (c *CloudClient) InspectED2KTask(ctx context.Context, cookie, hash string) (ED2KTaskObservation, error) {
	if normalized, ok := normalizeTaskHash(hash, "ed2k"); !ok || normalized != hash {
		return ED2KTaskObservation{}, ErrCloudInput
	}
	raw, err := c.webRequest(ctx, cookie, http.MethodGet, c.BaseURL+"/web/", url.Values{"ac": {"get_user_task"}, "info_hash": {hash}}, nil)
	if err != nil {
		return ED2KTaskObservation{}, err
	}
	return parseED2KTask(raw, hash)
}

func parseED2KTask(raw []byte, hash string) (ED2KTaskObservation, error) {
	var out ED2KTaskObservation
	m, ok, code, err := animeEnvelope(raw)
	if err != nil {
		return out, err
	}
	if !ok {
		if code == 10021 && animeMissingTaskDefaults(m) {
			return out, nil
		}
		if code != 0 {
			return out, &ED2KTaskRejection{Code: code}
		}
		return out, ErrAnimeOfflineResponse
	}
	p, err := animePayload(m)
	if err != nil {
		return out, err
	}
	for _, key := range []string{"info_hash", "infoHash", "hash"} {
		if v, exists := p[key]; exists {
			var text string
			if json.Unmarshal(v, &text) != nil {
				return out, ErrAnimeOfflineResponse
			}
			n, valid := normalizeTaskHash(text, "ed2k")
			if !valid || n != hash {
				return out, ErrAnimeOfflineResponse
			}
			out.Hash = n
		}
	}
	if out.Hash == "" {
		return out, ErrAnimeOfflineResponse
	}
	out.TargetCID, err = animeTaskID(p, "wp_path_id")
	if err != nil {
		return out, err
	}
	if v, exists := p["file_id"]; exists && string(v) != `""` && string(v) != "null" {
		out.ResultID, err = animeTaskID(p, "file_id")
		if err != nil {
			return out, err
		}
	}
	out.Present, out.State = true, "unknown"
	var display string
	if json.Unmarshal(p["display_status"], &display) != nil {
		return out, fmt.Errorf("ED2K 任务状态字段不完整")
	}
	switch strings.ToLower(strings.TrimSpace(display)) {
	case "failed", "stopped", "cancelled", "canceled":
		out.State = "failed"
	case "pending", "waiting", "parsing", "running", "downloading":
		out.State = "downloading"
	case "finished", "completed", "success":
		for _, key := range []string{"percentDone", "display_percent", "percent_done", "progress", "percent"} {
			if v, exists := p[key]; exists {
				var n json.Number
				if json.Unmarshal(v, &n) != nil {
					return out, ErrAnimeOfflineResponse
				}
				percent, e := n.Float64()
				if e != nil || percent != 100 {
					return out, ErrAnimeOfflineResponse
				}
				out.State = "completed"
			}
		}
	}
	return out, nil
}
