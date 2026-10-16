package imagecache

import (
	"context"
	"net/http"
	"strings"
)

// The resolver supplies current credentials for one request; they are never persisted.
type EmbyRequest func(context.Context, string, string) (*http.Request, error)

func NewEmby(root string, request EmbyRequest) (*Cache, error) {
	if request == nil {
		return nil, ErrInput
	}
	cache, err := New(root, 200<<20)
	if err != nil {
		return nil, err
	}
	cache.embyRequest = request
	cache.downloads = make(chan struct{}, 2)
	return cache, nil
}

func validEmbyImage(scope, name string) bool {
	if len(scope) != 64 || strings.Trim(scope, "0123456789abcdef") != "" || !strings.HasSuffix(name, ".webp") {
		return false
	}
	id := strings.TrimSuffix(name, ".webp")
	return len(id) > 0 && len(id) <= 128 && strings.Trim(id, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-_") == ""
}

func EmbyPosterURL(scope, id string) string {
	if !validEmbyImage(scope, id+".webp") {
		return ""
	}
	return "/api/admin/emby/posters/" + scope + "/" + id
}
