package httpapi

import (
	"encoding/json"
	"strings"
)

// Only use an unambiguous static STRM returned by the current user's item
// request. Missing or dynamic sources keep the existing negotiation path.
// This data is used internally for resolution, never as player PlaybackInfo.
func (h *embyProxyHandler) itemPlaybackSource(sources []map[string]json.RawMessage, id string) (map[string]json.RawMessage, *sharePlaybackParams) {
	var selected map[string]json.RawMessage
	for _, source := range sources {
		if id != "" && jsonString(source["Id"]) != id {
			continue
		}
		if selected != nil {
			return nil, nil
		}
		selected = source
	}
	if selected == nil || jsonString(selected["Id"]) == "" || jsonTrue(selected["RequiresOpening"]) || jsonString(selected["OpenToken"]) != "" || jsonString(selected["LiveStreamId"]) != "" {
		return nil, nil
	}
	if kind := jsonString(selected["Type"]); kind != "" && !strings.EqualFold(kind, "Default") {
		return nil, nil
	}
	if supported, exists := selected["SupportsDirectPlay"]; exists && !jsonTrue(supported) {
		return nil, nil
	}
	claim, err := h.playbackClaim(selected)
	if err != nil {
		return nil, nil
	}
	return selected, claim
}
