package emby

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

type Client struct {
	BaseURL, Key string
	HTTP         *http.Client
}
type Server struct {
	ID      string `json:"Id"`
	Name    string `json:"ServerName"`
	Version string `json:"Version"`
}
type Library struct {
	ID   string `json:"ItemId"`
	GUID string `json:"Guid"`
	Name string `json:"Name"`
	Type string `json:"CollectionType"`
}
type Stream struct {
	Type                            string
	Codec                           string
	Width                           int
	Height                          int
	VideoRange                      string
	DisplayTitle                    string
	Title                           string
	Language                        string
	DisplayLanguage                 string
	Profile                         string
	Level                           *float64
	AspectRatio                     string
	IsInterlaced                    *bool
	AverageFrameRate                *float64
	RealFrameRate                   *float64
	BitRate                         *int64
	BitDepth                        *int
	RefFrames                       *int
	PixelFormat                     string
	ColorPrimaries                  string
	ColorSpace                      string
	ColorTransfer                   string
	ExtendedVideoType               string
	ExtendedVideoSubTypeDescription string
	ChannelLayout                   string
	Channels                        *int
	SampleRate                      *int
	IsDefault                       *bool
	Index                           int
}
type Source struct {
	ID           string `json:"Id"`
	Name         string
	Container    string
	Size         int64
	MediaStreams []Stream
}
type Item struct {
	ID                                             string `json:"Id"`
	Name, Type, SeriesID, ParentID, LocationType   string
	DateCreated                                    string
	ProviderIDs                                    map[string]string `json:"ProviderIds"`
	IndexNumber, ParentIndexNumber, IndexNumberEnd *int
	IsVirtualItem                                  bool
	MediaSources                                   []Source
}
type Page struct {
	Items            []Item
	TotalRecordCount int
}

func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("Emby 地址必须是无账号、查询参数的 HTTP 或 HTTPS 地址")
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/emby")
	return strings.TrimRight(u.String(), "/"), nil
}
func New(base, key string) *Client {
	return &Client{BaseURL: base, Key: key, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

type User struct {
	ID              string `json:"Id"`
	Name            string `json:"Name"`
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
	Policy          struct {
		IsDisabled      bool `json:"IsDisabled"`
		IsAdministrator bool `json:"IsAdministrator"`
	} `json:"Policy"`
}

func (c *Client) UserImage(ctx context.Context, id string) ([]byte, string, error) {
	if len(id) != 32 || strings.Trim(id, "0123456789abcdefABCDEF") != "" {
		return nil, "", fmt.Errorf("Emby 用户 ID 无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/emby/Users/"+id+"/Images/Primary?maxWidth=128&maxHeight=128", nil)
	if err != nil {
		return nil, "", fmt.Errorf("创建头像请求: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("无法读取 Emby 头像")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Emby 头像返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, "", fmt.Errorf("读取头像: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, "", fmt.Errorf("头像超过大小限制")
	}
	kind := http.DetectContentType(data)
	switch kind {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return data, kind, nil
	default:
		return nil, "", fmt.Errorf("头像格式不支持")
	}
}

func (c *Client) Users(ctx context.Context) ([]User, error) {
	var users []User
	if err := c.get(ctx, "/Users", nil, &users); err != nil {
		return nil, err
	}
	if users == nil {
		return nil, fmt.Errorf("Emby 用户列表响应无效")
	}
	return users, nil
}
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/emby"+path+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("创建 Emby 请求: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.Key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Emby 请求取消: %w", ctx.Err())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("Emby 请求超时: %w", context.DeadlineExceeded)
		}
		return fmt.Errorf("无法连接 Emby，请检查地址和网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Emby 返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil {
		return fmt.Errorf("读取 Emby 响应: %w", err)
	}
	if len(data) > 8*1024*1024 {
		return fmt.Errorf("Emby 响应超过大小限制")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("Emby 返回的数据格式无效")
	}
	return nil
}
func (c *Client) Info(ctx context.Context) (Server, error) {
	var v Server
	err := c.get(ctx, "/System/Info", nil, &v)
	if err == nil && v.ID == "" {
		err = fmt.Errorf("Emby 未返回服务器 ID")
	}
	return v, err
}
func (c *Client) Libraries(ctx context.Context) ([]Library, error) {
	var v []Library
	err := c.get(ctx, "/Library/VirtualFolders", nil, &v)
	return v, err
}
func (c *Client) Items(ctx context.Context, library string, start int) (Page, error) {
	var v Page
	// 大型剧集库同名分集很多；按唯一 ID 排序，避免名称排序导致跨页重复。
	err := c.get(ctx, "/Items", url.Values{"ParentId": {library}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Series,Episode"}, "Fields": {"ProviderIds,ParentId,DateCreated"}, "StartIndex": {strconv.Itoa(start)}, "Limit": {"200"}, "SortBy": {"Id"}, "SortOrder": {"Ascending"}, "EnableImages": {"false"}, "EnableUserData": {"false"}}, &v)
	return v, err
}
func (c *Client) Item(ctx context.Context, id string, versions bool) (*Item, error) {
	fields := "ProviderIds,ParentId,DateCreated"
	if versions {
		fields += ",MediaSources,MediaStreams"
	}
	var v Page
	err := c.get(ctx, "/Items", url.Values{"Ids": {id}, "Fields": {fields}, "EnableImages": {"false"}, "EnableUserData": {"false"}}, &v)
	if err != nil {
		return nil, err
	}
	if len(v.Items) == 0 {
		return nil, nil
	}
	if v.Items[0].ID != id {
		return nil, fmt.Errorf("Emby 返回的条目 ID 不匹配")
	}
	return &v.Items[0], nil
}
func (c *Client) Ancestors(ctx context.Context, id string) ([]Item, error) {
	var v []Item
	err := c.get(ctx, "/Items/"+url.PathEscape(id)+"/Ancestors", nil, &v)
	return v, err
}

// 虚拟媒体库可聚合多个物理目录，父目录链不一定包含媒体库 ID。
func (c *Client) InLibrary(ctx context.Context, libraryID, itemID string) (bool, error) {
	var page Page
	err := c.get(ctx, "/Items", url.Values{"ParentId": {libraryID}, "Recursive": {"true"}, "Ids": {itemID}, "Limit": {"1"}, "Fields": {"ParentId"}, "EnableImages": {"false"}, "EnableUserData": {"false"}}, &page)
	if err != nil {
		return false, err
	}
	if len(page.Items) == 0 {
		return false, nil
	}
	if len(page.Items) != 1 || page.Items[0].ID != itemID {
		return false, fmt.Errorf("Emby 媒体库归属查询返回了不匹配的条目")
	}
	return true, nil
}
func (i Item) Record(library string) (model.EmbyItem, bool) {
	v := model.EmbyItem{ID: i.ID, LibraryID: library, Type: i.Type, SeriesID: i.SeriesID, ParentID: i.ParentID, Name: i.Name, DateCreated: normalizeDateCreated(i.DateCreated), Season: -1, Episode: -1, EndEpisode: -1}
	for key, value := range i.ProviderIDs {
		if strings.EqualFold(key, "tmdb") {
			if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
				v.TMDB = value
			}
		}
	}
	if i.ParentIndexNumber != nil {
		v.Season = *i.ParentIndexNumber
	}
	if i.IndexNumber != nil {
		v.Episode = *i.IndexNumber
	}
	if i.IndexNumberEnd != nil {
		v.EndEpisode = *i.IndexNumberEnd
	}
	return v, i.ID != "" && !i.IsVirtualItem && i.LocationType != "Virtual" && (i.Type == "Movie" || i.Type == "Series" || i.Type == "Episode")
}

func normalizeDateCreated(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse("2006-01-02T15:04:05.999999999", value)
		if err != nil {
			return ""
		}
		parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), time.UTC)
	}
	if parsed.Year() < 1970 {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}
