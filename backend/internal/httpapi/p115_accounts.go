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
	"strm115/backend/internal/service"
)

type accountQRClient interface {
	CreateQRCode(context.Context, string) (*p115.QRCode, error)
	CheckQRCode(context.Context, string) (*p115.QRCodeStatus, error)
	CompleteQRCodeLogin(context.Context, string) (string, error)
}
type accountQR struct {
	id       string
	revision uint64
	expires  time.Time
	busy     bool
}
type p115AccountsAPI struct {
	store    *service.P115Accounts
	checker  CookieChecker
	qr       accountQRClient
	mu       sync.Mutex
	sessions map[string]*accountQR
}

func RegisterP115Accounts(admin *gin.RouterGroup, store *service.P115Accounts, checker CookieChecker, qr accountQRClient) {
	a := &p115AccountsAPI{store: store, checker: checker, qr: qr, sessions: make(map[string]*accountQR)}
	group := admin.Group("/p115")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	group.GET("/accounts", func(c *gin.Context) { c.JSON(200, gin.H{"accounts": store.List()}) })
	group.POST("/accounts", func(c *gin.Context) {
		var in struct {
			Name string `json:"name"`
		}
		if c.ShouldBindJSON(&in) != nil {
			a.fail(c, service.ErrAccountInput)
			return
		}
		id, err := store.Add(in.Name)
		if err != nil {
			a.fail(c, err)
			return
		}
		c.JSON(200, gin.H{"id": id, "accounts": store.List()})
	})
	group.PUT("/accounts/:accountID", a.save)
	group.DELETE("/accounts/:accountID", a.remove)
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
	v, err := a.store.Get(c.Param("accountID"))
	if err != nil {
		a.fail(c, err)
		return v, false
	}
	return v, true
}
func (a *p115AccountsAPI) validate(c *gin.Context, cookie string) bool {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	valid, err := a.checker.CheckCookie(ctx, cookie)
	if err != nil {
		c.JSON(502, gin.H{"error": "暂时无法验证 Cookie，原配置未修改"})
		return false
	}
	if !valid {
		c.JSON(422, gin.H{"error": "Cookie 无效或已过期，原配置未修改"})
		return false
	}
	return true
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
	valid, err := a.checker.CheckCookie(ctx, v.Cookie)
	if err != nil {
		c.JSON(502, gin.H{"configured": true, "error": "暂时无法验证已保存的 Cookie，请重试"})
		return
	}
	c.JSON(200, gin.H{"configured": true, "connected": valid})
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
		if !a.validate(c, cookie) {
			return
		}
		replacement = &cookie
	} else if c.Request.Method == "POST" {
		c.JSON(400, gin.H{"error": "请填写新的 Cookie，空值不会覆盖现有配置"})
		return
	}
	if err := a.store.Update(v.ID, v.Revision, name, replacement); err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(200, gin.H{"accounts": a.store.List(), "message": "账号配置已保存", "configured": cookie != "" || v.Cookie != "", "connected": replacement != nil})
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
	if err := a.store.Delete(v.ID, v.Revision); err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(200, gin.H{"accounts": a.store.List()})
}
func (a *p115AccountsAPI) clear(c *gin.Context) {
	v, ok := a.mutation(c)
	if !ok {
		return
	}
	empty := ""
	if err := a.store.Update(v.ID, v.Revision, v.Name, &empty); err != nil {
		a.fail(c, err)
		return
	}
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
	app, ok := map[string]string{"": "alipaymini", "alipay-miniapp": "alipaymini", "wechat-miniapp": "wechatmini", "115-android": "115android", "115-ios": "115ios", "115-life-android": "android", "115-life-ios": "ios"}[in.ClientType]
	if !ok {
		a.fail(c, service.ErrAccountInput)
		return
	}
	pending := &accountQR{revision: v.Revision, expires: time.Now().Add(3 * time.Minute)}
	a.mu.Lock()
	for id, s := range a.sessions {
		if time.Now().After(s.expires) {
			delete(a.sessions, id)
		}
	}
	a.sessions[v.ID] = pending
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	qr, err := a.qr.CreateQRCode(ctx, app)
	if err != nil {
		c.JSON(502, gin.H{"error": "获取二维码失败，请稍后重试"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions[v.ID] != pending {
		a.fail(c, service.ErrAccountChanged)
		return
	}
	pending.id, pending.expires = qr.ID, qr.ExpiresAt
	c.JSON(200, gin.H{"qrId": qr.ID, "qrcode": qr.ImageURL, "expiresAt": qr.ExpiresAt})
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
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.sessions[v.ID] != pending {
			a.fail(c, service.ErrAccountChanged)
			return
		}
		if err := a.store.Update(v.ID, pending.revision, v.Name, &status.Cookie); err != nil {
			a.fail(c, err)
			return
		}
		delete(a.sessions, v.ID)
	}
	c.JSON(200, gin.H{"status": status.Status, "connected": status.Cookie != ""})
}
