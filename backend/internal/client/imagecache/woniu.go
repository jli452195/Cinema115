package imagecache

import (
	"regexp"

	"strm115/backend/internal/netproxy"
)

var woniuImageName = regexp.MustCompile(`^[a-fA-F0-9]{32}\.(jpg|jpeg|png|webp)$`)
var woniuImageDirectory = regexp.MustCompile(`^(vod|[0-9]{8}(?:-[1-9][0-9]{0,3})?)$`)

func NewWoniu(root string) (*Cache, error) {
	cache, err := New(root, 200<<20)
	if err != nil {
		return nil, err
	}
	cache.woniu = true
	cache.baseURL = "https://www.wn4k.com"
	cache.http.Transport = netproxy.Transport("woniu")
	cache.downloads = make(chan struct{}, 2)
	return cache, nil
}
