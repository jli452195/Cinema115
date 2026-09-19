package imagecache

import (
	"net/url"
	"strings"
	"time"
)

const MikanMaxBytes = int64(200 << 20)

// 蜜柑封面使用独立实例及目录，只允许固定站点、月份目录和图片文件名。
func NewMikan(root string) (*Cache, error) {
	cache, err := New(root, MikanMaxBytes)
	if err != nil {
		return nil, err
	}
	cache.mikan = true
	cache.baseURL = "https://mikanani.me"
	cache.downloads = make(chan struct{}, 2)
	return cache, nil
}

func validMikanMonth(month string) bool {
	if len(month) != 6 {
		return false
	}
	_, err := time.Parse("200601", month)
	return err == nil
}

// 对外只返回同源代理路径，不允许客户端提供域名、尺寸或任意上游地址。
func MikanCoverURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return ""
	}
	if u.IsAbs() && (u.Scheme != "https" || u.Host != "mikanani.me") {
		return ""
	}
	if !u.IsAbs() && u.Host != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/images/Bangumi/") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/images/Bangumi/"), "/")
	if len(parts) != 2 || !validMikanMonth(parts[0]) || !validName(parts[1]) {
		return ""
	}
	return "/api/mikan/image/" + parts[0] + "/" + parts[1]
}
