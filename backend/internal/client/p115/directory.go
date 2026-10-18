package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

type DriveDirectory struct {
	CID  string `json:"cid"`
	Path string `json:"path"`
}

// ReadDirectory reads one row at most, using the returned breadcrumb as the
// directory identity. Redirects and missing identity never become valid facts.
func (c *ShareClient) ReadDirectory(ctx context.Context, cid, cookie string) (DriveDirectory, error) {
	var out DriveDirectory
	if !validDriveID(cid, true) {
		return out, fmt.Errorf("invalid directory ID")
	}
	q := url.Values{"cid": {cid}, "offset": {"0"}, "limit": {"1"}, "show_dir": {"1"}}
	raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
	if err != nil {
		return out, fmt.Errorf("read directory: %w", err)
	}
	var data struct {
		CID  json.Number `json:"cid"`
		Path []struct {
			CID  json.Number `json:"cid"`
			Name string      `json:"name"`
			N    string      `json:"n"`
		} `json:"path"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return out, fmt.Errorf("decode directory: %w", err)
	}
	if data.CID != "" && data.CID.String() != cid || len(data.Path) > 256 {
		return out, fmt.Errorf("directory identity mismatch")
	}
	if len(data.Path) == 0 {
		if cid == "0" && data.CID == "0" {
			return DriveDirectory{CID: cid, Path: "/"}, nil
		}
		return out, fmt.Errorf("directory breadcrumb missing")
	}
	if data.Path[len(data.Path)-1].CID.String() != cid {
		return out, fmt.Errorf("directory redirected")
	}
	parts := make([]string, 0, len(data.Path))
	seen := make(map[string]bool, len(data.Path))
	for i, p := range data.Path {
		id := p.CID.String()
		if !validDriveID(id, true) || seen[id] || id == "0" && i != 0 {
			return out, fmt.Errorf("invalid directory breadcrumb")
		}
		seen[id] = true
		if id == "0" {
			continue
		}
		name := p.Name
		if name == "" {
			name = p.N
		}
		if strings.TrimSpace(name) == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.ContainsFunc(name, unicode.IsControl) {
			return out, fmt.Errorf("invalid directory name")
		}
		parts = append(parts, name)
	}
	return DriveDirectory{CID: cid, Path: "/" + strings.Join(parts, "/")}, nil
}
