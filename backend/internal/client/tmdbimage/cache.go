package tmdbimage

import (
	"time"

	"strm115/backend/internal/client/imagecache"
)

// 保留 TMDB 的公开接口及缓存目录格式，底层复用通用图片缓存。
type Cache = imagecache.Cache
type Asset = imagecache.Asset

var ErrInput = imagecache.ErrInput
var ErrNotFound = imagecache.ErrNotFound

const DefaultMaxBytes = imagecache.DefaultMaxBytes

func New(root string, maxBytes int64) (*Cache, error) { return imagecache.New(root, maxBytes) }
func BrowserMaxAge() time.Duration                    { return imagecache.BrowserMaxAge() }
