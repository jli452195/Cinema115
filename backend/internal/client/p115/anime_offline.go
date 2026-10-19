package p115

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

var (
	ErrAnimeOfflineResponse = errors.New("invalid anime offline response")
	ErrAnimeSubmitRejected  = errors.New("anime offline submission rejected")
)

// AnimeTaskObservation proves only exact task identity and supplied directory
// fields. It makes no claim about download status or actual file completion.
type AnimeTaskObservation struct {
	Present   bool
	InfoHash  string
	TargetCID string
	ResultID  string
}

func animeObject(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrAnimeOfflineResponse
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, ErrAnimeOfflineResponse
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrAnimeOfflineResponse
		}
		if _, exists := fields[key]; exists {
			return nil, ErrAnimeOfflineResponse
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, ErrAnimeOfflineResponse
		}
		fields[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, ErrAnimeOfflineResponse
	}
	var extra json.RawMessage
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, ErrAnimeOfflineResponse
	}
	return fields, nil
}

func animeEnvelope(raw []byte) (map[string]json.RawMessage, bool, int64, error) {
	m, err := animeObject(raw)
	if err != nil {
		return nil, false, 0, err
	}
	var ok bool
	switch string(m["state"]) {
	case "true", "1":
		ok = true
	case "false", "0":
	default:
		return nil, false, 0, ErrAnimeOfflineResponse
	}
	var code int64
	seen := false
	for _, key := range []string{"errcode", "errno", "code"} {
		if raw, exists := m[key]; exists {
			var n json.Number
			if json.Unmarshal(raw, &n) != nil {
				return nil, false, 0, ErrAnimeOfflineResponse
			}
			v, err := n.Int64()
			if err != nil || v < 0 || seen && code != v {
				return nil, false, 0, ErrAnimeOfflineResponse
			}
			code, seen = v, true
		}
	}
	if ok && code != 0 {
		return nil, false, 0, ErrAnimeOfflineResponse
	}
	return m, ok, code, nil
}

func animePayload(m map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	data, exists := m["data"]
	if !exists || string(data) == "null" {
		return m, nil
	}
	var encoded string
	if json.Unmarshal(data, &encoded) == nil {
		decoded, err := RSADecrypt(encoded)
		if err != nil {
			return nil, ErrAnimeOfflineResponse
		}
		data = decoded
	}
	payload, err := animeObject(data)
	if err != nil {
		return nil, err
	}
	if _, exists := payload["state"]; exists {
		_, ok, _, err := animeEnvelope(data)
		if err != nil || !ok {
			return nil, ErrAnimeOfflineResponse
		}
	}
	return payload, nil
}

func animeTaskID(m map[string]json.RawMessage, keys ...string) (string, error) {
	value := ""
	for _, key := range keys {
		raw, exists := m[key]
		if !exists {
			continue
		}
		var id string
		if json.Unmarshal(raw, &id) != nil {
			var n json.Number
			if json.Unmarshal(raw, &n) != nil {
				return "", ErrAnimeOfflineResponse
			}
			id = n.String()
		}
		if !validDriveID(id, false) || value != "" && value != id {
			return "", ErrAnimeOfflineResponse
		}
		value = id
	}
	return value, nil
}

func parseAnimeTask(raw []byte, hash string) (AnimeTaskObservation, error) {
	var out AnimeTaskObservation
	m, ok, code, err := animeEnvelope(raw)
	if err != nil {
		return out, err
	}
	if !ok {
		if code == 10021 && (m["data"] == nil || string(m["data"]) == "null") && m["info_hash"] == nil && m["hash"] == nil && m["infoHash"] == nil && m["file_id"] == nil && m["wp_path_id"] == nil {
			return out, nil
		}
		return out, ErrAnimeOfflineResponse
	}
	payload, err := animePayload(m)
	if err != nil {
		return out, err
	}
	for _, key := range []string{"info_hash", "infoHash", "hash"} {
		if raw, exists := payload[key]; exists {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return out, ErrAnimeOfflineResponse
			}
			got, valid := normalizeTaskHash(text, "magnet")
			if !valid || got != hash {
				return out, ErrAnimeOfflineResponse
			}
			out.InfoHash = got
		}
	}
	if out.InfoHash == "" {
		return out, ErrAnimeOfflineResponse
	}
	out.TargetCID, err = animeTaskID(payload, "wp_path_id")
	if err != nil {
		return AnimeTaskObservation{}, err
	}
	out.ResultID, err = animeTaskID(payload, "file_id")
	if err != nil {
		return AnimeTaskObservation{}, err
	}
	out.Present = true
	return out, nil
}

func (c *CloudClient) InspectAnimeTask(ctx context.Context, cookie, hash string) (AnimeTaskObservation, error) {
	if normalized, ok := normalizeTaskHash(hash, "magnet"); !ok || normalized != hash || len(hash) != 40 {
		return AnimeTaskObservation{}, ErrCloudInput
	}
	q := url.Values{"ac": {"get_user_task"}, "info_hash": {hash}}
	raw, err := c.webRequest(ctx, cookie, http.MethodGet, c.BaseURL+"/web/", q, nil)
	if err != nil {
		return AnimeTaskObservation{}, err
	}
	return parseAnimeTask(raw, hash)
}

// SubmitAnimeTask issues exactly one request without a preceding delete. A nil
// error is only a valid acceptance envelope; callers still reconcile identity.
func (c *CloudClient) SubmitAnimeTask(ctx context.Context, cookie, hash, cid string) error {
	if normalized, ok := normalizeTaskHash(hash, "magnet"); !ok || normalized != hash || len(hash) != 40 || !validDriveID(cid, false) {
		return ErrCloudInput
	}
	raw, err := c.sspRequest(ctx, cookie, "add_task_url", url.Values{"url": {"magnet:?xt=urn:btih:" + hash}, "wp_path_id": {cid}})
	if err != nil {
		return err
	}
	m, ok, code, err := animeEnvelope(raw)
	if err != nil {
		return err
	}
	if !ok {
		if code > 0 && code != 10008 && (m["data"] == nil || string(m["data"]) == "null") {
			return ErrAnimeSubmitRejected
		}
		return ErrAnimeOfflineResponse
	}
	payload, err := animePayload(m)
	if err != nil {
		return err
	}
	// Any supplied receipt identity must agree; omitted identity is resolved by
	// the subsequent exact read rather than inferred from an empty response.
	if payload["info_hash"] != nil || payload["infoHash"] != nil || payload["hash"] != nil {
		check, err := json.Marshal(map[string]any{"state": true, "data": payload})
		if err != nil {
			return ErrAnimeOfflineResponse
		}
		observation, err := parseAnimeTask(check, hash)
		if err != nil || observation.TargetCID != "" && observation.TargetCID != cid {
			return ErrAnimeOfflineResponse
		}
	}
	if raw, exists := payload["wp_path_id"]; exists {
		got, err := animeTaskID(map[string]json.RawMessage{"cid": raw}, "cid")
		if err != nil || got != cid {
			return ErrAnimeOfflineResponse
		}
	}
	return nil
}
