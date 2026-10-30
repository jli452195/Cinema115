// Package assrt implements the documented Assrt subtitle API.
package assrt

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/netproxy"
	"strm115/backend/internal/utils/subtitle"
)

var (
	ErrInput         = errors.New("射手网搜索词需为 3 至 128 个字符，或字幕参数无效")
	ErrToken         = errors.New("射手网 API Token 无效，请在字幕设置中重新配置")
	ErrNotConfigured = errors.New("尚未配置射手网 API Token")
	ErrRateLimited   = errors.New("射手网请求已达配额，请稍后重试")
	ErrUnavailable   = errors.New("射手网暂时无法访问，请稍后重试")
	ErrFile          = errors.New("射手字幕格式不受支持、超过大小限制或下载不完整，请重新选择或手动导入")
)

const PageSize = 15
const MaxDownloadBytes = 64 << 20

type Item struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	VideoName  string `json:"videoName,omitempty"`
	Language   string `json:"language,omitempty"`
	Group      string `json:"group,omitempty"`
	Format     string `json:"format,omitempty"`
	UploadedAt string `json:"uploadedAt,omitempty"`
}

type record struct {
	ID         int64  `json:"id"`
	Title      string `json:"native_name"`
	VideoName  string `json:"videoname"`
	Group      string `json:"release_site"`
	Format     string `json:"subtype"`
	UploadedAt string `json:"upload_time"`
	Language   struct {
		Description string `json:"desc"`
	} `json:"lang"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`

	// Search can mix documented records with unformatted upstream records.
	FileID      string `json:"fileid"`
	RawTitle    string `json:"m_title_bot"`
	RawGroup    string `json:"m_rl_name"`
	RawFormat   string `json:"m_subtype"`
	RawUploaded string `json:"uploadtime"`
	RawLanguage string `json:"m_lang"`
}

type response struct {
	Status *int `json:"status"`
	Sub    *struct {
		Items []record `json:"subs"`
	} `json:"sub"`
	User struct {
		Quota int `json:"quota"`
	} `json:"user"`
}

type Download struct {
	Name string
	Data []byte
}
type Client struct {
	HTTP  *http.Client
	token string
}

func New(token string) *Client {
	return &Client{HTTP: &http.Client{Transport: netproxy.Transport("assrt"), Timeout: 30 * time.Second}, token: token}
}

func ValidToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func (c *Client) request(ctx context.Context, endpoint string, args url.Values) (response, error) {
	var out response
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.assrt.net/v1/"+endpoint+"?"+args.Encode(), nil)
	if err != nil {
		return out, ErrInput
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "Cinema115/Subtitle")
	h := *c.HTTP
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := h.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return out, ErrToken
	}
	if resp.StatusCode == 429 {
		return out, ErrRateLimited
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return out, ErrUnavailable
	}
	// A 200 response may still carry an API error. Require status to be present.
	if json.Unmarshal(body, &out) != nil || out.Status == nil {
		return out, ErrUnavailable
	}
	switch *out.Status {
	case 0:
		if resp.StatusCode != http.StatusOK {
			return out, ErrUnavailable
		}
		return out, nil
	case 1, 20001:
		return out, ErrToken
	case 30900:
		return out, ErrRateLimited
	case 101, 20000, 20900:
		return out, ErrInput
	default:
		return out, ErrUnavailable
	}
}

func (c *Client) Quota(ctx context.Context) error {
	_, err := c.request(ctx, "user/quota", nil)
	return err
}

func (c *Client) Search(ctx context.Context, query string, position int) ([]Item, error) {
	if n := len([]rune(query)); n < 3 || n > 128 || position < 0 || position > 1500 || strings.ContainsAny(query, "\x00\r\n") {
		return nil, ErrInput
	}
	out, err := c.request(ctx, "sub/search", url.Values{"q": {query}, "pos": {strconv.Itoa(position)}, "cnt": {strconv.Itoa(PageSize)}})
	if err != nil {
		return nil, err
	}
	if out.Sub == nil || len(out.Sub.Items) > PageSize {
		return nil, ErrUnavailable
	}
	items := make([]Item, 0, len(out.Sub.Items))
	for _, item := range out.Sub.Items {
		if item.ID == 0 && item.FileID != "" {
			id, err := strconv.ParseInt(item.FileID, 10, 64)
			if err != nil {
				return nil, ErrUnavailable
			}
			item.ID = id
			item.Title = item.RawTitle
			item.Group = item.RawGroup
			item.Format = item.RawFormat
			item.UploadedAt = item.RawUploaded
			item.Language.Description = strings.Join(strings.Fields(html.UnescapeString(item.RawLanguage)), " ")
		}
		if item.ID <= 0 {
			return nil, ErrUnavailable
		}
		items = append(items, Item{ID: item.ID, Title: item.Title, VideoName: item.VideoName, Language: item.Language.Description, Group: item.Group, Format: item.Format, UploadedAt: item.UploadedAt})
	}
	return items, nil
}

func downloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return (host == "assrt.net" || strings.HasSuffix(host, ".assrt.net")) && (u.Port() == "" || u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80")
}

func (c *Client) Download(ctx context.Context, id int64) (Download, error) {
	if id <= 0 {
		return Download{}, ErrInput
	}
	out, err := c.request(ctx, "sub/detail", url.Values{"id": {strconv.FormatInt(id, 10)}})
	if err != nil {
		return Download{}, err
	}
	if out.Sub == nil || len(out.Sub.Items) != 1 || out.Sub.Items[0].ID != id {
		return Download{}, ErrUnavailable
	}
	item := out.Sub.Items[0]
	name := path.Base(strings.ReplaceAll(item.Filename, "\\", "/"))
	ext := strings.ToLower(path.Ext(name))
	if !subtitle.Supported(name) && ext != ".zip" && ext != ".rar" && ext != ".7z" {
		return Download{}, ErrFile
	}
	if item.Size < 1 || item.Size > MaxDownloadBytes {
		return Download{}, ErrFile
	}
	if len(name) > 2048 || strings.ContainsAny(name, "\x00\r\n") || !downloadURL(item.URL) {
		return Download{}, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return Download{}, ErrUnavailable
	}
	req.Header.Set("User-Agent", "Cinema115/Subtitle")
	h := *c.HTTP
	h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 || !downloadURL(req.URL.String()) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := h.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Download{}, ctx.Err()
		}
		return Download{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength > MaxDownloadBytes {
		return Download{}, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, item.Size+1))
	if err != nil || int64(len(body)) != item.Size {
		return Download{}, ErrFile
	}
	return Download{Name: name, Data: body}, nil
}
