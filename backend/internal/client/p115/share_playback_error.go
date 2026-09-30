package p115

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	ErrShareNotReadable      = errors.New("分享受限或文件对当前账号不可见")
	ErrShareLoginRequired    = errors.New("115 登录失效，请更新 Cookie")
	ErrShareThrottled        = errors.New("115 风控或限流，请稍后重试")
	ErrSharePassword         = errors.New("分享提取码无效")
	ErrShareDownloadRejected = errors.New("115 拒绝分享取链，原因未能识别")
)

// Only classify explicit API error messages, never file names or guessed error codes.
// Account/rate/password failures take precedence over share visibility hints.
func shareDownloadError(response shareResponse) error {
	message := strings.ToLower(strings.Join([]string{responseMessage(response.Error), responseMessage(response.Message), responseMessage(response.Msg), responseMessage(response.ErrorMsg)}, " "))
	contains := func(hints ...string) bool {
		for _, hint := range hints {
			if strings.Contains(message, hint) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("登录", "登陆", "cookie", "login", "log in", "账号异常", "帐号异常", "账号被禁", "帐号被禁"):
		return ErrShareLoginRequired
	case contains("频繁", "频率", "风控", "验证码", "验证身份", "稍后", "繁忙", "too many", "rate limit", "captcha"):
		return ErrShareThrottled
	case contains("提取码", "访问码", "密码", "receive_code", "password"):
		return ErrSharePassword
	case response.underReview(), contains("违规", "仅自己", "仅本人", "仅分享者", "仅限本人", "只有分享者", "无权访问", "没有权限", "无权限", "访问受限", "分享受限", "文件不存在", "文件已删除", "文件已被删除", "文件不可见", "分享不存在", "分享已取消", "分享已失效", "分享已过期", "分享链接不存在", "分享链接已失效", "file not found", "share not found", "permission denied"):
		return ErrShareNotReadable
	default:
		return ErrShareDownloadRejected
	}
}

// SharePlaybackFailureReason is safe for logs: no upstream response text or URLs.
func SharePlaybackFailureReason(err error) string {
	var completion *PlaybackCompletionError
	if errors.As(err, &completion) {
		return completion.Reason
	}
	var drive *DriveAPIError
	if errors.As(err, &drive) {
		return drive.Error()
	}
	if errors.Is(err, ErrPlaybackRangeUnavailable) {
		return ErrPlaybackRangeUnavailable.Error()
	}
	for _, known := range []error{ErrDriveFileMissing, ErrDriveRejected, ErrDriveInvalidResponse, ErrDriveLinkRestricted, ErrShareNotReadable, ErrShareLoginRequired, ErrShareThrottled, ErrSharePassword, ErrShareDownloadRejected, ErrShareUnderReview} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	if errors.Is(err, context.Canceled) {
		return "请求已取消"
	}
	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkErr) && networkErr.Timeout() {
		return "请求超时"
	}
	var upstream *HTTPError
	if errors.As(err, &upstream) {
		return fmt.Sprintf("115 返回 HTTP %d", upstream.Status)
	}
	return "网络请求失败或响应无效"
}

// Public playback must not depend on sending a 115 Cookie to the player.
func AllowedDirectPlaybackURL(link string) bool {
	if !AllowedMediaURL(link) {
		return false
	}
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	flags := q["f"]
	return len(flags) == 0 || len(flags) == 1 && (flags[0] == "" || flags[0] == "0" || flags[0] == "1")
}
