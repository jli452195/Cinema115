package imagecache

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrInput = errors.New("图片参数无效")
var ErrNotFound = errors.New("图片不存在")

const (
	DefaultMaxBytes = int64(5 << 30)
	freshTTL        = 7 * 24 * time.Hour
	retentionTTL    = 90 * 24 * time.Hour
	browserMaxAge   = 24 * time.Hour
	maxImageBytes   = int64(16 << 20)
	maxPending      = 256
)

var allowedSizes = map[string]struct{}{
	"w185": {}, "w300": {}, "w342": {}, "w500": {}, "w1280": {},
}
var allowedTypes = map[string]struct{}{
	"image/jpeg": {}, "image/png": {}, "image/webp": {},
}

type metadata struct {
	ContentType          string `json:"contentType"`
	ETag                 string `json:"etag"`
	UpstreamETag         string `json:"upstreamEtag,omitempty"`
	UpstreamLastModified string `json:"upstreamLastModified,omitempty"`
}

type Asset struct {
	File        *os.File
	Size        int64
	ModTime     time.Time
	ContentType string
	ETag        string
}

type flight struct {
	done chan struct{}
	err  error
}
type negativeEntry struct {
	err     error
	expires time.Time
}
type refreshRequest struct{ size, name string }

type Cache struct {
	embyRequest EmbyRequest
	mikan       bool
	gying       bool
	woniu       bool
	root        string
	maxBytes    int64
	baseURL     string
	http        *http.Client
	now         func() time.Time
	mu          sync.Mutex
	flights     map[string]*flight
	negative    map[string]negativeEntry
	pending     map[string]refreshRequest
	refresh     chan struct{}
	prune       chan struct{}
	downloads   chan struct{}
}

func New(root string, maxBytes int64) (*Cache, error) {
	if strings.TrimSpace(root) == "" || maxBytes < 1 {
		return nil, ErrInput
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析图片缓存目录: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("创建图片缓存目录: %w", err)
	}
	return &Cache{
		root: abs, maxBytes: maxBytes, baseURL: "https://image.tmdb.org",
		http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:  time.Now, flights: make(map[string]*flight), negative: make(map[string]negativeEntry),
		pending: make(map[string]refreshRequest), refresh: make(chan struct{}, 1), prune: make(chan struct{}, 1), downloads: make(chan struct{}, 4),
	}, nil
}

func BrowserMaxAge() time.Duration { return browserMaxAge }

func validName(name string) bool {
	if len(name) < 5 || len(name) > 160 || name != filepath.Base(name) || strings.Contains(name, "..") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return false
	}
	for _, r := range strings.TrimSuffix(name, filepath.Ext(name)) {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func (c *Cache) paths(size, name string) (string, string, error) {
	_, valid := allowedSizes[size]
	if c.mikan {
		valid = validMikanMonth(size)
	}
	if c.gying {
		valid = validGyingImage(size, name)
	}
	if c.woniu {
		valid = woniuImageDirectory.MatchString(size) && woniuImageName.MatchString(name)
	}
	if c.embyRequest != nil {
		valid = validEmbyImage(size, name)
	}
	if !valid || !validName(name) {
		return "", "", ErrInput
	}
	directory := filepath.Join(c.root, size)
	return filepath.Join(directory, name), filepath.Join(directory, name+".json"), nil
}

func readMetadata(path string) metadata {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4096 {
		return metadata{}
	}
	var value metadata
	if json.Unmarshal(data, &value) != nil {
		return metadata{}
	}
	return value
}

func (c *Cache) open(path, metadataPath string) (*Asset, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开图片缓存: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("读取图片缓存信息: %w", err)
	}
	meta := readMetadata(metadataPath)
	contentType := meta.ContentType
	if _, ok := allowedTypes[contentType]; !ok {
		contentType = mime.TypeByExtension(filepath.Ext(path))
		if semicolon := strings.IndexByte(contentType, ';'); semicolon >= 0 {
			contentType = contentType[:semicolon]
		}
	}
	return &Asset{File: file, Size: info.Size(), ModTime: info.ModTime(), ContentType: contentType, ETag: meta.ETag}, nil
}

func (c *Cache) Open(ctx context.Context, size, name string) (*Asset, error) {
	path, metadataPath, err := c.paths(size, name)
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(path)
	switch {
	case statErr == nil && info.Mode().IsRegular():
		if c.now().Sub(info.ModTime()) >= freshTTL {
			c.enqueue(refreshRequest{size: size, name: name})
		}
	case errors.Is(statErr, os.ErrNotExist):
		if err := c.fetch(ctx, size, name, path, metadataPath); err != nil {
			return nil, err
		}
	case statErr != nil:
		return nil, fmt.Errorf("检查图片缓存: %w", statErr)
	default:
		return nil, fmt.Errorf("图片缓存路径不是文件")
	}
	return c.open(path, metadataPath)
}

func (c *Cache) cachedFailure(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.negative[key]
	if !ok {
		return nil
	}
	if c.now().Before(entry.expires) {
		return entry.err
	}
	delete(c.negative, key)
	return nil
}

func (c *Cache) rememberFailure(key string, err error) {
	ttl := time.Minute
	if errors.Is(err, ErrNotFound) {
		ttl = time.Hour
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.negative) >= 512 {
		for existing, entry := range c.negative {
			if !c.now().Before(entry.expires) {
				delete(c.negative, existing)
			}
		}
		if len(c.negative) >= 512 {
			for existing := range c.negative {
				delete(c.negative, existing)
				break
			}
		}
	}
	c.negative[key] = negativeEntry{err: err, expires: c.now().Add(ttl)}
}

func (c *Cache) fetch(ctx context.Context, size, name, path, metadataPath string) (err error) {
	key := size + "/" + name
	if failure := c.cachedFailure(key); failure != nil {
		return failure
	}
	c.mu.Lock()
	if current, ok := c.flights[key]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-current.done:
			return current.err
		}
	}
	if len(c.flights) >= maxPending {
		c.mu.Unlock()
		return fmt.Errorf("图片请求繁忙，请稍后重试")
	}
	current := &flight{done: make(chan struct{})}
	c.flights[key] = current
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		current.err = err
		delete(c.flights, key)
		close(current.done)
		c.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.downloads <- struct{}{}:
	}
	defer func() { <-c.downloads }()
	// Another caller may have completed between the initial disk check and joining
	// this flight. Recheck after acquiring capacity to avoid duplicate downloads.
	if failure := c.cachedFailure(key); failure != nil {
		return failure
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() && c.now().Sub(info.ModTime()) < freshTTL {
		return nil
	}
	if err = c.download(ctx, size, name, path, metadataPath); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			c.rememberFailure(key, err)
		}
	}
	return err
}

func (c *Cache) download(ctx context.Context, size, name, path, metadataPath string) error {
	upstream, err := url.JoinPath(c.baseURL, "t", "p", size, name)
	if c.mikan {
		upstream, err = url.JoinPath(c.baseURL, "images", "Bangumi", size, name)
		upstream += "?width=240&height=336&format=webp"
	}
	if c.gying {
		upstream, err = url.JoinPath(c.baseURL, "img", size, strings.TrimSuffix(name, ".webp"), "256.webp")
	}
	if c.woniu {
		if size == "vod" {
			upstream, err = url.JoinPath(c.baseURL, "upload", "vod", name)
		} else {
			upstream, err = url.JoinPath(c.baseURL, "upload", "vod", size, name)
		}
	}
	if err != nil {
		return fmt.Errorf("创建图片地址: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if c.embyRequest != nil {
		req, err = c.embyRequest(ctx, size, strings.TrimSuffix(name, ".webp"))
	}
	if err != nil {
		return fmt.Errorf("创建图片请求: %w", err)
	}
	if c.mikan {
		req.Header.Set("User-Agent", "Cinema115-MikanImage/1.0")
		req.Header.Set("Accept", "image/webp,image/png,image/jpeg")
	}
	if c.gying {
		req.Header.Set("User-Agent", "Cinema115-GyingImage/1.0")
		req.Header.Set("Accept", "image/webp,image/png,image/jpeg")
	}
	meta := readMetadata(metadataPath)
	if meta.UpstreamETag != "" {
		req.Header.Set("If-None-Match", meta.UpstreamETag)
	}
	if meta.UpstreamLastModified != "" {
		req.Header.Set("If-Modified-Since", meta.UpstreamLastModified)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("下载图片: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("验证图片缓存: %w", err)
		}
		if err := os.Chtimes(path, c.now(), c.now()); err != nil {
			return fmt.Errorf("更新图片缓存时间: %w", err)
		}
		return nil
	}
	if response.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("图片上游返回 HTTP %d", response.StatusCode)
	}
	contentType := response.Header.Get("Content-Type")
	if parsed, _, parseErr := mime.ParseMediaType(contentType); parseErr == nil {
		contentType = parsed
	}
	if _, ok := allowedTypes[contentType]; !ok {
		return fmt.Errorf("图片类型不受支持")
	}
	if response.ContentLength > maxImageBytes {
		return fmt.Errorf("图片超过大小限制")
	}
	// 上游验证页面可能伪装成图片响应；检查文件签名，禁止将 HTML 缓存为封面。
	var source io.Reader = response.Body
	if c.mikan || c.gying || c.woniu || c.embyRequest != nil {
		reader := bufio.NewReader(response.Body)
		header, peekErr := reader.Peek(512)
		if peekErr != nil && !errors.Is(peekErr, io.EOF) {
			return fmt.Errorf("读取图片文件头: %w", peekErr)
		}
		contentType = http.DetectContentType(header)
		if _, ok := allowedTypes[contentType]; !ok {
			return fmt.Errorf("图片内容类型不受支持")
		}
		source = reader
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建图片尺寸目录: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmdb-image-*")
	if err != nil {
		return fmt.Errorf("创建图片临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(source, maxImageBytes+1))
	if copyErr != nil {
		_ = temporary.Close()
		return fmt.Errorf("保存图片: %w", copyErr)
	}
	if written > maxImageBytes {
		_ = temporary.Close()
		return fmt.Errorf("图片超过大小限制")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步图片缓存: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭图片缓存: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("替换图片缓存: %w", err)
	}
	if err := os.Chtimes(path, c.now(), c.now()); err != nil {
		return fmt.Errorf("设置图片缓存时间: %w", err)
	}
	meta = metadata{
		ContentType: contentType, ETag: `"` + hex.EncodeToString(hash.Sum(nil)) + `"`,
		UpstreamETag: response.Header.Get("ETag"), UpstreamLastModified: response.Header.Get("Last-Modified"),
	}
	if err := writeMetadata(metadataPath, meta); err != nil {
		return err
	}
	// 小容量缓存写入后通知受控工作协程清理，避免只等定时清理导致持续膨胀。
	if c.mikan || c.gying || c.woniu || c.embyRequest != nil {
		select {
		case c.prune <- struct{}{}:
		default:
		}
	}
	return nil
}

func replaceFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	backup := target + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func writeMetadata(path string, value metadata) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码图片元数据: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmdb-metadata-*")
	if err != nil {
		return fmt.Errorf("创建图片元数据: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("保存图片元数据: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭图片元数据: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("替换图片元数据: %w", err)
	}
	return nil
}

func (c *Cache) enqueue(request refreshRequest) {
	key := request.size + "/" + request.name
	c.mu.Lock()
	if len(c.pending) < maxPending {
		c.pending[key] = request
	}
	c.mu.Unlock()
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

func (c *Cache) popRefresh() (refreshRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, request := range c.pending {
		delete(c.pending, key)
		return request, true
	}
	return refreshRequest{}, false
}

func (c *Cache) hasPending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending) > 0
}

// Run 负责过期图片的后台验证和缓存清理，调用方必须纳入进程退出等待。
func (c *Cache) Run(ctx context.Context) {
	_ = c.cleanup()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.cleanup()
		case <-c.prune:
			_ = c.cleanup()
		case <-c.refresh:
			request, ok := c.popRefresh()
			if !ok {
				continue
			}
			path, metadataPath, err := c.paths(request.size, request.name)
			if err == nil {
				requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				_ = c.fetch(requestCtx, request.size, request.name, path, metadataPath)
				cancel()
			}
			if c.hasPending() {
				select {
				case c.refresh <- struct{}{}:
				default:
				}
			}
		}
	}
}

type cachedFile struct {
	path    string
	size    int64
	modTime time.Time
}

func (c *Cache) cleanup() error {
	now := c.now()
	files := make([]cachedFile, 0, 256)
	var total int64
	err := filepath.WalkDir(c.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".tmdb-") || strings.HasSuffix(entry.Name(), ".old") {
			if info, err := entry.Info(); err == nil && now.Sub(info.ModTime()) >= time.Hour {
				_ = os.Remove(path)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) >= retentionTTL {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			_ = os.Remove(path + ".json")
			return nil
		}
		files = append(files, cachedFile{path: path, size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return fmt.Errorf("扫描图片缓存: %w", err)
	}
	if total <= c.maxBytes {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files {
		if total <= c.maxBytes {
			break
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("清理图片缓存: %w", err)
		}
		_ = os.Remove(file.path + ".json")
		total -= file.size
	}
	return nil
}
