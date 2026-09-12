package p115

import (
	"fmt"
	"net/url"
	"strings"
)

// ShareInfo 表示 115 分享链接中的公开参数。
type ShareInfo struct {
	Code     string
	Password string
	URL      string
}

// ParseShareURL 解析 115.com/115cdn.com 分享链接。
func ParseShareURL(raw string) (*ShareInfo, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("无效的分享链接")
	}
	host := u.Hostname()
	if host != "115.com" && host != "www.115.com" && host != "115cdn.com" {
		return nil, fmt.Errorf("不是 115 分享链接")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "s" || parts[1] == "" {
		return nil, fmt.Errorf("分享链接格式应为 /s/{分享码}")
	}
	return &ShareInfo{Code: parts[1], Password: u.Query().Get("password"), URL: raw}, nil
}
