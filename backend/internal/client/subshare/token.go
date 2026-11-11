package subshare

import (
	"context"
	"errors"
)

var (
	ErrInput       = errors.New("请输入有效的 GitHub Token")
	ErrToken       = errors.New("GitHub Token 无效或已过期，请在字幕设置中重新配置")
	ErrRateLimited = errors.New("GitHub API 请求已达配额，请稍后重试；未配置 Token 时可在字幕设置中配置")
	ErrForbidden   = errors.New("GitHub 拒绝访问字幕库，请检查 Token 的公共仓库读取权限及网络设置")
	ErrUnavailable = errors.New("字幕库暂时无法访问，请检查网络或代理后重试")
)

func ValidToken(token string) bool {
	if len(token) < 20 || len(token) > 255 {
		return false
	}
	for _, ch := range token {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
			return false
		}
	}
	return true
}

func (c *Client) SetToken(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

// 用独立客户端核对候选凭据，验证失败不影响当前字幕请求或缓存。
func (c *Client) ValidateToken(ctx context.Context, token string) error {
	if !ValidToken(token) {
		return ErrInput
	}
	probe := New("")
	probe.API, probe.HTTP = c.API, c.HTTP
	probe.SetToken(token)
	_, err := probe.currentRevision(ctx)
	return err
}
