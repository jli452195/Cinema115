package httpapi

import (
	"context"
	"errors"
	"net/http"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

// Only the request context proves that the client canceled. An unexplained
// upstream cancellation is an upstream failure, never a binding denial.
func playbackAuthorizationFailure(ctx context.Context, err error) (string, int) {
	var admission *service.PoolAdmissionError
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return "播放请求已取消，未判定账号绑定失效", 499
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return "播放权限查询超时，请稍后重试", http.StatusGatewayTimeout
	case errors.Is(err, service.ErrPoolCooling):
		return "共享小号正在冷却，请稍后重试", http.StatusServiceUnavailable
	case errors.Is(err, service.ErrUserPlaybackStopped):
		return "USER_PLAYBACK_STOPPED：此播放会话已结束，请重新发起播放", http.StatusConflict
	case errors.Is(err, service.ErrUserPlaybackBusy):
		return "播放会话管理暂忙，请稍后重试", http.StatusServiceUnavailable
	case errors.Is(err, service.ErrPoolInvalid):
		return "共享小号登录或目录失效，请联系管理员处理", http.StatusServiceUnavailable
	case errors.Is(err, service.ErrPoolEmpty):
		return "共享池没有可用小号，请联系管理员完成配置", http.StatusServiceUnavailable
	case errors.As(err, &admission):
		return admission.Error(), http.StatusServiceUnavailable
	case errors.Is(err, service.ErrPoolBusy):
		return "共享池会话已满或正在检查，请稍后重试", http.StatusServiceUnavailable
	case errors.Is(err, service.ErrPoolUnavailable), errors.Is(err, p115.ErrRateQueueFull), errors.Is(err, p115.ErrShareThrottled):
		return "共享池暂无可用容量或正在冷却，请稍后重试", http.StatusServiceUnavailable
	case errors.Is(err, service.ErrPlaybackIdentityUnavailable), errors.Is(err, context.Canceled):
		return "Emby 播放身份查询暂不可用，请稍后重试", http.StatusBadGateway
	default:
		return "播放权限或账号绑定不可用，已停止取链", http.StatusForbidden
	}
}
