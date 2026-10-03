package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func embyLoginRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := strings.Trim(strings.ToLower(r.URL.Path), "/")
	path = strings.TrimPrefix(path, "emby/")
	if path == "users/authenticatebyname" {
		return true
	}
	parts := strings.Split(path, "/")
	return len(parts) == 3 && parts[0] == "users" && embyProxyID.MatchString(parts[1]) && parts[2] == "authenticate"
}

func (h *embyProxyHandler) modifyResponse(resp *http.Response) error {
	h.observePoolSession(resp)
	if !embyLoginRequest(resp.Request) || resp.StatusCode != http.StatusOK {
		return h.rewritePlayback(resp)
	}
	// Never read/log the password. Only forward sessions whose personal
	// provisioning and current permissions have been confirmed.
	body := resp.Body
	data, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	resp.Body = &embyReplayBody{Reader: io.MultiReader(bytes.NewReader(data), body), closer: body}
	if err != nil {
		return err
	}
	var login struct {
		AccessToken string
		User        struct {
			ID   string `json:"Id"`
			Name string `json:"Name"`
		}
		SessionInfo struct {
			UserID   string `json:"UserId"`
			DeviceID string `json:"DeviceId"`
			Client   string `json:"Client"`
		}
	}
	if len(data) > 1<<20 || json.Unmarshal(data, &login) != nil || !embyProxyID.MatchString(login.User.ID) || login.AccessToken == "" || len(login.AccessToken) > 2048 || login.SessionInfo.UserID != "" && login.SessionInfo.UserID != login.User.ID {
		h.logs.Add(resp.Request.Context(), "WARNING", "Emby 登录响应缺少有效身份，未记录 302 播放身份")
		return nil
	}
	if h.cache != nil && h.cache.Members != nil {
		if err := h.cache.Members.AuthorizeLogin(resp.Request.Context(), h.config.URL, login.User.ID, login.User.Name); err != nil {
			_ = resp.Body.Close()
			message := []byte(`{"error":"个人播放尚未就绪，请在网站个人中心检查账号状态或等待播放检查"}`)
			resp.StatusCode = http.StatusForbidden
			resp.Status = "403 Forbidden"
			resp.Body = io.NopCloser(bytes.NewReader(message))
			resp.ContentLength = int64(len(message))
			resp.Header.Del("Content-Length")
			resp.Header.Del("Content-Encoding")
			resp.Header.Del("Set-Cookie")
			resp.Header.Set("Content-Type", "application/json; charset=utf-8")
			resp.Header.Set("Cache-Control", "no-store")
			return nil
		}
	}
	if err := h.identities.RememberDevice(h.config.URL, login.AccessToken, login.User.ID, login.SessionInfo.DeviceID, login.SessionInfo.Client); err != nil {
		h.logs.Add(resp.Request.Context(), "ERROR", "Emby 播放身份保存失败，请检查数据目录后通过反代重新登录")
		return nil
	}
	h.logs.Add(resp.Request.Context(), "INFO", "已记录 Emby 登录身份，用户="+login.User.ID+"；播放时仍会向 Emby 校验权限")
	return nil
}

type embyReplayBody struct {
	io.Reader
	closer io.Closer
}

func (b *embyReplayBody) Close() error { return b.closer.Close() }
