package p115

import (
	"context"
	"fmt"
)

// PlaybackRangeSHA1 reads only the challenge range using the source's pinned
// credentials to acquire a URL. The CDN request itself receives no credentials.
func (c *ShareClient) PlaybackRangeSHA1(ctx context.Context, info *DownloadInfo, start, end int64) (string, error) {
	if info == nil || start < 0 || end < start || end >= info.Size || end-start+1 > MaxInstantRangeBytes {
		return "", fmt.Errorf("秒传校验范围无效")
	}
	link := info.URL
	if info.loadPlaybackURL != nil {
		var err error
		link, err = info.loadPlaybackURL(ctx)
		if err != nil {
			return "", fmt.Errorf("获取二次校验源直链: %w", err)
		}
	}
	return c.instantRangeHash(ctx, link, start, end, info.Size)
}
