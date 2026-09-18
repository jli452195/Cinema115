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

// ParseShareURL parses supported public share aliases and returns one canonical
// 115cdn.com URL. anxia.com is only an alias at this boundary; downstream code
// never needs domain-specific handling.
func ParseShareURL(raw string) (*ShareInfo, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Port() != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("无效的分享链接")
	}
	host := strings.ToLower(u.Hostname())
	if host != "115.com" && host != "www.115.com" && host != "115cdn.com" && host != "www.115cdn.com" && host != "anxia.com" && host != "www.anxia.com" {
		return nil, fmt.Errorf("不是 115 分享链接")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "s" || parts[1] == "" {
		return nil, fmt.Errorf("分享链接格式应为 /s/{分享码}")
	}
	password := strings.TrimSpace(u.Query().Get("password"))
	canonical := "https://115cdn.com/s/" + url.PathEscape(parts[1])
	if password != "" {
		canonical += "?password=" + url.QueryEscape(password)
	}
	return &ShareInfo{Code: parts[1], Password: password, URL: canonical}, nil
}
