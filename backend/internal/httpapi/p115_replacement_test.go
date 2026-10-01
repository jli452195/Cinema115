package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
)

type replacementDrive struct {
	cookie         string
	beforeValidate func()
	fail           bool
}

func (d *replacementDrive) ValidateCacheParent(_ context.Context, cid, cookie string) error {
	d.cookie = cookie
	if d.beforeValidate != nil {
		d.beforeValidate()
	}
	if d.fail || cid == "999" {
		return errors.New("missing")
	}
	return nil
}
func (d *replacementDrive) DriveFiles(_ context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	d.cookie = cookie
	return &p115.SharePage{Files: []p115.ShareFile{{ID: "789", Name: "新网盘目录", Directory: true}}, Count: 1}, nil
}
func replacementToken(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var result struct {
		Replacement struct {
			ID string `json:"sessionId"`
		} `json:"replacement"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Replacement.ID == "" {
		t.Fatal("missing pending session", w.Body.String())
	}
	return result.Replacement.ID
}
func TestReplacementSessionIsolationCancellationAndStaleCommit(t *testing.T) {
	for _, mode := range []string{"cancel", "refresh", "revision", "cancel-during-validation", "directory-failure", "success"} {
		t.Run(mode, func(t *testing.T) {
			_, accounts, other := setupAccountsAPI(t, nil, nil)
			drive := &replacementDrive{}
			r := gin.New()
			RegisterP115Accounts(r.Group("/api/admin"), accounts, cookieCheckFunc(func(context.Context, string) (bool, error) { return true, nil }), &accountQRFake{}, drive)
			original, _ := accounts.Get("default")
			response := accountRequest(t, r, "PUT", "/accounts/default", `{"revision":2,"cookie":"UID=999_A; SEID=new-private"}`, 200)
			token := replacementToken(t, response)
			if got, _ := accounts.Get("default"); got != original {
				t.Fatal("staging changed active account")
			}
			accountRequest(t, r, "GET", "/accounts/"+other+"/replacement/paths?sessionId="+token, "", 409)
			accountRequest(t, r, "GET", "/accounts/default/replacement/paths?sessionId="+token, "", 200)
			if drive.cookie != "UID=999_A; SEID=new-private" {
				t.Fatal("browsed with old Cookie")
			}
			code := 409
			switch mode {
			case "cancel":
				accountRequest(t, r, "DELETE", "/accounts/default/replacement?sessionId="+token, "", 204)
			case "refresh":
				accountRequest(t, r, "POST", "/accounts/default/qrcode", `{"clientType":"115-ios"}`, 200)
			case "revision":
				if err := accounts.Update("default", 2, "renamed", nil); err != nil {
					t.Fatal(err)
				}
			case "cancel-during-validation":
				drive.beforeValidate = func() { accountRequest(t, r, "DELETE", "/accounts/default/replacement?sessionId="+token, "", 204) }
			case "directory-failure":
				drive.fail = true
				code = 400
			case "success":
				code = 200
			}
			accountRequest(t, r, "POST", "/accounts/default/replacement/confirm", `{"sessionId":"`+token+`","directoryId":"789"}`, code)
			got, _ := accounts.Get("default")
			if mode == "success" {
				if got.UID != "999" || got.PlaybackParentCID != "789" {
					t.Fatal("replacement not committed")
				}
				accountRequest(t, r, "POST", "/accounts/default/replacement/confirm", `{"sessionId":"`+token+`","directoryId":"789"}`, 409)
			} else if got.Cookie != original.Cookie || got.UID != original.UID || got.PlaybackParentCID != original.PlaybackParentCID {
				t.Fatal("failed or cancelled replacement changed active account")
			}
		})
	}
}
func TestDirectorySaveValidatesSnapshotBeforeCommit(t *testing.T) {
	for _, mode := range []string{"invalid", "changed", "valid"} {
		t.Run(mode, func(t *testing.T) {
			_, accounts, _ := setupAccountsAPI(t, nil, nil)
			drive := &replacementDrive{fail: mode == "invalid"}
			if mode == "changed" {
				drive.beforeValidate = func() {
					cookie := "UID=111_B; SEID=new"
					if err := accounts.Update("default", 2, "name", &cookie); err != nil {
						t.Fatal(err)
					}
				}
			}
			r := gin.New()
			RegisterP115Accounts(r.Group("/api/admin"), accounts, nil, nil, drive)
			code := 200
			if mode == "invalid" {
				code = 400
			}
			if mode == "changed" {
				code = 409
			}
			accountRequest(t, r, "PUT", "/accounts/default/playback-directory", `{"revision":2,"parentCid":"789"}`, code)
			got, _ := accounts.Get("default")
			if (got.PlaybackParentCID == "789") != (mode == "valid") {
				t.Fatal("directory update bypassed verification")
			}
		})
	}
}

type expiringAccountQR struct{ accountQRFake }

func (*expiringAccountQR) CreateQRCode(context.Context, string) (*p115.QRCode, error) {
	return &p115.QRCode{ID: "short-lived", ExpiresAt: time.Now().Add(20 * time.Millisecond)}, nil
}

func TestAccountSessionExpiryAndCredentialRemoval(t *testing.T) {
	for _, mode := range []string{"qr-expiry", "replacement-expiry", "clear", "delete"} {
		t.Run(mode, func(t *testing.T) {
			_, accounts, _ := setupAccountsAPI(t, nil, nil)
			a := &p115AccountsAPI{store: accounts, qr: &expiringAccountQR{}, sessions: make(map[string]*accountQR)}
			r := gin.New()
			g := r.Group("/api/admin/p115/accounts/:accountID")
			g.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
			g.POST("/qrcode", a.createQR)
			g.DELETE("/login", a.clear)
			g.DELETE("", a.remove)
			var pending *accountQR
			if mode == "qr-expiry" {
				accountRequest(t, r, "POST", "/accounts/default/qrcode", `{"clientType":"115-ios"}`, 200)
			} else {
				pending = &accountQR{cookie: "private", revision: 2, expires: time.Now().Add(time.Minute)}
				if mode == "replacement-expiry" {
					pending.expires = time.Now().Add(20 * time.Millisecond)
				}
				if err := a.startSession("default", pending); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "clear" {
				accountRequest(t, r, "DELETE", "/accounts/default/login", `{"revision":2}`, 204)
			} else if mode == "delete" {
				accountRequest(t, r, "DELETE", "/accounts/default", `{"revision":2}`, 200)
			}
			defer func() { a.mu.Lock(); a.dropSession("default"); a.mu.Unlock() }()
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				a.mu.Lock()
				gone := a.sessions["default"] == nil
				cleared := pending == nil || pending.cookie == ""
				a.mu.Unlock()
				if gone {
					if !cleared {
						t.Fatal("expired session retained credentials")
					}
					return
				}
				select {
				case <-deadline.C:
					t.Fatal("session was not cleaned up")
				case <-tick.C:
				}
			}
		})
	}
}
