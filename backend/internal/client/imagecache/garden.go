package imagecache

import (
	"regexp"

	"strm115/backend/internal/netproxy"
)

var gardenSubjectID = regexp.MustCompile(`^[1-9][0-9]{0,7}$`)

func NewGarden(root string) (*Cache, error) {
	c, err := New(root, 200<<20)
	if err != nil {
		return nil, err
	}
	c.garden = true
	c.baseURL = "https://bgm.animes.garden"
	c.http.Transport = netproxy.Transport("garden")
	c.downloads = make(chan struct{}, 2)
	return c, nil
}
