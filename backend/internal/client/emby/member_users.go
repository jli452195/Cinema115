package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ManagedUser struct {
	ID     string         `json:"Id"`
	Name   string         `json:"Name"`
	Policy map[string]any `json:"Policy"`
}

func validUserID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdefABCDEF") == ""
}
func (c *Client) ManagedUser(ctx context.Context, id string) (ManagedUser, error) {
	var u ManagedUser
	if !validUserID(id) {
		return u, fmt.Errorf("Emby 用户编号无效")
	}
	err := c.get(ctx, "/Users/"+id, nil, &u)
	if err == nil && (u.ID != id || u.Policy == nil) {
		err = fmt.Errorf("Emby 用户响应无效")
	}
	return u, err
}
func (c *Client) userWrite(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("编码 Emby 用户请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/emby"+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("创建 Emby 用户请求: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Emby 用户操作响应未确认")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Emby 用户操作返回 HTTP %d", resp.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
		return err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取 Emby 用户响应失败")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("Emby 用户操作响应无效")
	}
	return nil
}
func (c *Client) CreateManagedUser(ctx context.Context, name string) (ManagedUser, error) {
	var u ManagedUser
	if !strings.HasPrefix(name, "c115_") {
		return u, fmt.Errorf("Emby 创建参数无效")
	}
	err := c.userWrite(ctx, "/Users/New", map[string]any{"Name": name}, &u)
	if err == nil && (!validUserID(u.ID) || u.Name != name) {
		err = fmt.Errorf("Emby 新用户响应无效")
	}
	return u, err
}
func (c *Client) SetManagedPolicy(ctx context.Context, id string, policy map[string]any) error {
	if !validUserID(id) {
		return fmt.Errorf("Emby 用户编号无效")
	}
	return c.userWrite(ctx, "/Users/"+id+"/Policy", policy, nil)
}
func (c *Client) SetManagedPassword(ctx context.Context, id, password string) error {
	if !validUserID(id) || len(password) < 8 || len(password) > 128 {
		return fmt.Errorf("Emby 密码需为 8–128 字节")
	}
	return c.userWrite(ctx, "/Users/"+id+"/Password", map[string]any{"Id": id, "CurrentPw": "", "NewPw": password, "ResetPassword": false}, nil)
}

func (c *Client) RenameManagedUser(ctx context.Context, id, name string) error {
	if !validUserID(id) || strings.TrimSpace(name) == "" || len(name) > 128 {
		return fmt.Errorf("Emby 用户名称参数无效")
	}
	return c.userWrite(ctx, "/Users/"+id, map[string]any{"Id": id, "Name": name}, nil)
}

func (c *Client) DeleteManagedUser(ctx context.Context, id string) error {
	if !validUserID(id) {
		return fmt.Errorf("Emby 用户编号无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/emby/Users/"+id, nil)
	if err != nil {
		return fmt.Errorf("创建 Emby 删除请求: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Emby 删除响应未确认")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Emby 删除返回 HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
	return err
}
