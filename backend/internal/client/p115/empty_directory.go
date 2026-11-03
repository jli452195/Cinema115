package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// ProbeEmptyDirectory 用一次轻量读取核验目录身份和空状态，根目录不参与清理。
func (c *ShareClient) ProbeEmptyDirectory(ctx context.Context, cid, cookie string) (DriveDirectory, bool, error) {
	if !validDriveID(cid, false) {
		return DriveDirectory{}, false, fmt.Errorf("空目录核验编号无效")
	}
	raw, err := c.readDirectory(ctx, cid, cookie)
	if err != nil {
		return DriveDirectory{}, false, err
	}
	directory, err := decodeDriveDirectory(raw, cid)
	if err != nil {
		return DriveDirectory{}, false, err
	}
	var response struct {
		Count  json.Number       `json:"count"`
		Offset json.Number       `json:"offset"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return DriveDirectory{}, false, fmt.Errorf("解析空目录状态: %w", err)
	}
	count, err := response.Count.Int64()
	if err != nil || count < 0 || response.Data == nil || len(response.Data) > 1 {
		return DriveDirectory{}, false, fmt.Errorf("空目录计数或列表不完整")
	}
	if response.Offset != "" {
		offset, err := response.Offset.Int64()
		if err != nil {
			return DriveDirectory{}, false, fmt.Errorf("空目录列表偏移无效")
		}
		if offset != 0 {
			return DriveDirectory{}, false, fmt.Errorf("%w：空目录列表偏移已变化", ErrDriveDirectorySnapshotChanged)
		}
	}
	if int64(len(response.Data)) > count || count > 0 && len(response.Data) == 0 {
		return DriveDirectory{}, false, fmt.Errorf("%w：空目录计数与列表不一致", ErrDriveDirectorySnapshotChanged)
	}
	return directory, count == 0, nil
}

// DeleteEntriesBatch 将同父目录下的精确编号一次提交至回收站，调用方仍须逐项核验结果。
func (c *ShareClient) DeleteEntriesBatch(ctx context.Context, parent string, ids []string, cookie string) error {
	if !validDriveID(parent, true) || len(ids) == 0 || len(ids) > DriveMutationBatchLimit {
		return fmt.Errorf("批量回收站删除参数无效")
	}
	form := make(url.Values, len(ids)+1)
	form.Set("pid", parent)
	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		if !validDriveID(id, false) || id == parent || seen[id] {
			return fmt.Errorf("批量回收站删除编号无效或重复")
		}
		seen[id] = true
		form.Set("fid["+strconv.Itoa(i)+"]", id)
	}
	// 仅使用现有单次写入机制，超时或忙碌均交给调用方读取核验，不重放整批。
	if _, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/rb/delete", cookie, form); err != nil {
		return fmt.Errorf("115 批量移入回收站失败: %w", err)
	}
	return nil
}
