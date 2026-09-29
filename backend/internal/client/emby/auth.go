package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// AuthenticateUser deliberately omits the administrator API key. Passwords and
// upstream session tokens never enter the portal's database or responses.
func (c *Client) AuthenticateUser(ctx context.Context, name, password string) (ManagedUser, string, error) {
	var result struct {
		User     ManagedUser `json:"User"`
		ServerID string      `json:"ServerId"`
	}
	raw, err := json.Marshal(map[string]string{"Username": name, "Pw": password})
	if err != nil {
		return result.User, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/emby/Users/AuthenticateByName", bytes.NewReader(raw))
	if err != nil {
		return result.User, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Cinema115", Device="Personal Center", DeviceId="cinema115-portal", Version="1.0"`)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return result.User, "", fmt.Errorf("Emby 登录服务暂不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result.User, "", fmt.Errorf("Emby 登录未通过")
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return ManagedUser{}, "", fmt.Errorf("Emby 登录响应无效")
	}
	if !validUserID(result.User.ID) || result.ServerID == "" || result.User.Policy == nil {
		return ManagedUser{}, "", fmt.Errorf("Emby 登录身份无效")
	}
	return result.User, result.ServerID, nil
}
