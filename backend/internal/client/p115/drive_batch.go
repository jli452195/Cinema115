package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// DriveMutationBatchLimit is our conservative chunk size, not an upstream limit.
// Mutations are serialized by metadataRequest and are never automatically retried.
const DriveMutationBatchLimit = 50

type RenameTarget struct {
	ID   string
	Name string
}

// RenameEntries sends one /files/batch_rename form. An accepted response does
// not imply every file succeeded; callers must verify each ID after the call.
func (c *ShareClient) RenameEntries(ctx context.Context, entries []RenameTarget, cookie string) error {
	if len(entries) == 0 || len(entries) > DriveMutationBatchLimit {
		return fmt.Errorf("批量重命名条目数量无效")
	}
	form := make(url.Values, len(entries))
	for _, entry := range entries {
		key := "files_new_name[" + entry.ID + "]"
		if !validDriveID(entry.ID, false) || strings.TrimSpace(entry.Name) == "" || len([]rune(entry.Name)) > 200 || strings.ContainsAny(entry.Name, "/\\\x00\r\n") || form.Has(key) {
			return fmt.Errorf("批量重命名参数无效或文件编号重复")
		}
		form.Set(key, entry.Name)
	}
	if _, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/files/batch_rename", cookie, form); err != nil {
		return fmt.Errorf("115 批量重命名失败: %w", err)
	}
	return nil
}

// MoveEntriesBatch uses the web API's indexed fid fields. Keep MoveEntries
// unchanged: existing share/monitor workflows checkpoint individual files.
func (c *ShareClient) MoveEntriesBatch(ctx context.Context, ids []string, cid, cookie string) error {
	if !validDriveID(cid, false) || len(ids) == 0 || len(ids) > DriveMutationBatchLimit {
		return fmt.Errorf("批量移动参数无效")
	}
	form := make(url.Values, len(ids)+1)
	form.Set("pid", cid)
	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		if !validDriveID(id, false) || seen[id] || id == cid {
			return fmt.Errorf("批量移动文件编号无效或重复")
		}
		seen[id] = true
		form.Set("fid["+strconv.Itoa(i)+"]", id)
	}
	if _, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/files/move", cookie, form); err != nil {
		return fmt.Errorf("115 批量移动失败: %w", err)
	}
	return nil
}
