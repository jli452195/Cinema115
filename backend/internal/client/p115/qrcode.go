package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/netproxy"
)

// QRCode 表示二维码会话。
type QRCode struct {
	ID, ImageURL string
	ExpiresAt    time.Time
}

// QRCodeStatus 表示二维码状态。
type QRCodeStatus struct{ Status, Cookie string }
type session struct {
	Time int64
	Sign string
	App  string
}

// Client 封装二维码请求。
type Client struct {
	HTTP     *http.Client
	BaseURL  string
	mu       sync.RWMutex
	sessions map[string]session
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Transport: netproxy.DirectTransport(), Timeout: 15 * time.Second}, BaseURL: "https://qrcodeapi.115.com", sessions: make(map[string]session)}
}

func (c *Client) CreateQRCode(ctx context.Context, app string) (*QRCode, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/1.0/web/1.0/token/", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var p struct {
		State int `json:"state"`
		Data  struct {
			UID    string `json:"uid"`
			Time   int64  `json:"time"`
			Sign   string `json:"sign"`
			QRCode string `json:"qrcode"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	if p.State != 1 || p.Data.UID == "" {
		return nil, fmt.Errorf("115 未返回二维码会话")
	}
	c.mu.Lock()
	c.sessions[p.Data.UID] = session{p.Data.Time, p.Data.Sign, app}
	c.mu.Unlock()
	return &QRCode{ID: p.Data.UID, ImageURL: p.Data.QRCode, ExpiresAt: time.Now().Add(3 * time.Minute)}, nil
}

func (c *Client) CheckQRCode(ctx context.Context, id string) (*QRCodeStatus, error) {
	c.mu.RLock()
	s, ok := c.sessions[id]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("二维码会话不存在")
	}
	u := c.BaseURL + "/get/status/?uid=" + url.QueryEscape(id) + "&time=" + fmt.Sprint(s.Time) + "&sign=" + url.QueryEscape(s.Sign)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// 上游采用长轮询，等待期间超时不等于登录失败。
		var networkError net.Error
		if ctx.Err() == nil && errors.As(err, &networkError) && networkError.Timeout() {
			return &QRCodeStatus{Status: "waiting"}, nil
		}
		return nil, err
	}
	defer resp.Body.Close()
	var p struct {
		State int `json:"state"`
		Data  struct {
			Status int    `json:"status"`
			Cookie string `json:"cookie"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	status := "waiting"
	if p.State != 1 {
		return nil, fmt.Errorf("115 二维码会话失效，请重新获取")
	}
	value := p.Data.Status
	switch value {
	case 1:
		status = "scanned"
	case 2:
		status = "success"
	case -1:
		status = "expired"
	case -2:
		status = "cancelled"
	}
	return &QRCodeStatus{status, p.Data.Cookie}, nil
}

// CompleteQRCodeLogin 在扫码确认后换取登录 Cookie。
func (c *Client) CompleteQRCodeLogin(ctx context.Context, id string) (string, error) {
	c.mu.RLock()
	s, ok := c.sessions[id]
	c.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("二维码会话不存在")
	}
	u := c.BaseURL + "/app/1.0/" + url.PathEscape(s.App) + "/1.0/login/qrcode/"
	form := url.Values{"account": {id}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if s.App == "ios" {
		req.Header.Set("User-Agent", "UPhone/1.0.0")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var p struct {
		Data struct {
			Cookie json.RawMessage `json:"cookie"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return "", err
	}
	var cookie string
	if json.Unmarshal(p.Data.Cookie, &cookie) != nil {
		var parts map[string]string
		if json.Unmarshal(p.Data.Cookie, &parts) != nil {
			return "", fmt.Errorf("115 登录 Cookie 格式无效")
		}
		for _, key := range []string{"UID", "CID", "SEID", "KID"} {
			if value := parts[key]; value != "" {
				cookie += key + "=" + value + "; "
			}
		}
	}
	if cookie == "" {
		return "", fmt.Errorf("115 未返回登录 Cookie")
	}
	return strings.TrimSpace(cookie), nil
}
