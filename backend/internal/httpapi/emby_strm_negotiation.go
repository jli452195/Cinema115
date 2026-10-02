package httpapi

import (
	"net/url"
	"strings"
)

// Internal fallback lookups must not start a second playback session. Client
// PlaybackInfo requests retain their original negotiation flags and pass
// through disablePlaybackTranscoding before reaching Emby.
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
