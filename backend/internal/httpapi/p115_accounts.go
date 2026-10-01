package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

type accountQRClient interface {
	CreateQRCode(context.Context, string) (*p115.QRCode, error)
	CheckQRCode(context.Context, string) (*p115.QRCodeStatus, error)
	CompleteQRCodeLogin(context.Context, string) (string, error)
}
type accountQR struct {
	client   string
	cookie   string // Pending replacement credential; never serialized.
	uid      string
	token    string
	name     string
	timer    *time.Timer
	id       string
	revision uint64
	expires  time.Time
	busy     bool
}
type p115AccountsAPI struct {
	store    *service.P115Accounts
	checker  CookieChecker
	qr       accountQRClient
	drive    service.AccountDirectoryChecker
	mu       sync.Mutex
	sessions map[string]*accountQR
}

func RegisterP115Accounts(admin *gin.RouterGroup, store *service.P115Accounts, checker CookieChecker, qr accountQRClient, drive service.AccountDirectoryChecker) {
	a := &p115AccountsAPI{store: store, checker: checker, qr: qr, sessions: make(map[string]*accountQR)}
	a.drive = drive
	group := admin.Group("/p115")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if store.IsPersonal(c.Param("accountID")) {
			c.AbortWithStatusJSON(403, gin.H{"error": "个人网盘由个人播放管理维护"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	group.GET("/accounts", func(c *gin.Context) { c.JSON(200, gin.H{"accounts": store.ManagedList()}) })
	group.POST("/accounts", func(c *gin.Context) {
		var in struct {
			Name    string `json:"name"`
			Primary bool   `json:"primary"`
		}
		if c.ShouldBindJSON(&in) != nil {
			a.fail(c, service.ErrAccountInput)
			return
		}
		add := store.Add
		if in.Primary {
			add = store.AddPrimary
		}
		id, err := add(in.Name)
		if err != nil {
			a.fail(c, err)
			return
		}
		c.JSON(200, gin.H{"id": id, "accounts": store.ManagedList()})
	})
	group.PUT("/accounts/:accountID", a.save)
	group.PUT("/accounts/:accountID/playback-directory", func(c *gin.Context) {
		var in struct {
			Revision  uint64 `json:"revision"`
			ParentCID string `json:"parentCid"`
		}
		if c.ShouldBindJSON(&in) != nil {
			a.fail(c, service.ErrAccountInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		if err := store.SaveManagedDirectory(ctx, c.Param("accountID"), in.Revision, in.ParentCID, a.drive); err != nil {
			a.fail(c, err)
			return
		}
		c.JSON(200, gin.H{"accounts": store.ManagedList()})
	})
	group.DELETE("/accounts/:accountID", a.remove)
	group.GET("/accounts/:accountID/replacement/paths", a.replacementPaths)
	group.POST("/accounts/:accountID/replacement/confirm", a.confirmReplacement)
	group.DELETE("/accounts/:accountID/replacement", a.cancelReplacement)
	group.DELETE("/accounts/:accountID/qrcode", a.cancelQR)
	for _, prefix := range []string{"/accounts/:accountID"} {
		group.GET(prefix+"/status", a.status)
		group.GET(prefix+"/cookie", a.cookie)
		group.POST(prefix+"/cookie/test", a.test)
		group.POST(prefix+"/login", a.save)
		group.DELETE(prefix+"/login", a.clear)
		group.POST(prefix+"/qrcode", a.createQR)
		group.GET(prefix+"/qrcode/status", a.pollQR)
	}
}
func (a *p115AccountsAPI) fail(c *gin.Context, err error) {
	code, message := 500, "账号操作失败，请检查存储权限后重试"
	switch {
	case errors.Is(err, service.ErrAccountInvalidCookie):
		code, message = 422, err.Error()
	case errors.Is(err, service.ErrAccountVerification):
		code, message = 502, err.Error()
	case errors.Is(err, service.ErrAccountMissing):
		code, message = 404, err.Error()
	case errors.Is(err, service.ErrAccountChanged):
		code, message = 409, err.Error()
	case errors.Is(err, service.ErrAccountInput):
		code, message = 400, err.Error()
	}
	c.JSON(code, gin.H{"error": message})
}
func (a *p115AccountsAPI) target(c *gin.Context) (service.P115Account, bool) {
	if a.store.IsPersonal(c.Param("accountID")) {
		c.JSON(403, gin.H{"error": "个人网盘请通过个人播放管理操作，不能读取凭据或更换归属"})
		return service.P115Account{}, false
	}
	v, err := a.store.Get(c.Param("accountID"))
	if err != nil {
		a.fail(c, err)
		return v, false
	}
	return v, true
}
func (a *p115AccountsAPI) status(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	if v.Cookie == "" {
		c.JSON(200, gin.H{"configured": false, "connected": false})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	var valid bool
	var err error
	summary := v.Summary
	if reader, ok := a.checker.(interface {
		QuerySummary(context.Context, string, string) (model.P115Summary, bool, error)
	}); ok {
		summary, valid, err = reader.QuerySummary(ctx, v.ID, v.Cookie)
		if err == nil {
			if saveErr := a.store.SaveSummary(v, summary); saveErr != nil {
				a.fail(c, saveErr)
				return
			}
		}
	} else {
		valid, err = a.checker.CheckCookie(ctx, v.Cookie)
	}
	if err != nil {
		c.JSON(502, gin.H{"configured": true, "error": "暂时无法验证已保存的 Cookie，请重试"})
		return
	}
	client, note, err := a.loginClient(ctx, v, valid)
	if err != nil {
		a.fail(c, err)
		return
	}
	if valid && summary.CheckedAt != "" && (summary.VIP == "" || !summary.SpaceKnown) {
		if note != "" {
			note += "；"
		}
		note += "会员或空间信息暂未获取，请稍后重试"
	}
	c.JSON(200, gin.H{"configured": true, "connected": valid, "loginClient": client, "clientMessage": note, "summary": summary})
}

func (a *p115AccountsAPI) loginClient(ctx context.Context, snapshot service.P115Account, valid bool) (string, string, error) {
	client, note := snapshot.LoginClient, ""
	if valid && client == "" {
		if reader, ok := a.checker.(interface {
			CurrentLoginClient(context.Context, string) (string, error)
		}); ok {
			lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
			detected, err := reader.CurrentLoginClient(lookup, snapshot.Cookie)
			cancel()
			if err == nil && detected != "" {
				if err = a.store.SupplementLoginClient(snapshot, detected); err == nil {
					client = detected
				} else if errors.Is(err, service.ErrAccountChanged) || errors.Is(err, service.ErrAccountMissing) {
					return "", "", err
				} else {
					note = "登录客户端信息保存失败，请稍后重试"
				}
			}
		}
		if client == "" && note == "" {
			note = "Cookie 有效，暂未识别登录客户端"
		}
	}
	current, err := a.store.Get(snapshot.ID)
	if err != nil {
		return "", "", err
	}
	if current.Revision != snapshot.Revision || current.Cookie != snapshot.Cookie {
		return "", "", service.ErrAccountChanged
	}
	return client, note, nil
}
func (a *p115AccountsAPI) cookie(c *gin.Context) {
	v, ok := a.target(c)
	if ok {
		c.JSON(200, gin.H{"cookie": v.Cookie})
	}
}
func (a *p115AccountsAPI) test(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	var in struct {
		Cookie string `json:"cookie"`
	}
	if c.ShouldBindJSON(&in) != nil {
		a.fail(c, service.ErrAccountInput)
		return
	}
	cookie := strings.TrimSpace(in.Cookie)
	if cookie == "" {
		cookie = v.Cookie
	}
	if cookie == "" {
		c.JSON(400, gin.H{"error": "请先扫码登录或填写 Cookie"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	valid, err := a.checker.CheckCookie(ctx, cookie)
	if err != nil {
		c.JSON(502, gin.H{"error": "暂时无法验证 Cookie，请稍后重试"})
		return
	}
	if strings.TrimSpace(in.Cookie) == "" {
		client, note, err := a.loginClient(ctx, v, valid)
		if err != nil {
			a.fail(c, err)
			return
		}
		c.JSON(200, gin.H{"valid": valid, "loginClient": client, "clientMessage": note})
		return
	}
	c.JSON(200, gin.H{"valid": valid})
}
func (a *p115AccountsAPI) save(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	var in struct {
		Name     *string `json:"name"`
		Cookie   string  `json:"cookie"`
		Revision uint64  `json:"revision"`
	}
	if c.ShouldBindJSON(&in) != nil {
		a.fail(c, service.ErrAccountInput)
		return
	}
	if c.Param("accountID") != "" && in.Revision == 0 {
		a.fail(c, service.ErrAccountInput)
		return
	}
	if in.Revision != 0 && in.Revision != v.Revision {
		a.fail(c, service.ErrAccountChanged)
		return
	}
	name := v.Name
	if in.Name != nil {
		name = *in.Name
	}
	cookie := strings.TrimSpace(in.Cookie)
	var replacement *string
	if cookie != "" {
		replacement = &cookie
	} else if c.Request.Method == "POST" {
		c.JSON(400, gin.H{"error": "请填写新的 Cookie，空值不会覆盖现有配置"})
		return
	}
	var err error
	if replacement != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
		defer cancel()
		pending := &accountQR{revision: v.Revision, name: name, expires: time.Now().Add(5 * time.Minute)}
		if err = a.startSession(v.ID, pending); err != nil {
			a.fail(c, err)
			return
		}
		a.acceptLogin(c, ctx, v, pending, *replacement)
		return
	} else {
		err = a.store.Update(v.ID, v.Revision, name, nil)
	}
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(200, gin.H{"accounts": a.store.ManagedList(), "message": "账号配置已保存", "configured": cookie != "" || v.Cookie != "", "connected": replacement != nil})
}
func (a *p115AccountsAPI) mutation(c *gin.Context) (service.P115Account, bool) {
	v, ok := a.target(c)
	if !ok {
		return v, false
	}
	var in struct {
		Revision uint64 `json:"revision"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Revision == 0 {
		a.fail(c, service.ErrAccountInput)
		return v, false
	}
	if in.Revision != v.Revision {
		a.fail(c, service.ErrAccountChanged)
		return v, false
	}
	return v, true
}
func (a *p115AccountsAPI) remove(c *gin.Context) {
	v, ok := a.mutation(c)
	if !ok {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.store.Delete(v.ID, v.Revision); err != nil {
		a.fail(c, err)
		return
	}
	a.dropSession(v.ID)
	c.JSON(200, gin.H{"accounts": a.store.ManagedList()})
}
func (a *p115AccountsAPI) clear(c *gin.Context) {
	v, ok := a.mutation(c)
	if !ok {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	empty := ""
	if err := a.store.Update(v.ID, v.Revision, v.Name, &empty); err != nil {
		a.fail(c, err)
		return
	}
	a.dropSession(v.ID)
	c.Status(204)
}

func (a *p115AccountsAPI) createQR(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	var in struct {
		ClientType string `json:"clientType"`
	}
	if c.ShouldBindJSON(&in) != nil {
		a.fail(c, service.ErrAccountInput)
		return
	}
	app, ok := p115.LoginApp(in.ClientType)
	if !ok {
		a.fail(c, service.ErrAccountInput)
		return
	}
	pending := &accountQR{client: app, name: v.Name, revision: v.Revision, expires: time.Now().Add(3 * time.Minute)}
	if err := a.startSession(v.ID, pending); err != nil {
		a.fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	qr, err := a.qr.CreateQRCode(ctx, app)
	if err != nil {
		a.mu.Lock()
		if a.sessions[v.ID] == pending {
			a.dropSession(v.ID)
		}
		a.mu.Unlock()
		c.JSON(502, gin.H{"error": "获取二维码失败，请稍后重试"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.activeSession(v, pending); err != nil {
		a.fail(c, err)
		return
	}
	pending.id, pending.expires = qr.ID, qr.ExpiresAt
	a.armSession(v.ID, pending)
	c.JSON(200, gin.H{"qrId": qr.ID, "sessionId": pending.token, "qrcode": qr.ImageURL, "expiresAt": qr.ExpiresAt})
}
func (a *p115AccountsAPI) pollQR(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	a.mu.Lock()
	pending := a.sessions[v.ID]
	if pending == nil || pending.id == "" || pending.id != c.Query("qrId") || pending.revision != v.Revision || time.Now().After(pending.expires) {
		a.mu.Unlock()
		c.JSON(409, gin.H{"error": "二维码已过期或配置已更新，请重新获取"})
		return
	}
	if pending.busy {
		a.mu.Unlock()
		c.JSON(409, gin.H{"error": "二维码正在验证，请稍后重试"})
		return
	}
	if pending.cookie != "" {
		a.replacementResponse(c, v, pending)
		a.mu.Unlock()
		return
	}
	pending.busy = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); pending.busy = false; a.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	defer cancel()
	status, err := a.qr.CheckQRCode(ctx, pending.id)
	if err != nil {
		c.JSON(502, gin.H{"error": "查询二维码状态失败，请重试"})
		return
	}
	if status.Status == "success" && status.Cookie == "" {
		status.Cookie, err = a.qr.CompleteQRCodeLogin(ctx, pending.id)
		if err != nil {
			c.JSON(502, gin.H{"error": "手机已确认，但获取 Cookie 失败，请重试"})
			return
		}
	}
	if status.Cookie != "" {
		a.acceptLogin(c, ctx, v, pending, status.Cookie)
		return
	}
	c.JSON(200, gin.H{"status": status.Status, "connected": status.Cookie != "", "loginClient": pending.client})
}
