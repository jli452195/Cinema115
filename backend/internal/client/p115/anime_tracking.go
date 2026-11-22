package p115

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"

	"strm115/backend/internal/model"
)

func parseAnimeTaskStatus(raw []byte, hash string, allowFinishedDisplayProgress bool) (model.AnimeOfflineStatus, error) {
	identity, err := parseAnimeTask(raw, hash)
	if err != nil {
		return model.AnimeOfflineStatus{}, err
	}
	out := model.AnimeOfflineStatus{SchemaVersion: 1, Source: model.AnimeOfflineExact, Present: identity.Present, InfoHash: identity.InfoHash, TargetCID: identity.TargetCID, ResultID: identity.ResultID, State: "unknown"}
	if !identity.Present {
		// parseAnimeTask 已经校验任务确实未命中的响应。
		return out, nil
	}
	envelope, _, _, err := animeEnvelope(raw)
	if err != nil {
		return model.AnimeOfflineStatus{}, err
	}
	payload, err := animePayload(envelope)
	if err != nil {
		return model.AnimeOfflineStatus{}, err
	}
	var number json.Number
	if json.Unmarshal(payload["status"], &number) == nil {
		if status, err := number.Int64(); err == nil && status >= math.MinInt32 && status <= math.MaxInt32 {
			out.Status = &status
		}
	}
	if out.Status == nil {
		out.Malformed = true
	}
	var display string
	if json.Unmarshal(payload["display_status"], &display) != nil {
		out.Malformed = true
	} else {
		display = strings.ToLower(strings.TrimSpace(display))
		switch display {
		case "pending", "waiting", "parsing", "running", "downloading", "finished", "completed", "success", "failed", "stopped", "cancelled", "canceled":
			out.DisplayStatus = display
		default:
			out.Malformed = true
		}
	}
	finishedWithResult := allowFinishedDisplayProgress && out.Status != nil && *out.Status == 1 &&
		out.DisplayStatus == "finished" && out.ResultID != ""
	for _, key := range []string{"percentDone", "display_percent", "percent_done", "progress", "percent"} {
		value, exists := payload[key]
		if !exists {
			continue
		}
		var n json.Number
		if json.Unmarshal(value, &n) != nil {
			out.Malformed = true
			continue
		}
		progress, err := n.Float64()
		if err != nil || math.IsInf(progress, 0) || math.IsNaN(progress) || progress < 0 || progress > 100 {
			out.Malformed = true
			continue
		}
		if out.Progress != nil && *out.Progress != progress {
			// 115 的已完成任务仍可能返回 percentDone=0，此处只兼容实测的 0/100 组合。
			if !finishedWithResult || key != "display_percent" || *out.Progress != 0 || progress != 100 {
				out.Malformed = true
				continue
			}
		}
		out.Progress = &progress
	}
	// Error text is neither persisted nor interpreted as proof of completion.
	for _, key := range []string{"error", "error_msg"} {
		if value, exists := payload[key]; exists && string(value) != `""` && string(value) != "null" {
			switch out.DisplayStatus {
			case "failed", "stopped", "cancelled", "canceled":
			default:
				out.Malformed = true
			}
		}
	}
	out.State = out.NormalizedState()
	if !out.Valid() {
		return model.AnimeOfflineStatus{}, ErrAnimeOfflineResponse
	}
	return out, nil
}

// ReadAnimeTaskStatus only uses exact-hash GET. A partial/missing list never
// proves absence. Missing means the strict 10021 envelope, not a resubmit grant.
func (c *CloudClient) ReadAnimeTaskStatus(ctx context.Context, cookie, hash string) (model.AnimeOfflineStatus, error) {
	return c.readAnimeTaskStatus(ctx, cookie, hash, false)
}

// ReadAnimeSubscriptionTaskStatus 按精确 Hash 查询，并兼容动漫订阅实测的完成进度。
func (c *CloudClient) ReadAnimeSubscriptionTaskStatus(ctx context.Context, cookie, hash string) (model.AnimeOfflineStatus, error) {
	return c.readAnimeTaskStatus(ctx, cookie, hash, true)
}

func (c *CloudClient) readAnimeTaskStatus(ctx context.Context, cookie, hash string, allowFinishedDisplayProgress bool) (model.AnimeOfflineStatus, error) {
	if normalized, ok := normalizeTaskHash(hash, "magnet"); !ok || normalized != hash || len(hash) != 40 {
		return model.AnimeOfflineStatus{}, ErrCloudInput
	}
	raw, err := c.webRequest(ctx, cookie, http.MethodGet, c.BaseURL+"/web/", url.Values{"ac": {"get_user_task"}, "info_hash": {hash}}, nil)
	if err != nil {
		return model.AnimeOfflineStatus{}, err
	}
	return parseAnimeTaskStatus(raw, hash, allowFinishedDisplayProgress)
}
