package emby

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type PlaybackSession struct {
	UserID           string    `json:"UserId"`
	PlaySessionID    string    `json:"PlaySessionId"`
	LastActivityDate time.Time `json:"LastActivityDate"`
	NowPlayingItem   struct {
		ID string `json:"Id"`
	} `json:"NowPlayingItem"`
	PlayState struct {
		PlaySessionID string `json:"PlaySessionId"`
	} `json:"PlayState"`
}

// Read-only metadata; never changes permissions or stops media.
func (c *Client) PlaybackSessions(ctx context.Context) ([]PlaybackSession, error) {
	var sessions []PlaybackSession
	if err := c.get(ctx, "/Sessions", url.Values{"ActiveWithinSeconds": {"90"}}, &sessions); err != nil {
		return nil, err
	}
	if sessions == nil || len(sessions) > 4096 {
		return nil, fmt.Errorf("Emby 会话列表无效或超过上限")
	}
	return sessions, nil
}
