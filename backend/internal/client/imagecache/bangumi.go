package imagecache

import (
	"net/url"
	"regexp"
	"strings"

	"strm115/backend/internal/netproxy"
)

var bangumiImageDirectory = regexp.MustCompile(`^[lcmsg]-[a-fA-F0-9]{2}-[a-fA-F0-9]{2}$`)
var bangumiImagePath = regexp.MustCompile(`^/pic/cover/([lcmsg])/([a-fA-F0-9]{2})/([a-fA-F0-9]{2})/([^/]+)$`)

func NewBangumi(root string) (*Cache, error) {
	cache, err := New(root, 200<<20)
	if err != nil {
		return nil, err
	}
	cache.bgm = true
	cache.baseURL = "https://lain.bgm.tv"
	cache.http.Transport = netproxy.Transport("bangumi")
	cache.downloads = make(chan struct{}, 2)
	return cache, nil
}

func validBangumiImage(directory, name string) bool {
	return bangumiImageDirectory.MatchString(directory) && validName(name)
}

// Covers are served from one fixed origin; callers cannot supply remote URLs.
func BangumiCoverURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host != "lain.bgm.tv" || u.Scheme != "https" && u.Scheme != "http" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" {
		return ""
	}
	parts := bangumiImagePath.FindStringSubmatch(u.Path)
	if len(parts) != 5 {
		return ""
	}
	directory := strings.Join(parts[1:4], "-")
	if !validBangumiImage(directory, parts[4]) {
		return ""
	}
	return "/api/bangumi/image/" + directory + "/" + parts[4]
}
