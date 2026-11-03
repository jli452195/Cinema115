package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrDriveDirectorySnapshotChanged 表示目录分页事实暂不一致，调用方可有界重读，但不能据此确认条目消失。
var ErrDriveDirectorySnapshotChanged = errors.New("115 目录分页快照暂不一致")

type driveDirectoryItem struct {
	FID      json.Number `json:"fid"`
	CID      json.Number `json:"cid"`
	Name     string      `json:"n"`
	Size     json.Number `json:"s"`
	SHA1     string      `json:"sha"`
	PickCode string      `json:"pc"`
}

func decodeDriveDirectoryItem(item driveDirectoryItem) (ShareFile, error) {
	id := item.FID.String()
	directory := id == "" || id == "0"
	if directory {
		id = item.CID.String()
	}
	if !validDriveID(id, false) {
		return ShareFile{}, fmt.Errorf("网盘条目 ID 无效")
	}
	var size int64
	if item.Size != "" {
		var err error
		size, err = item.Size.Int64()
		if err != nil {
			return ShareFile{}, fmt.Errorf("网盘文件大小无效: %w", err)
		}
	}
	return ShareFile{ID: id, Name: item.Name, Size: size, Directory: directory, SHA1: strings.ToUpper(item.SHA1), PickCode: item.PickCode}, nil
}

// ReadDirectoryFiles 完整读取目录，身份、分页或条目不完整时不返回可用于删除核验的部分结果。
func (c *ShareClient) ReadDirectoryFiles(ctx context.Context, cid, cookie string) (DriveDirectory, []ShareFile, error) {
	if !validDriveID(cid, true) {
		return DriveDirectory{}, nil, fmt.Errorf("目录完整读取编号无效")
	}
	var directory DriveDirectory
	files := make([]ShareFile, 0)
	seen := make(map[string]bool)
	total := -1
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return DriveDirectory{}, nil, err
		}
		q := url.Values{"cid": {cid}, "offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(driveTreePageSize)}, "show_dir": {"1"}, "o": {"file_name"}, "asc": {"1"}}
		raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
		if err != nil {
			return DriveDirectory{}, nil, fmt.Errorf("完整读取目录: %w", err)
		}
		current, err := decodeDriveDirectory(raw, cid)
		if err != nil {
			return DriveDirectory{}, nil, err
		}
		if total >= 0 && current != directory {
			return DriveDirectory{}, nil, fmt.Errorf("目录完整读取路径已变化")
		}
		var response struct {
			Count  json.Number          `json:"count"`
			Offset json.Number          `json:"offset"`
			Data   []driveDirectoryItem `json:"data"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return DriveDirectory{}, nil, fmt.Errorf("解析完整目录列表: %w", err)
		}
		count, countErr := strconv.Atoi(response.Count.String())
		pageOffset, offsetErr := strconv.Atoi(response.Offset.String())
		// 与整理现有分页上限一致，禁止把截断或变化的目录当成完整空目录。
		if countErr != nil || offsetErr != nil || count < 0 || count > 20000 || response.Data == nil || len(response.Data) > driveTreePageSize {
			return DriveDirectory{}, nil, fmt.Errorf("目录完整读取分页结构无效或范围过大")
		}
		if pageOffset != offset || count < offset+len(response.Data) || total >= 0 && total != count || len(response.Data) == 0 && offset < count {
			return DriveDirectory{}, nil, fmt.Errorf("%w：目录完整读取分页不完整或已变化", ErrDriveDirectorySnapshotChanged)
		}
		directory, total = current, count
		for _, item := range response.Data {
			file, err := decodeDriveDirectoryItem(item)
			if err != nil {
				return DriveDirectory{}, nil, err
			}
			if seen[file.ID] || file.ID == cid || !driveTreeName(file.Name) || file.Size < 0 || !file.Directory && item.CID != "" && item.CID.String() != cid {
				return DriveDirectory{}, nil, fmt.Errorf("目录完整读取条目身份或归属无效")
			}
			seen[file.ID] = true
			files = append(files, file)
		}
		offset += len(response.Data)
		if offset == total {
			return directory, files, nil
		}
	}
}
