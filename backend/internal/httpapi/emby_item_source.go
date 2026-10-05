package httpapi

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Only recognized share URL formats with a definite syntax error fail early.
// Missing local files, unknown formats and dynamic media still negotiate.
func isMalformedShareURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if u.Path == "/api/" {
		q, _ := url.ParseQuery(u.RawQuery)
		_, code := q["share_code"]
		_, password := q["receive_code"]
		return code || password
	}
	return strings.HasPrefix(u.Path, "/api/share115/") || strings.HasPrefix(u.Path, "/s/")
}

// Only use an unambiguous static STRM returned by the current user's item
// request. Missing or dynamic sources keep the existing negotiation path.
// This data is used internally for resolution, never as player PlaybackInfo.
func (h *embyProxyHandler) itemPlaybackSource(sources []map[string]json.RawMessage, id string) (map[string]json.RawMessage, *sharePlaybackParams, error) {
	var selected map[string]json.RawMessage
	for _, source := range sources {
		if id != "" && jsonString(source["Id"]) != id {
			continue
		}
		if selected != nil {
			return nil, nil, nil
		}
		selected = source
	}
	if selected == nil || jsonString(selected["Id"]) == "" || jsonTrue(selected["RequiresOpening"]) || jsonString(selected["OpenToken"]) != "" || jsonString(selected["LiveStreamId"]) != "" {
		return nil, nil, nil
	}
	if kind := jsonString(selected["Type"]); kind != "" && !strings.EqualFold(kind, "Default") {
		return nil, nil, nil
	}
	if supported, exists := selected["SupportsDirectPlay"]; exists && !jsonTrue(supported) {
		return nil, nil, nil
	}
	raw, readErr := h.sourceURL(selected)
	if readErr != nil {
		return nil, nil, nil
	}
	claim, err := h.playbackURLClaim(raw)
	if err != nil {
		if isMalformedShareURL(raw) {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	return selected, claim, nil
}
