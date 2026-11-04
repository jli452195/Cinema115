package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

var ErrLifeEventIdentity = errors.New("生活事件文件身份不完整或不一致")

// LifeEventParent 只在事件缺父目录时精确查询一次身份，不读取目录列表或播放资料。
// 文件使用 fid/cid，目录使用 cid/pid；别名必须一致，不能把目录自身当成父目录。
func (c *ShareClient) LifeEventParent(ctx context.Context, id, cookie string) (string, error) {
	if !validDriveID(id, false) {
		return "", fmt.Errorf("生活事件文件编号无效: %w", ErrLifeEventIdentity)
	}
	query := url.Values{"file_id": {id}}
	raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files/get_info?"+query.Encode(), cookie, nil)
	if err != nil {
		return "", fmt.Errorf("核验生活事件文件父目录: %w", err)
	}
	response, err := animeObject(raw)
	if err != nil {
		return "", ErrLifeEventIdentity
	}
	for _, key := range []string{"uid", "user_id"} {
		if _, exists := response[key]; exists {
			uid, err := animeDriveParent(response, key)
			if err != nil || uid == "" || uid != CookieUserID(cookie) {
				return "", ErrLifeEventIdentity
			}
		}
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(response["data"], &rows); err != nil || len(rows) != 1 {
		return "", ErrLifeEventIdentity
	}
	row, err := animeObject(rows[0])
	if err != nil {
		return "", ErrLifeEventIdentity
	}
	if string(row["fid"]) == "0" || string(row["fid"]) == `"0"` {
		delete(row, "fid")
	}
	fileID, err := animeDriveParent(row, "fid", "file_id")
	if err != nil || fileID == "0" {
		return "", ErrLifeEventIdentity
	}
	var parent string
	if fileID != "" {
		if fileID != id {
			return "", ErrLifeEventIdentity
		}
		parent, err = animeDriveParent(row, "cid", "category_id", "pid", "parent_id")
	} else {
		directoryID, identityErr := animeDriveParent(row, "cid", "category_id")
		if identityErr != nil || directoryID != id {
			return "", ErrLifeEventIdentity
		}
		parent, err = animeDriveParent(row, "pid", "parent_id")
	}
	// 先核对精确身份，再接受删除区域；跳转到其他条目不能证明原条目已删除。
	if area, exists := row["aid"]; exists {
		value, areaErr := animeDriveNumber(map[string]json.RawMessage{"aid": area}, "aid")
		if areaErr != nil {
			return "", ErrLifeEventIdentity
		}
		if value == 120 {
			return "", ErrDriveFileMissing
		}
		if value != 1 && value != 12 {
			return "", ErrLifeEventIdentity
		}
	}
	if err != nil || parent == "" || parent == id {
		return "", ErrLifeEventIdentity
	}
	return parent, nil
}
