package clouddrive2

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

type directoryCacheKey struct{}
type directoryKey struct {
	client *Client
	path   string
}
type directorySnapshot struct {
	files   []File
	forced  bool
	version uint64
	at      time.Time
	done    chan struct{}
}
type directoryCache struct {
	mu      sync.Mutex
	entries map[directoryKey]*directorySnapshot
}

// WithDirectoryCache 为一次文件组操作创建短期目录快照，合并重复查询及并发读取。
func WithDirectoryCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, directoryCacheKey{}, &directoryCache{entries: make(map[directoryKey]*directorySnapshot)})
}

func withDirectoryCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(directoryCacheKey{}).(*directoryCache); ok {
		return ctx
	}
	return WithDirectoryCache(ctx)
}

func (c *Client) directoryVersion(directory string) uint64 {
	c.operationsMu.Lock()
	defer c.operationsMu.Unlock()
	if c.directoryVersions == nil {
		c.directoryVersions = make(map[string]uint64)
	}
	if version, ok := c.directoryVersions[directory]; ok {
		return version
	}
	c.directoryVersions[directory] = c.version
	return c.version
}

func (c *Client) invalidateDirectories(directories ...string) {
	c.operationsMu.Lock()
	defer c.operationsMu.Unlock()
	c.version++
	for known := range c.directoryVersions {
		for _, directory := range directories {
			if known == directory || strings.HasPrefix(known, strings.TrimRight(directory, "/")+"/") {
				c.directoryVersions[known] = c.version
				break
			}
		}
	}
	// 快照只在单次操作中存活；定期回收路径版本，避免长期运行累积目录名。
	if len(c.directoryVersions) > 4096 {
		clear(c.directoryVersions)
	}
}

func (c *Client) cachedList(ctx context.Context, directory string, force bool) ([]File, error) {
	cache, _ := ctx.Value(directoryCacheKey{}).(*directoryCache)
	if cache == nil {
		return c.list(ctx, directory, force)
	}
	key := directoryKey{client: c, path: directory}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		version := c.directoryVersion(directory)
		cache.mu.Lock()
		entry := cache.entries[key]
		if entry != nil && entry.done != nil {
			done := entry.done
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		if entry != nil && entry.version == version && (!force || entry.forced) && time.Since(entry.at) < 2*time.Second {
			files := slices.Clone(entry.files)
			cache.mu.Unlock()
			return files, nil
		}
		entry = &directorySnapshot{forced: force, version: version, done: make(chan struct{})}
		cache.entries[key] = entry
		cache.mu.Unlock()
		files, err := c.list(ctx, directory, force)
		cache.mu.Lock()
		if err != nil {
			delete(cache.entries, key)
		} else {
			entry.files, entry.at = slices.Clone(files), time.Now()
		}
		close(entry.done)
		entry.done = nil
		cache.mu.Unlock()
		return files, err
	}
}

func overlappingDirectories(a, b string) bool {
	return a == b || strings.HasPrefix(a, strings.TrimRight(b, "/")+"/") || strings.HasPrefix(b, strings.TrimRight(a, "/")+"/")
}

// lockWrite 仅协调发生路径冲突的写操作，不让无关目录被全局网络锁阻塞。
func (c *Client) lockWrite(ctx context.Context, directories ...string) (func(), error) {
	keys := make([]string, 0, len(directories))
	for _, directory := range directories {
		clean := path.Clean(directory)
		if !slices.Contains(keys, clean) {
			keys = append(keys, clean)
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.operationsMu.Lock()
		if c.writesChanged == nil {
			c.writesChanged = make(chan struct{})
			c.writePaths = make(map[string]bool)
		}
		busy := false
		for active := range c.writePaths {
			for _, key := range keys {
				if overlappingDirectories(active, key) {
					busy = true
					break
				}
			}
			if busy {
				break
			}
		}
		if !busy {
			for _, key := range keys {
				c.writePaths[key] = true
			}
			c.operationsMu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					c.operationsMu.Lock()
					for _, key := range keys {
						delete(c.writePaths, key)
					}
					close(c.writesChanged)
					c.writesChanged = make(chan struct{})
					c.operationsMu.Unlock()
				})
			}, nil
		}
		changed := c.writesChanged
		c.operationsMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
