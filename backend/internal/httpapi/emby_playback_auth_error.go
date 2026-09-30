package httpapi

import (
	"context"
	"errors"
	"net/http"

	"strm115/backend/internal/service"
)

// Only the request context proves that the client canceled. An unexplained
// upstream cancellation is an upstream failure, never a binding denial.
func playbackAuthorizationFailure(ctx context.Context, err error) (string, int) {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return "播放请求已取消，未判定账号绑定失效", 499
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return "播放权限查询超时，请稍后重试", http.StatusGatewayTimeout
	case errors.Is(err, service.ErrPlaybackIdentityUnavailable), errors.Is(err, context.Canceled):
		return "Emby 播放身份查询暂不可用，请稍后重试", http.StatusBadGateway
	default:
		return "播放权限或账号绑定不可用，已停止取链", http.StatusForbidden
	}
}
