package imagecache

import (
	"net/url"
	"regexp"

	"strm115/backend/internal/netproxy"
)

var anibtCoverPath = regexp.MustCompile(`^/anime_covers/([A-Za-z0-9_-]{1,64})/([a-f0-9]{64})(?:\.(?:jpg|png|webp)|/v4/w(?:240|360)\.webp)$`)
var anibtCoverDirectory = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var anibtCoverName = regexp.MustCompile(`^[a-f0-9]{64}\.webp$`)

func NewAnibt(root string) (*Cache, error) {
	c, err := New(root, 200<<20)
	if err != nil {
		return nil, err
	}
	c.anibt = true
	c.baseURL = "https://r2.anibt.net"
	c.http.Transport = netproxy.Transport("anibt")
	c.downloads = make(chan struct{}, 2)
	return c, nil
}
func AnibtCoverURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "r2.anibt.net" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.EscapedPath() != u.Path {
		return ""
	}
	m := anibtCoverPath.FindStringSubmatch(u.Path)
	if m == nil {
		return ""
	}
	return "/api/anibt/image/" + m[1] + "/" + m[2] + ".webp"
}
