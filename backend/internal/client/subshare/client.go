// Package subshare reads foxofice/sub_share's catalogue and immutable Git blobs.
package subshare

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/singleflight"
	"strm115/backend/internal/utils/subtitle"
)

type Version struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	EnglishTitle  string `json:"englishTitle"`
	Year          string `json:"year"`
	Source        string `json:"source"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	URL           string `json:"url"`
	directory     string
	search        string
}
type File struct {
	Revision string `json:"revision"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
}
type listing struct {
	Files []File
	At    time.Time
}
type Client struct {
	HTTP               *http.Client
	API, Raw, CacheDir string
	mu                 sync.Mutex
	versions           []Version
	at                 time.Time
	lists              map[string]listing
	flight             singleflight.Group
	revision           string
	revisionAt         time.Time
}

func New(cacheDir string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 25 * time.Second}, API: "https://api.github.com/repos/foxofice/sub_share", Raw: "https://raw.githubusercontent.com/foxofice/sub_share/master", CacheDir: cacheDir, lists: make(map[string]listing)}
}

func (c *Client) get(ctx context.Context, target string, limit int64, raw bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Cinema115 (https://github.com/jli452195/Cinema115)")
	req.Header.Set("Accept", "application/vnd.github+json")
	if raw {
		req.Header.Set("Accept", "application/vnd.github.raw+json")
	}
	h := *c.HTTP
	h.Jar = nil
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("读取字幕库: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("字幕库请求失败（HTTP %d），请稍后重试", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取字幕库响应: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("字幕库响应超过大小限制")
	}
	return body, nil
}

func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
func encodePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
func validPath(p string) bool {
	return strings.HasPrefix(p, "subs_list/animation/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n")
}
func parseIndex(body []byte) ([]Version, error) {
	var doc struct {
		Rows []struct {
			Time        string `xml:"time"`
			CHS         string `xml:"name_chs"`
			CHT         string `xml:"name_cht"`
			JP          string `xml:"name_jp"`
			EN          string `xml:"name_en"`
			Rome        string `xml:"name_rome"`
			Type        string `xml:"type"`
			Source      string `xml:"source"`
			Name        string `xml:"sub_name"`
			Description string `xml:"desc"`
			Path        string `xml:"path"`
		} `xml:"subs"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("解析字幕索引: %w", err)
	}
	versions := make([]Version, 0, len(doc.Rows))
	seen := make(map[string]bool, len(doc.Rows))
	for _, r := range doc.Rows {
		if r.Type != "animation" {
			continue
		}
		dir := "subs_list/" + strings.ReplaceAll(r.Path, "\\", "/") + "/" + r.Source + "/" + r.Name
		if !validPath(dir) || r.Name == "" || strings.Contains(r.Source, "/") || strings.Contains(r.Name, "/") || len(r.Time) < 4 {
			continue
		}
		h := sha256.Sum256([]byte(dir))
		id := hex.EncodeToString(h[:16])
		if seen[id] {
			continue
		}
		seen[id] = true
		versions = append(versions, Version{ID: id, Title: r.CHS, OriginalTitle: r.JP, EnglishTitle: r.EN, Year: r.Time[:4], Source: r.Source, Name: r.Name, Description: r.Description, URL: "https://github.com/foxofice/sub_share/tree/master/" + encodePath(dir), directory: dir, search: normalize(r.CHS + " " + r.CHT + " " + r.JP + " " + r.EN + " " + r.Rome)})
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("字幕索引没有有效条目")
	}
	return versions, nil
}
func (c *Client) index(ctx context.Context) ([]Version, error) {
	c.mu.Lock()
	cached, at := c.versions, c.at
	c.mu.Unlock()
	if len(cached) > 0 && time.Since(at) < 7*24*time.Hour {
		return cached, nil
	}
	result := c.flight.DoChan("index", func() (any, error) {
		filename := filepath.Join(c.CacheDir, "index.xml")
		var body []byte
		if c.CacheDir != "" {
			if info, err := os.Stat(filename); err == nil && info.Size() <= 8<<20 && time.Since(info.ModTime()) < 7*24*time.Hour {
				body, _ = os.ReadFile(filename)
			}
		}
		rows, err := parseIndex(body)
		if err != nil {
			body, err = c.get(ctx, c.Raw+"/Subtitles%20DataBase/Files/db.xml", 8<<20, false)
			if err != nil {
				if len(cached) > 0 {
					return cached, nil
				}
				return nil, err
			}
			rows, err = parseIndex(body)
			if err != nil {
				return nil, err
			}
			if c.CacheDir != "" {
				if err = os.MkdirAll(c.CacheDir, 0700); err != nil {
					return nil, err
				}
				tmp, e := os.CreateTemp(c.CacheDir, "index-*.tmp")
				if e != nil {
					return nil, e
				}
				name := tmp.Name()
				defer os.Remove(name)
				_, e = tmp.Write(body)
				closeErr := tmp.Close()
				if e != nil {
					return nil, e
				}
				if closeErr != nil {
					return nil, closeErr
				}
				if e = os.Rename(name, filename); e != nil {
					return nil, e
				}
			}
		}
		c.mu.Lock()
		c.versions, c.at = rows, time.Now()
		c.mu.Unlock()
		return rows, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.([]Version), nil
	}
}
func (c *Client) Search(ctx context.Context, queries []string) ([]Version, error) {
	if len(queries) == 0 || len(queries) > 3 {
		return nil, fmt.Errorf("请输入字幕搜索词")
	}
	keys := make([]string, 0, len(queries))
	for _, q := range queries {
		if len([]rune(q)) > 128 {
			return nil, fmt.Errorf("搜索词过长")
		}
		if k := normalize(q); k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("请输入字幕搜索词")
	}
	rows, err := c.index(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, 32)
	for _, v := range rows {
		for _, q := range keys {
			if strings.Contains(v.search, q) {
				out = append(out, v)
				break
			}
		}
		if len(out) >= 200 {
			break
		}
	}
	return out, nil
}
func (c *Client) version(ctx context.Context, id string) (Version, error) {
	rows, err := c.index(ctx)
	if err != nil {
		return Version{}, err
	}
	for _, v := range rows {
		if v.ID == id {
			return v, nil
		}
	}
	return Version{}, fmt.Errorf("字幕版本不存在，请重新搜索")
}
func (c *Client) Files(ctx context.Context, id string) ([]File, error) {
	c.mu.Lock()
	cached, ok := c.lists[id]
	c.mu.Unlock()
	if ok && time.Since(cached.At) < 24*time.Hour {
		return append([]File(nil), cached.Files...), nil
	}
	ch := c.flight.DoChan("files:"+id, func() (any, error) {
		v, err := c.version(ctx, id)
		if err != nil {
			return nil, err
		}
		revision, err := c.currentRevision(ctx)
		if err != nil {
			return nil, err
		}
		body, err := c.get(ctx, c.API+"/contents/"+encodePath(v.directory)+"?ref="+revision, 4<<20, false)
		if err != nil {
			return nil, err
		}
		var entries []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		}
		if err = json.Unmarshal(body, &entries); err != nil {
			return nil, fmt.Errorf("字幕目录响应无效: %w", err)
		}
		if len(entries) >= 1000 {
			return nil, fmt.Errorf("字幕目录过大，未返回完整列表")
		}
		files := make([]File, 0, len(entries))
		add := func(p, sha string, size int64) {
			if validPath(p) && strings.HasPrefix(p, v.directory+"/") && subtitle.Supported(p) && size > 0 && size <= 16<<20 && validSHA(sha) {
				files = append(files, File{Revision: revision, Path: p, Name: path.Base(p), SHA: sha, Size: size})
			}
		}
		directories := 0
		for _, e := range entries {
			if e.Type == "dir" {
				directories++
				if directories > 8 || !validSHA(e.SHA) || path.Dir(e.Path) != v.directory {
					return nil, fmt.Errorf("字幕子目录过多或无效，请手动导入")
				}
				body, err := c.get(ctx, c.API+"/git/trees/"+e.SHA+"?recursive=1", 4<<20, false)
				if err != nil {
					return nil, err
				}
				var tree struct {
					Truncated bool `json:"truncated"`
					Tree      []struct {
						Path string `json:"path"`
						Type string `json:"type"`
						Mode string `json:"mode"`
						SHA  string `json:"sha"`
						Size int64  `json:"size"`
					} `json:"tree"`
				}
				if json.Unmarshal(body, &tree) != nil || tree.Truncated || len(tree.Tree) > 2000 {
					return nil, fmt.Errorf("字幕子目录未返回完整列表")
				}
				for _, f := range tree.Tree {
					if f.Type == "blob" && (f.Mode == "100644" || f.Mode == "100755") {
						add(e.Path+"/"+f.Path, f.SHA, f.Size)
					}
				}
				continue
			}
			if e.Type != "file" || !subtitle.Supported(e.Name) {
				continue
			}
			if e.Size <= 0 || e.Size > 16<<20 || path.Base(e.Path) != e.Name || path.Dir(e.Path) != v.directory || len(e.SHA) != 40 {
				continue
			}
			add(e.Path, e.SHA, e.Size)
		}
		if len(files) > 2000 {
			return nil, fmt.Errorf("字幕文件过多，请分版本选择")
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("该版本没有支持的字幕文件（支持 ASS / SSA / SRT / VTT / IDX / SUB）")
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		c.mu.Lock()
		if len(c.lists) >= 64 {
			for key := range c.lists {
				delete(c.lists, key)
				break
			}
		}
		c.lists[id] = listing{files, time.Now()}
		c.mu.Unlock()
		return files, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		return append([]File(nil), r.Val.([]File)...), nil
	}
}
func (c *Client) Read(ctx context.Context, f File) ([]byte, error) {
	digest, err := hex.DecodeString(f.SHA)
	if err != nil || len(digest) != sha1.Size || !validSHA(f.Revision) || f.Size <= 0 || f.Size > 16<<20 || !validPath(f.Path) || !subtitle.Supported(f.Name) {
		return nil, fmt.Errorf("字幕来源无效")
	}
	body, err := c.get(ctx, strings.TrimSuffix(c.Raw, "/master")+"/"+f.Revision+"/"+encodePath(f.Path), f.Size, true)
	if err != nil {
		return nil, err
	}
	h := sha1.New()
	_, _ = io.WriteString(h, "blob "+strconv.Itoa(len(body))+"\x00")
	_, _ = h.Write(body)
	if int64(len(body)) != f.Size || !bytes.Equal(h.Sum(nil), digest) {
		return nil, fmt.Errorf("字幕内容与已确认版本不一致")
	}
	return body, nil
}

func validSHA(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha1.Size
}
func (c *Client) currentRevision(ctx context.Context) (string, error) {
	c.mu.Lock()
	revision, at := c.revision, c.revisionAt
	c.mu.Unlock()
	if revision != "" && time.Since(at) < 24*time.Hour {
		return revision, nil
	}
	ch := c.flight.DoChan("revision", func() (any, error) {
		body, err := c.get(ctx, c.API+"/git/ref/heads/master", 16<<10, false)
		if err != nil {
			return nil, err
		}
		var ref struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if json.Unmarshal(body, &ref) != nil || !validSHA(ref.Object.SHA) {
			return nil, fmt.Errorf("字幕仓库版本无效")
		}
		c.mu.Lock()
		c.revision, c.revisionAt = ref.Object.SHA, time.Now()
		c.mu.Unlock()
		return ref.Object.SHA, nil
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return "", r.Err
		}
		return r.Val.(string), nil
	}
}
