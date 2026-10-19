package p115

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"strm115/backend/internal/model"
)

var ErrAnimeDriveResponse = errors.New("invalid anime drive metadata")

type AnimeDrivePage struct {
	CID     string
	Offset  int
	Count   int
	Entries []model.AnimeDriveEntry
}

func animeDriveString(m map[string]json.RawMessage, keys ...string) (string, error) {
	var out string
	seen := false
	for _, key := range keys {
		if raw, exists := m[key]; exists {
			var value string
			if json.Unmarshal(raw, &value) != nil || seen && value != out {
				return "", ErrAnimeDriveResponse
			}
			out, seen = value, true
		}
	}
	return out, nil
}

func animeDriveNumber(m map[string]json.RawMessage, keys ...string) (int64, error) {
	var out int64
	seen := false
	for _, key := range keys {
		if raw, exists := m[key]; exists {
			var n json.Number
			if json.Unmarshal(raw, &n) != nil {
				return 0, ErrAnimeDriveResponse
			}
			value, err := n.Int64()
			if err != nil || value < 0 || seen && value != out {
				return 0, ErrAnimeDriveResponse
			}
			out, seen = value, true
		}
	}
	if !seen {
		return 0, ErrAnimeDriveResponse
	}
	return out, nil
}

func animeDriveParent(m map[string]json.RawMessage, keys ...string) (string, error) {
	value := ""
	for _, key := range keys {
		if raw, exists := m[key]; exists {
			var id string
			if json.Unmarshal(raw, &id) != nil {
				var n json.Number
				if json.Unmarshal(raw, &n) != nil {
					return "", ErrAnimeDriveResponse
				}
				id = n.String()
			}
			if !validDriveID(id, true) || value != "" && value != id {
				return "", ErrAnimeDriveResponse
			}
			value = id
		}
	}
	return value, nil
}

func parseAnimeDriveEntry(raw []byte) (model.AnimeDriveEntry, error) {
	var out model.AnimeDriveEntry
	m, err := animeObject(raw)
	if err != nil {
		return out, ErrAnimeDriveResponse
	}
	if area, exists := m["aid"]; exists {
		v, err := animeDriveNumber(map[string]json.RawMessage{"aid": area}, "aid")
		if err != nil || v != 1 && v != 12 {
			return out, ErrAnimeDriveResponse
		}
	}
	// Observed directories have cid/pid, files fid/cid or file_id/category_id.
	fid := m["fid"]
	if string(fid) == `"0"` || string(fid) == "0" {
		delete(m, "fid")
	}
	out.ID, err = animeTaskID(m, "fid", "file_id")
	if err != nil {
		return out, ErrAnimeDriveResponse
	}
	if out.ID == "" {
		out.Kind = "directory"
		out.ID, err = animeTaskID(m, "cid", "category_id")
		if err != nil {
			return out, ErrAnimeDriveResponse
		}
		out.ParentCID, err = animeDriveParent(m, "pid", "parent_id")
	} else {
		out.Kind = "file"
		out.ParentCID, err = animeDriveParent(m, "cid", "category_id", "pid", "parent_id")
		if err != nil {
			return out, ErrAnimeDriveResponse
		}
		out.Size, err = animeDriveNumber(m, "s", "file_size", "size")
		if err != nil {
			return out, ErrAnimeDriveResponse
		}
	}
	if err != nil {
		return out, ErrAnimeDriveResponse
	}
	out.Name, err = animeDriveString(m, "n", "file_name", "name")
	if err != nil || !out.Valid() {
		return model.AnimeDriveEntry{}, ErrAnimeDriveResponse
	}
	return out, nil
}

func animeDriveEnvelope(raw []byte, cookie string) (map[string]json.RawMessage, error) {
	m, ok, _, err := animeEnvelope(raw)
	if err != nil || !ok || CookieUserID(cookie) == "" {
		return nil, ErrAnimeDriveResponse
	}
	for _, key := range []string{"uid", "user_id"} {
		if value, exists := m[key]; exists {
			uid, err := animeTaskID(map[string]json.RawMessage{key: value}, key)
			if err != nil || uid != CookieUserID(cookie) {
				return nil, ErrAnimeDriveResponse
			}
		}
	}
	return m, nil
}

// ReadAnimeDrivePage collects names, IDs and types from one scoped listing.
// Hashes are intentionally ignored; this module does not audit file contents.
func (c *ShareClient) ReadAnimeDrivePage(ctx context.Context, cid string, offset int, cookie string) (AnimeDrivePage, error) {
	var out AnimeDrivePage
	if !validDriveID(cid, true) || offset < 0 || offset > 4096 {
		return out, ErrCloudInput
	}
	q := url.Values{"cid": {cid}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}, "show_dir": {"1"}, "o": {"file_name"}, "asc": {"1"}}
	raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
	if err != nil {
		return out, err
	}
	m, err := animeDriveEnvelope(raw, cookie)
	if err != nil {
		return out, err
	}
	gotCID, err := animeDriveParent(m, "cid")
	if err != nil || gotCID != cid {
		return out, ErrAnimeDriveResponse
	}
	count, err := animeDriveNumber(m, "count")
	if err != nil || count > 4096 {
		return out, ErrAnimeDriveResponse
	}
	gotOffset, err := animeDriveNumber(m, "offset")
	if err != nil || gotOffset != int64(offset) {
		return out, ErrAnimeDriveResponse
	}
	if path, exists := m["path"]; exists {
		var parts []json.RawMessage
		if json.Unmarshal(path, &parts) != nil || len(parts) == 0 || len(parts) > 256 {
			return out, ErrAnimeDriveResponse
		}
		last, err := animeObject(parts[len(parts)-1])
		if err != nil {
			return out, ErrAnimeDriveResponse
		}
		id, err := animeDriveParent(last, "cid")
		if err != nil || id != cid {
			return out, ErrAnimeDriveResponse
		}
	}
	var rows []json.RawMessage
	if string(m["data"]) == "null" || json.Unmarshal(m["data"], &rows) != nil || len(rows) > 100 || offset+len(rows) > int(count) || len(rows) == 0 && offset < int(count) {
		return out, ErrAnimeDriveResponse
	}
	out = AnimeDrivePage{CID: cid, Offset: offset, Count: int(count), Entries: make([]model.AnimeDriveEntry, 0, len(rows))}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		entry, err := parseAnimeDriveEntry(row)
		if err != nil || entry.ParentCID != cid || seen[entry.ID] || entry.ID == cid {
			return AnimeDrivePage{}, ErrAnimeDriveResponse
		}
		seen[entry.ID] = true
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}
