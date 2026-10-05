package httpapi

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"strm115/backend/internal/strm"
)

type ShareAPI struct {
	Runs       *service.ShareRuns
	Workflow   service.MediaGenerator
	OutputRoot string
	Client     *p115.ShareClient
	Cookie     func() string
	BaseURL    func() string
	MediaHTTP  *http.Client
}

func NewShareAPI(cookie func() string) *ShareAPI {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 25 * time.Second
	return &ShareAPI{OutputRoot: "../data/media", Client: p115.NewShareClient(), Cookie: cookie, MediaHTTP: &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !p115.AllowedMediaURL(req.URL.String()) {
			return fmt.Errorf("下载跳转地址无效")
		}
		return nil
	}}}
}

func (a *ShareAPI) Register(r *gin.Engine, admin *gin.RouterGroup) {
	a.registerShareRuns(admin)
	admin.POST("/p115/share/files", a.files)
	admin.POST("/p115/share/strm", a.strm)
	admin.GET("/p115/share/generation-config", a.generationConfig)
	admin.GET("/p115/share/output-directories", a.outputDirectories)
	admin.POST("/p115/share/generate", a.generate)
	admin.POST("/p115/share/generate-stream", a.generateStream)
	r.GET("/api/share115/:code/:sha1/:password/:fileID", a.streamShare)
	r.HEAD("/api/share115/:code/:sha1/:password/:fileID", a.streamShare)
	r.GET("/api/", a.streamQuery)
	r.HEAD("/api/", a.streamQuery)
}

type shareInput struct {
	SHA1     string `json:"sha1"`
	URL      string `json:"url" binding:"required"`
	Password string `json:"password"`
	CID      string `json:"cid"`
	Offset   int    `json:"offset"`
	FileID   string `json:"fileId"`
	Name     string `json:"name"`
	BaseURL  string `json:"baseUrl"`
}

func (a *ShareAPI) files(c *gin.Context) {
	var input shareInput
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(400, gin.H{"error": "请输入分享链接"})
		return
	}
	share, err := p115.ParseShareURL(input.URL)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if input.Password != "" {
		share.Password = input.Password
	}
	cookie := a.Cookie()
	if cookie == "" {
		c.JSON(400, gin.H{"error": "请在分享基础设置中选择已登录的 115 账号配置"})
		return
	}
	page, err := a.Client.List(c.Request.Context(), share, input.CID, input.Offset, cookie)
	if err != nil {
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, page)
}

// 分享播放参数；不包含身份凭证或有效期。
// PickCode STRM fields are untrusted hints; only 115 responses or verified
// local material may supply metadata for instant upload and persistence.
type sharePlaybackParams struct {
	Code     string `json:"code"`
	Password string `json:"password"`
	FileID   string `json:"fileId"`
	Name     string `json:"name"`
	PickCode string `json:"pickCode,omitempty"`
	SHA1     string `json:"sha1,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

func (a *ShareAPI) strm(c *gin.Context) {
	if a.Cookie() == "" {
		c.JSON(400, gin.H{"error": "请在分享基础设置中选择已登录的 115 账号配置"})
		return
	}
	var input shareInput
	if c.ShouldBindJSON(&input) != nil || input.FileID == "" || input.Name == "" {
		c.JSON(400, gin.H{"error": "请选择分享中的文件"})
		return
	}
	share, err := p115.ParseShareURL(input.URL)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if input.Password != "" {
		share.Password = input.Password
	}
	name := path.Base(strings.ReplaceAll(input.Name, "\\", "/"))
	claim := sharePlaybackParams{Code: share.Code, Password: share.Password, FileID: input.FileID, Name: name, SHA1: input.SHA1}
	relative, err := strm.SharePath(strm.Share{Code: claim.Code, SHA1: claim.SHA1, Password: claim.Password, FileID: claim.FileID})
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	base := a.generationBaseURL(c)
	if input.BaseURL != "" {
		base, err = service.ValidateBaseURL(input.BaseURL)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	public, err := url.Parse(base)
	if err != nil || public.Host == "" || (public.Scheme != "http" && public.Scheme != "https") {
		c.JSON(500, gin.H{"error": "STRM 播放地址无效，请在生成页面重新保存"})
		return
	}
	c.JSON(200, gin.H{"name": strings.TrimSuffix(name, path.Ext(name)) + ".strm", "content": base + relative + "\n", "playUrl": relative})
}

// streamShare uses the same strict path parser as Emby and local STRM playback.
func (a *ShareAPI) streamShare(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	u := c.Request.URL
	if u.RawPath != "" || u.RawQuery != "" || u.ForceQuery {
		c.JSON(400, gin.H{"error": "STRM 分享地址不允许编码分隔符或查询参数"})
		return
	}
	value, err := strm.ParseSharePath(u.Path)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	a.play(c, &sharePlaybackParams{Code: value.Code, SHA1: value.SHA1, Password: value.Password, FileID: value.FileID, Name: value.FileID})
}

// streamQuery preserves existing STRM URLs without generating new legacy URLs.
func (a *ShareAPI) streamQuery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.Request.URL.RawPath != "" || len(c.Request.URL.RawQuery) > 16384 {
		c.JSON(400, gin.H{"error": "STRM 分享地址无效"})
		return
	}
	value, err := strm.ParseShareQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	a.play(c, &sharePlaybackParams{Code: value.Code, Password: value.Password, FileID: value.FileID, Name: value.FileID})
}

func (a *ShareAPI) play(c *gin.Context, claim *sharePlaybackParams) {
	cookie := a.Cookie()
	if cookie == "" {
		c.JSON(503, gin.H{"error": "分享使用的 115 账号未选择、已删除或未登录，请管理员检查分享基础设置"})
		return
	}
	link, err := a.Client.DownloadURL(c.Request.Context(), &p115.ShareInfo{Code: claim.Code, Password: claim.Password}, claim.FileID, cookie)
	if err != nil {
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	if err = a.proxy(c, link, claim.Name); err != nil && !c.Writer.Written() {
		c.JSON(502, gin.H{"error": "读取 115 媒体失败"})
	}
}

func (a *ShareAPI) proxy(c *gin.Context, link, name string) error {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, c.Request.Method, link, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", p115.PlaybackUserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	for _, h := range []string{"Range", "If-Range"} {
		if v := c.GetHeader(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := a.MediaHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 && resp.StatusCode != 416 {
		return fmt.Errorf("媒体返回状态 %d", resp.StatusCode)
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			c.Header(h, v)
		}
	}
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	c.Status(resp.StatusCode)
	if c.Request.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(c.Writer, resp.Body)
	return err
}
