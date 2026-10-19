package p115

import (
	"bytes"
	"context"
	"errors"
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
	return c.renameEntries(ctx, entries, cookie, false)
}

func (c *ShareClient) RenameAnimeEntries(ctx context.Context, entries []RenameTarget, cookie string) error {
	return c.renameEntries(ctx, entries, cookie, true)
}

func (c *ShareClient) renameEntries(ctx context.Context, entries []RenameTarget, cookie string, receipt bool) error {
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
	raw, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/files/batch_rename", cookie, form)
	if err != nil {
		return fmt.Errorf("115 批量重命名失败: %w", err)
	}
	if receipt {
		return animeBatchReceipt(raw)
	}
	return nil
}

// MoveEntriesBatch uses the web API's indexed fid fields. Keep MoveEntries
// unchanged: existing share/monitor workflows checkpoint individual files.
func (c *ShareClient) MoveEntriesBatch(ctx context.Context, ids []string, cid, cookie string) error {
	return c.moveEntriesBatch(ctx, ids, cid, cookie, false)
}

func (c *ShareClient) MoveAnimeEntries(ctx context.Context, ids []string, cid, cookie string) error {
	return c.moveEntriesBatch(ctx, ids, cid, cookie, true)
}

func (c *ShareClient) moveEntriesBatch(ctx context.Context, ids []string, cid, cookie string, receipt bool) error {
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
	raw, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/files/move", cookie, form)
	if err != nil {
		return fmt.Errorf("115 批量移动失败: %w", err)
	}
	if receipt {
		return animeBatchReceipt(raw)
	}
	return nil
}

var ErrAnimeBatchReceipt = errors.New("anime batch receipt is ambiguous")

// The synchronous web endpoints normally return a bare success envelope.
// Item reports, async acknowledgements and unknown response shapes need scoped
// recovery instead of assuming every member succeeded. No extra success GET.
func animeBatchReceipt(raw []byte) error {
	m, ok, _, err := animeEnvelope(raw)
	if err != nil || !ok {
		return ErrAnimeBatchReceipt
	}
	for key, value := range m {
		switch key {
		case "state", "errno", "errcode", "code", "msg", "message":
		case "error":
			value = bytes.TrimSpace(value)
			if !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte("0")) && !bytes.Equal(value, []byte(`""`)) {
				return ErrAnimeBatchReceipt
			}
		case "data":
			value = bytes.TrimSpace(value)
			if !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte("[]")) && !bytes.Equal(value, []byte("{}")) {
				return ErrAnimeBatchReceipt
			}
		default:
			return ErrAnimeBatchReceipt
		}
	}
	// A missing state must not be inferred from HTTP 200 or an empty object.
	return nil
}
