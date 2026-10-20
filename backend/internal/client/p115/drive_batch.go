package p115

import (
	"bytes"
	"context"
	"encoding/json"
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
		return animeRenameReceipt(raw, entries)
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

// Only a fixed classification is retained, never upstream text or credentials.
type AnimeBatchReceiptError struct{ reason string }

func (e *AnimeBatchReceiptError) Error() string {
	return ErrAnimeBatchReceipt.Error() + ": " + e.reason
}
func (e *AnimeBatchReceiptError) Unwrap() error  { return ErrAnimeBatchReceipt }
func (e *AnimeBatchReceiptError) Reason() string { return e.reason }

func animeReceiptError(reason string) error { return &AnimeBatchReceiptError{reason: reason} }

// Moves return a synchronous success envelope. Renames can also report the
// resulting names keyed by file ID; every member must match the submitted batch.
// Partial/async/unknown receipts still require scoped recovery, never a replay.
func animeBatchReceipt(raw []byte) error {
	return animeMutationReceipt(raw, nil)
}

func animeRenameReceipt(raw []byte, entries []RenameTarget) error {
	return animeMutationReceipt(raw, entries)
}

func animeMutationReceipt(raw []byte, renamed []RenameTarget) error {
	// The web move endpoint returns {state:true,errno:"",error:""} on
	// synchronous success. Normalize that empty error code only here; exact
	// offline-task identity/missing responses retain their stricter contract.
	fields, err := animeObject(raw)
	if err != nil {
		return animeReceiptError("invalid_envelope")
	}
	if bytes.Equal(bytes.TrimSpace(fields["errno"]), []byte(`""`)) {
		fields["errno"] = json.RawMessage("0")
		raw, err = json.Marshal(fields)
		if err != nil {
			return animeReceiptError("invalid_envelope")
		}
	}
	m, ok, _, err := animeEnvelope(raw)
	if err != nil || !ok {
		return animeReceiptError("invalid_envelope")
	}
	for key, value := range m {
		switch key {
		case "state", "errno", "errcode", "code", "msg", "message":
		case "error":
			value = bytes.TrimSpace(value)
			if !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte("0")) && !bytes.Equal(value, []byte(`""`)) {
				return animeReceiptError("error_field")
			}
		case "data":
			value = bytes.TrimSpace(value)
			if !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte("[]")) && !bytes.Equal(value, []byte("{}")) {
				if len(renamed) != 0 && animeRenameReport(value, renamed) {
					continue
				}
				return animeReceiptError(animeReceiptDataIssue(value))
			}
		default:
			return animeReceiptError("unexpected_field")
		}
	}
	// A missing state must not be inferred from HTTP 200 or an empty object.
	return nil
}

func animeReceiptDataIssue(raw []byte) string {
	if len(raw) > 0 {
		switch raw[0] {
		case '{':
			return "data_object"
		case '[':
			return "data_array"
		case '"':
			return "data_string"
		case 't', 'f':
			return "data_boolean"
		}
	}
	return "data_other"
}

func animeRenameReport(raw []byte, entries []RenameTarget) bool {
	report, err := animeObject(raw)
	if err != nil || len(report) != len(entries) {
		return false
	}
	for _, entry := range entries {
		var name string
		if err := json.Unmarshal(report[entry.ID], &name); err != nil || name != entry.Name {
			return false
		}
	}
	return true
}
