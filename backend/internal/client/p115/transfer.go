package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func validDriveID(id string, root bool) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && (root || n > 0)
}

// CreateDirectory 仅创建新目录，返回值用于精确限定本次任务的清理范围。
func (c *ShareClient) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	if !validDriveID(parent, true) || name == "" {
		return "", fmt.Errorf("临时目录参数无效")
	}
	raw, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/files/add", cookie, url.Values{"pid": {parent}, "cname": {name}})
	if err != nil {
		return "", err
	}
	var response struct {
		CID json.Number `json:"cid"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("解析新建目录: %w", err)
	}
	id := response.CID.String()
	if !validDriveID(id, false) || id == parent {
		return "", fmt.Errorf("115 未返回有效的新目录 ID")
	}
	return id, nil
}

func (c *ShareClient) ReceiveShare(ctx context.Context, share *ShareInfo, ids []string, cid, cookie string) error {
	if !validDriveID(cid, false) || len(ids) == 0 || len(ids) > 100 {
		return fmt.Errorf("转存参数无效")
	}
	for _, id := range ids {
		if !validDriveID(id, false) {
			return fmt.Errorf("转存条目 ID 无效")
		}
	}
	_, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/share/receive", cookie, url.Values{"share_code": {share.Code}, "receive_code": {share.Password}, "file_id": {strings.Join(ids, ",")}, "cid": {cid}})
	return err
}

func (c *ShareClient) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*SharePage, error) {
	if !validDriveID(cid, false) || offset < 0 {
		return nil, fmt.Errorf("网盘目录参数无效")
	}
	q := url.Values{"cid": {cid}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}, "show_dir": {"1"}, "o": {"file_name"}, "asc": {"1"}}
	raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Count json.Number `json:"count"`
		Data  []struct {
			FID  json.Number `json:"fid"`
			CID  json.Number `json:"cid"`
			Name string      `json:"n"`
			Size json.Number `json:"s"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("解析网盘目录: %w", err)
	}
	count, err := strconv.Atoi(response.Count.String())
	if err != nil || count < 0 {
		return nil, fmt.Errorf("115 返回无效的网盘条目数")
	}
	page := &SharePage{Count: count, Offset: offset, Files: make([]ShareFile, 0, len(response.Data))}
	for _, item := range response.Data {
		id := item.FID.String()
		dir := id == "" || id == "0"
		if dir {
			id = item.CID.String()
		}
		if !validDriveID(id, false) {
			return nil, fmt.Errorf("网盘条目 ID 无效")
		}
		var size int64
		if item.Size != "" {
			size, err = item.Size.Int64()
			if err != nil {
				return nil, fmt.Errorf("网盘文件大小无效: %w", err)
			}
		}
		page.Files = append(page.Files, ShareFile{ID: id, Name: item.Name, Size: size, Directory: dir})
	}
	return page, nil
}

// SendShare 为转存后的条目创建分享；快照是否完成由服务层继续校验。
func (c *ShareClient) SendShare(ctx context.Context, ids []string, cookie string) (*ShareInfo, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, fmt.Errorf("单次转分享最多支持 1000 个顶层条目")
	}
	for _, id := range ids {
		if !validDriveID(id, false) {
			return nil, fmt.Errorf("分享条目 ID 无效")
		}
	}
	raw, err := c.request(ctx, http.MethodPost, c.WebURL+"/share/send", cookie, url.Values{"file_ids": {strings.Join(ids, ",")}, "ignore_warn": {"1"}, "is_asc": {"1"}, "order": {"file_name"}})
	if err != nil {
		return nil, err
	}
	var response struct {
		Code     string `json:"share_code"`
		Password string `json:"receive_code"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("解析新分享: %w", err)
	}
	if response.Code == "" {
		return nil, fmt.Errorf("115 未返回新分享码")
	}
	return &ShareInfo{Code: response.Code, Password: response.Password, URL: "https://115cdn.com/s/" + url.PathEscape(response.Code) + "?password=" + url.QueryEscape(response.Password)}, nil
}

// SetSharePermanent 显式设置长期有效，并读取服务器保存的期限确认，不能依赖账号默认值。
func (c *ShareClient) SetSharePermanent(ctx context.Context, share *ShareInfo, cookie string) error {
	return c.ConfigureShare(ctx, share, "", cookie)
}

// ConfigureShare 设置并回读长期有效和指定密码，只有核实一致后才能生成与清理。
func (c *ShareClient) ConfigureShare(ctx context.Context, share *ShareInfo, password, cookie string) error {
	if share == nil || share.Code == "" {
		return fmt.Errorf("分享码不能为空")
	}
	form := url.Values{"share_code": {share.Code}, "share_duration": {"-1"}}
	if password != "" {
		form.Set("receive_code", password)
	}
	_, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/share/updateshare", cookie, form)
	if err != nil {
		return fmt.Errorf("设置分享密码及长期有效: %w", err)
	}
	if password != "" {
		share.Password = password
		share.URL = "https://115cdn.com/s/" + url.PathEscape(share.Code) + "?password=" + url.QueryEscape(password)
	}
	raw, err := c.request(ctx, http.MethodGet, c.WebURL+"/share/shareinfo?"+url.Values{"share_code": {share.Code}}.Encode(), cookie, nil)
	if err != nil {
		return fmt.Errorf("核实分享有效期: %w", err)
	}
	var info struct {
		Duration json.Number `json:"share_duration"`
		Password string      `json:"receive_code"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf("解析分享有效期: %w", err)
	}
	if info.Duration.String() != "-1" {
		return fmt.Errorf("115 未确认长期有效，已停止后续生成和清理")
	}
	if password != "" && info.Password != password {
		return fmt.Errorf("115 未确认指定分享密码，已停止后续生成和清理")
	}
	return nil
}

// DeleteDirectory 仅移动到回收站，不调用清空回收站或永久删除接口。
func (c *ShareClient) DeleteDirectory(ctx context.Context, cid, cookie string) error {
	if !validDriveID(cid, false) {
		return fmt.Errorf("禁止删除网盘根目录或无效目录")
	}
	_, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/rb/delete", cookie, url.Values{"fid": {cid}})
	return err
}
