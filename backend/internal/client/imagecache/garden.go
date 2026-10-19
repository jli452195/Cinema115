package imagecache

import "regexp"

var gardenSubjectID = regexp.MustCompile(`^[1-9][0-9]{0,7}$`)

func NewGarden(root string) (*Cache, error) {
	c, err := New(root, 200<<20)
	if err != nil {
		return nil, err
	}
	c.garden = true
	c.baseURL = "https://bgm.animes.garden"
	c.downloads = make(chan struct{}, 2)
	return c, nil
}
