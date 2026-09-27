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
	r.GET("/api/", a.streamQuery)
	r.HEAD("/api/", a.streamQuery)
}

type shareInput struct {
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
type sharePlaybackParams struct {
	Code     string `json:"code"`
	Password string `json:"password"`
	FileID   string `json:"fileId"`
	Name     string `json:"name"`
	PickCode string `json:"pickCode,omitempty"`
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
	claim := sharePlaybackParams{Code: share.Code, Password: share.Password, FileID: input.FileID, Name: name}
	relative := a.queryURL(claim)
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

// 播放入口使用分享码、文件 ID 和提取码。
func (a *ShareAPI) queryURL(claim sharePlaybackParams) string {
	return "/api/?share_code=" + url.QueryEscape(claim.Code) + "&id=" + url.QueryEscape(claim.FileID) + "&receive_code=" + url.QueryEscape(claim.Password)
}

func (a *ShareAPI) parseQuery(q url.Values) (*sharePlaybackParams, error) {
	for _, key := range []string{"share_code", "id", "receive_code"} {
		if len(q[key]) == 0 {
			return nil, fmt.Errorf("缺少必要参数 share_code、receive_code 或 id")
		}
		if len(q[key]) > 1 {
			return nil, fmt.Errorf("必要参数 share_code、receive_code 或 id 重复")
		}
		if len(q.Get(key)) > 1024 {
			return nil, fmt.Errorf("分享播放参数过长")
		}
	}
	if q.Get("id") == "" || q.Get("share_code") == "" {
		return nil, fmt.Errorf("分享码和文件 ID 不能为空")
	}
	return &sharePlaybackParams{Code: q.Get("share_code"), Password: q.Get("receive_code"), FileID: q.Get("id"), Name: q.Get("id")}, nil
}

func (a *ShareAPI) streamQuery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	q, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分享参数编码无效"})
		return
	}
	claim, err := a.parseQuery(q)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.play(c, claim)
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
