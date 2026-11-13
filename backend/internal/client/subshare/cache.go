package subshare

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"strm115/backend/internal/utils/subtitle"
)

// 停更仓库的元数据永久复用；缓存缺失或损坏时才重新获取。
func (c *Client) readCache(name string, limit int64) []byte {
	if c.CacheDir == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(c.CacheDir, name))
	if err != nil {
		return nil
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil
	}
	return body
}

// 只在内容完整且校验通过后调用，临时文件避免中断留下半份缓存。
func (c *Client) writeCache(name string, body []byte) error {
	if c.CacheDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.CacheDir, 0700); err != nil {
		return fmt.Errorf("创建字幕缓存目录: %w", err)
	}
	f, err := os.CreateTemp(c.CacheDir, "cache-*.tmp")
	if err != nil {
		return fmt.Errorf("创建字幕缓存文件: %w", err)
	}
	defer os.Remove(f.Name())
	_, err = f.Write(body)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("写入字幕缓存: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭字幕缓存: %w", closeErr)
	}
	if err = os.Rename(f.Name(), filepath.Join(c.CacheDir, name)); err != nil {
		return fmt.Errorf("保存字幕缓存: %w", err)
	}
	return nil
}

func validListing(files []File, directory, revision string) bool {
	if len(files) == 0 || len(files) > 2000 {
		return false
	}
	for _, f := range files {
		if f.Revision != revision || !validSHA(f.SHA) || !validPath(f.Path) || !strings.HasPrefix(f.Path, directory+"/") || f.Name != path.Base(f.Path) || !subtitle.Supported(f.Name) || f.Size <= 0 || f.Size > 16<<20 {
			return false
		}
	}
	return true
}

func (c *Client) rememberFiles(id string, files []File) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.lists) >= 64 {
		for key := range c.lists {
			delete(c.lists, key)
			break
		}
	}
	c.lists[id] = files
}
