package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// LifeEvent is an entry from the read-only operation history. An event is a
// change hint, not a complete file declaration: PickCode and even ParentID may
// be absent, and some cloud operations never appear in this history.
type LifeEvent struct {
	ID        string
	FileID    string
	ParentID  string
	Type      int
	UpdatedAt int64
}

func (e LifeEvent) ChangesDirectory() bool {
	switch e.Type {
	case 1, 2, 5, 6, 14, 17, 18, 20, 22, 23, 24:
		return true
	default:
		return false
	}
}

type LifeEventPage struct {
	Events []LifeEvent
	Count  int
}

// LifeEvents reads a repeatable page of history, newest first. It never clears,
// acknowledges or changes upstream records, so other consumers keep their own
// independent watermarks. Web history exposes at most the first 10,000 rows;
// callers must retain low-frequency full directory reconciliation.
//
// API and known coverage gaps are documented by p115client/client.py and
// p115client/tool/life.py (ChenyangGao/p115client). In particular, restore,
// third-party uploads and some rename/copy operations can be missing.
func (c *ShareClient) LifeEvents(ctx context.Context, offset, limit int, cookie string) (LifeEventPage, error) {
	var out LifeEventPage
	if offset < 0 || limit < 1 || limit > 1000 || offset+limit > 10000 {
		return out, fmt.Errorf("生活事件分页参数无效")
	}
	q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
	raw, err := c.request(ctx, http.MethodGet, c.WebURL+"/behavior/detail?"+q.Encode(), cookie, nil)
	if err != nil {
		return out, fmt.Errorf("读取 115 生活事件: %w", err)
	}
	var response struct {
		Count json.Number `json:"count"`
		List  *[]struct {
			ID        json.Number `json:"id"`
			FileID    json.Number `json:"file_id"`
			ParentID  json.Number `json:"parent_id"`
			Type      json.Number `json:"type"`
			UpdatedAt json.Number `json:"update_time"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return out, fmt.Errorf("解析 115 生活事件: %w", err)
	}
	count, err := strconv.Atoi(response.Count.String())
	if err != nil || count < 0 || response.List == nil || len(*response.List) > limit || count < len(*response.List) || len(*response.List) == 0 && offset < count {
		return out, fmt.Errorf("115 返回无效的生活事件分页")
	}
	out.Count = count
	out.Events = make([]LifeEvent, 0, len(*response.List))
	for _, row := range *response.List {
		e := LifeEvent{ID: row.ID.String(), FileID: row.FileID.String(), ParentID: row.ParentID.String()}
		e.Type, err = strconv.Atoi(row.Type.String())
		if err != nil || e.Type <= 0 || !validDriveID(e.ID, false) || e.FileID != "" && !validDriveID(e.FileID, false) || e.ParentID != "" && !validDriveID(e.ParentID, true) {
			return LifeEventPage{}, fmt.Errorf("115 返回无效的生活事件身份")
		}
		if row.UpdatedAt != "" {
			e.UpdatedAt, err = row.UpdatedAt.Int64()
			if err != nil || e.UpdatedAt < 0 {
				return LifeEventPage{}, fmt.Errorf("115 返回无效的生活事件时间")
			}
		}
		out.Events = append(out.Events, e)
	}
	return out, nil
}
