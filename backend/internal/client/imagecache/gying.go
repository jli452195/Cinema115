package imagecache

import "strings"

const GyingMaxBytes = int64(200 << 20)

// 观影封面使用独立缓存及固定图片源，不复用观影登录客户端或凭据。
func NewGying(root string) (*Cache, error) {
	cache, err := New(root, GyingMaxBytes)
	if err != nil {
		return nil, err
	}
	cache.gying = true
	cache.baseURL = "https://s.tutu.pm"
	cache.downloads = make(chan struct{}, 2)
	return cache, nil
}

func validGyingCover(kind, id string) bool {
	switch kind {
	case "mv", "tv", "ac", "zy":
	default:
		return false
	}
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func validGyingImage(kind, name string) bool {
	return strings.HasSuffix(name, ".webp") && validGyingCover(kind, strings.TrimSuffix(name, ".webp"))
}

// 搜索已包含类型和编号，生成封面地址不增加上游请求。
func GyingCoverURL(kind, id string) string {
	if !validGyingCover(kind, id) {
		return ""
	}
	return "/api/gying/image/" + kind + "/" + id + ".webp"
}
