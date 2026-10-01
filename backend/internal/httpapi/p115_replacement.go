package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

// Sessions are bounded to one per managed account and expire without retaining
// credentials on disk. Timer callbacks only remove memory; they never do I/O.
func (a *p115AccountsAPI) startSession(id string, pending *accountQR) error {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	pending.token = hex.EncodeToString(nonce[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropSession(id)
	a.sessions[id] = pending
	a.armSession(id, pending)
	return nil
}

// All session helpers below require a.mu.
func (a *p115AccountsAPI) dropSession(id string) {
	if previous := a.sessions[id]; previous != nil {
		if previous.timer != nil {
			previous.timer.Stop()
		}
		previous.cookie = ""
		delete(a.sessions, id)
	}
}
func (a *p115AccountsAPI) armSession(id string, pending *accountQR) {
	if pending.timer != nil {
		pending.timer.Stop()
	}
	pending.timer = time.AfterFunc(time.Until(pending.expires), func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.sessions[id] == pending && !time.Now().Before(pending.expires) {
			a.dropSession(id)
		}
	})
}
func (a *p115AccountsAPI) activeSession(v service.P115Account, pending *accountQR) error {
	if pending == nil || a.sessions[v.ID] != pending {
		return service.ErrAccountChanged
	}
	if !time.Now().Before(pending.expires) {
		a.dropSession(v.ID)
		return service.ErrAccountChanged
	}
	current, err := a.store.Get(v.ID)
	if err != nil {
		a.dropSession(v.ID)
		return err
	}
	if current.Revision != pending.revision {
		a.dropSession(v.ID)
		return service.ErrAccountChanged
	}
	return nil
}
func (a *p115AccountsAPI) replacementResponse(c *gin.Context, v service.P115Account, pending *accountQR) {
	c.JSON(http.StatusOK, gin.H{"status": "directory_required", "connected": false, "replacement": gin.H{
		"sessionId": pending.token, "oldUid": v.UID, "newUid": pending.uid, "expiresAt": pending.expires,
	}})
}

func (a *p115AccountsAPI) acceptLogin(c *gin.Context, ctx context.Context, v service.P115Account, pending *accountQR, cookie string) {
	uid := p115.CookieUserID(cookie)
	replacing := !v.Primary && v.UID != "" && uid != "" && v.UID != uid
	var prepared *service.PreparedManagedLogin
	var err error
	if replacing {
		_, err = a.store.VerifyManagedCredential(ctx, v.ID, v.Revision, cookie, a.checker)
	} else {
		client := []string(nil)
		if pending.client != "" {
			client = []string{pending.client}
		}
		prepared, err = a.store.PrepareManagedLogin(ctx, v.ID, v.Revision, pending.name, cookie, "", a.checker, a.drive, client...)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		if a.sessions[v.ID] == pending {
			a.dropSession(v.ID)
		}
		a.fail(c, err)
		return
	}
	if err := a.activeSession(v, pending); err != nil {
		a.fail(c, err)
		return
	}
	if err := ctx.Err(); err != nil {
		a.dropSession(v.ID)
		a.fail(c, err)
		return
	}
	if replacing {
		pending.cookie, pending.uid = cookie, uid
		pending.expires = time.Now().Add(5 * time.Minute)
		a.armSession(v.ID, pending)
		a.replacementResponse(c, v, pending)
		return
	}
	if err := a.store.CommitManagedLogin(ctx, prepared); err != nil {
		a.fail(c, err)
		return
	}
	a.dropSession(v.ID)
	c.JSON(200, gin.H{"status": "success", "configured": true, "connected": true, "loginClient": pending.client, "message": "账号配置已保存"})
}

func (a *p115AccountsAPI) replacementSnapshot(c *gin.Context, v service.P115Account, token string) (*accountQR, accountQR, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pending := a.sessions[v.ID]
	if pending == nil || pending.token != token || pending.cookie == "" || a.activeSession(v, pending) != nil {
		a.fail(c, service.ErrAccountChanged)
		return nil, accountQR{}, false
	}
	return pending, *pending, true
}

func (a *p115AccountsAPI) replacementPaths(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	pending, snapshot, ok := a.replacementSnapshot(c, v, c.Query("sessionId"))
	if !ok {
		return
	}
	reader, ok := a.drive.(interface {
		DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	})
	if !ok {
		a.fail(c, service.ErrAccountInput)
		return
	}
	cid := strings.TrimSpace(c.DefaultQuery("cid", "0"))
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 || offset > 20000 || cid == "" || strings.Trim(cid, "0123456789") != "" {
		a.fail(c, service.ErrAccountInput)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	page, err := reader.DriveFiles(ctx, cid, offset, snapshot.cookie)
	if err != nil {
		c.JSON(502, gin.H{"error": "读取新网盘目录失败，请重试"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.activeSession(v, pending); err != nil {
		a.fail(c, err)
		return
	}
	directories := make([]gin.H, 0, len(page.Files))
	for _, file := range page.Files {
		if file.Directory {
			directories = append(directories, gin.H{"id": file.ID, "name": file.Name})
		}
	}
	c.JSON(200, gin.H{"directories": directories, "hasMore": offset+len(page.Files) < page.Count, "nextOffset": offset + len(page.Files)})
}

func (a *p115AccountsAPI) confirmReplacement(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	var in struct {
		SessionID   string `json:"sessionId"`
		DirectoryID string `json:"directoryId"`
	}
	if c.ShouldBindJSON(&in) != nil {
		a.fail(c, service.ErrAccountInput)
		return
	}
	pending, snapshot, ok := a.replacementSnapshot(c, v, in.SessionID)
	if !ok {
		return
	}
	a.mu.Lock()
	if pending.busy {
		a.mu.Unlock()
		a.fail(c, service.ErrAccountChanged)
		return
	}
	pending.busy = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); pending.busy = false; a.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	defer cancel()
	client := []string(nil)
	if snapshot.client != "" {
		client = []string{snapshot.client}
	}
	prepared, err := a.store.PrepareManagedLogin(ctx, v.ID, snapshot.revision, snapshot.name, snapshot.cookie, in.DirectoryID, a.checker, a.drive, client...)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.activeSession(v, pending); err != nil {
		a.fail(c, err)
		return
	}
	if err := a.store.CommitManagedLogin(ctx, prepared); err != nil {
		a.fail(c, err)
		return
	}
	a.dropSession(v.ID)
	c.JSON(200, gin.H{"status": "success", "connected": true, "configured": true, "loginClient": snapshot.client, "message": "账号已更换"})
}

func (a *p115AccountsAPI) cancelReplacement(c *gin.Context) { a.cancelQR(c) }
func (a *p115AccountsAPI) cancelQR(c *gin.Context) {
	v, ok := a.target(c)
	if !ok {
		return
	}
	token := c.Query("sessionId")
	if token == "" {
		a.fail(c, service.ErrAccountInput)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if pending := a.sessions[v.ID]; pending != nil && pending.token == token {
		a.dropSession(v.ID)
	}
	c.Status(204)
}
