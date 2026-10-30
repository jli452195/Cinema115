package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// DriveTreeEntry preserves cloud names. The caller owns local filename
// sanitizing; IDs and every parent relationship are checked before delivery.
type DriveTreeEntry struct {
	ShareFile
	ParentCID    string
	RelativePath string
}

const driveTreePageSize = 1000

var (
	ErrDriveTreeUnsupported = errors.New("115 不支持此目录的批量扫描")
	ErrDriveTreeIncomplete  = errors.New("115 批量目录扫描数据不完整")
)

// WalkDriveFiles reads a complete directory subtree with batched, read-only
// APIs. Unlike a recursive browser, the flat file listing does not request
// every leaf directory separately. It never derives a trusted ID from a
// PickCode, and never requests file download URLs.
//
// Each delivered batch is independently valid; a later error means that the
// overall scan is incomplete. The caller must not infer deletions on error.
func (c *ShareClient) WalkDriveFiles(ctx context.Context, cid, cookie string, visit func([]DriveTreeEntry) error) error {
	if cid == "0" {
		return ErrDriveTreeUnsupported
	}
	if !validDriveID(cid, false) || visit == nil {
		return fmt.Errorf("完整目录扫描参数无效")
	}
	q := url.Values{"file_id": {cid}}
	raw, err := c.request(ctx, http.MethodGet, c.WebURL+"/files/file?"+q.Encode(), cookie, nil)
	if err != nil {
		return driveTreeRequestError("读取完整扫描来源", err)
	}
	var roots []struct {
		ID       json.Number `json:"file_id"`
		PickCode string      `json:"pick_code"`
		SHA1     string      `json:"sha1"`
	}
	if json.Unmarshal(raw, &roots) != nil || len(roots) != 1 || roots[0].ID.String() != cid || roots[0].SHA1 != "" || !instantPickCode.MatchString(roots[0].PickCode) {
		return fmt.Errorf("%w：来源目录身份无效", ErrDriveTreeIncomplete)
	}
	directories, err := c.driveTreeDirectories(ctx, cid, roots[0].PickCode, cookie)
	if err != nil {
		return err
	}
	if len(directories) > 0 {
		if err := visit(directories); err != nil {
			return err
		}
	}
	paths := make(map[string]string, len(directories)+1)
	paths[cid] = ""
	for _, entry := range directories {
		paths[entry.ID] = entry.RelativePath
	}
	seen := make(map[string]bool)
	total := -1
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		q := url.Values{"cid": {cid}, "cur": {"0"}, "show_dir": {"0"}, "count_folders": {"0"}, "offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(driveTreePageSize)}, "o": {"file_name"}, "asc": {"1"}}
		raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
		if err != nil {
			return driveTreeRequestError("读取完整扫描文件", err)
		}
		var response struct {
			CID    json.Number                 `json:"cid"`
			Count  json.Number                 `json:"count"`
			Offset json.Number                 `json:"offset"`
			Path   []struct{ CID json.Number } `json:"path"`
			Data   []struct {
				FID      json.Number `json:"fid"`
				CID      json.Number `json:"cid"`
				Name     string      `json:"n"`
				Size     json.Number `json:"s"`
				SHA1     string      `json:"sha"`
				PickCode string      `json:"pc"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &response) != nil {
			return fmt.Errorf("%w：文件响应无效", ErrDriveTreeIncomplete)
		}
		if response.CID == "" && len(response.Path) == 0 || response.CID != "" && response.CID.String() != cid || len(response.Path) > 0 && response.Path[len(response.Path)-1].CID.String() != cid {
			return fmt.Errorf("%w：文件列表来源已变化", ErrDriveTreeIncomplete)
		}
		count, countErr := strconv.Atoi(response.Count.String())
		pageOffset, offsetErr := strconv.Atoi(response.Offset.String())
		if countErr != nil || offsetErr != nil || count < 0 || count > 1000000 || pageOffset != offset || response.Data == nil || len(response.Data) > driveTreePageSize || len(response.Data) > count-offset || len(response.Data) == 0 && offset < count || total >= 0 && total != count {
			return fmt.Errorf("%w：文件分页不完整", ErrDriveTreeIncomplete)
		}
		total = count
		batch := make([]DriveTreeEntry, 0, len(response.Data))
		for _, file := range response.Data {
			id, parentID := file.FID.String(), file.CID.String()
			parentPath, found := paths[parentID]
			_, isDirectory := paths[id]
			size, sizeErr := file.Size.Int64()
			if !validDriveID(id, false) || seen[id] || isDirectory || !found || !driveTreeName(file.Name) || sizeErr != nil || size < 0 || !instantHash.MatchString(file.SHA1) || !instantPickCode.MatchString(file.PickCode) {
				return fmt.Errorf("%w：文件身份或目录归属无效", ErrDriveTreeIncomplete)
			}
			seen[id] = true
			batch = append(batch, DriveTreeEntry{ShareFile: ShareFile{ID: id, Name: file.Name, Size: size, SHA1: strings.ToUpper(file.SHA1), PickCode: file.PickCode}, ParentCID: parentID, RelativePath: path.Join(parentPath, file.Name)})
		}
		if len(batch) > 0 {
			if err := visit(batch); err != nil {
				return err
			}
		}
		offset += len(response.Data)
		if offset == count {
			return nil
		}
	}
}

// Only an explicit unimplemented endpoint is a capability fallback. Missing
// resources, authentication failures and transport errors must stop the scan.
func driveTreeRequestError(operation string, err error) error {
	var response *HTTPError
	if errors.As(err, &response) && response.Status == http.StatusNotImplemented {
		return errors.Join(ErrDriveTreeUnsupported, fmt.Errorf("%s: %w", operation, err))
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func driveTreeName(name string) bool {
	return strings.TrimSpace(name) != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl)
}

func (c *ShareClient) driveTreeDirectories(ctx context.Context, rootCID, pickCode, cookie string) ([]DriveTreeEntry, error) {
	entries := make([]DriveTreeEntry, 0)
	byID := make(map[string]int)
	total := -1
	for page := 1; page <= 100; page++ {
		q := url.Values{"pickcode": {pickCode}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(driveTreePageSize)}}
		raw, err := c.request(ctx, http.MethodGet, c.WebURL+"/files/downfolders?"+q.Encode(), cookie, nil)
		if err != nil {
			return nil, driveTreeRequestError("读取完整扫描目录", err)
		}
		var response struct {
			Root struct {
				ID json.Number `json:"fid"`
			} `json:"root"`
			Count json.Number     `json:"count"`
			More  json.RawMessage `json:"has_next_page"`
			List  []struct {
				ID     json.Number `json:"fid"`
				Parent json.Number `json:"pid"`
				Name   string      `json:"fn"`
			} `json:"list"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Root.ID.String() != rootCID || response.List == nil || len(response.List) > driveTreePageSize || string(response.More) != "false" && string(response.More) != "0" && !upstreamOK(response.More) || len(response.List) == 0 && upstreamOK(response.More) {
			return nil, fmt.Errorf("%w：目录分页不完整", ErrDriveTreeIncomplete)
		}
		count, countErr := strconv.Atoi(response.Count.String())
		consumed := len(entries) + len(response.List)
		if countErr != nil || count < 0 || count > 100000 || consumed > count || total >= 0 && total != count || upstreamOK(response.More) != (consumed < count) {
			return nil, fmt.Errorf("%w：目录总数或分页已变化", ErrDriveTreeIncomplete)
		}
		total = count
		for _, dir := range response.List {
			id, parent := dir.ID.String(), dir.Parent.String()
			_, duplicate := byID[id]
			if !validDriveID(id, false) || !validDriveID(parent, true) || id == rootCID || duplicate || !driveTreeName(dir.Name) {
				return nil, fmt.Errorf("%w：目录身份无效", ErrDriveTreeIncomplete)
			}
			byID[id] = len(entries)
			entries = append(entries, DriveTreeEntry{ShareFile: ShareFile{ID: id, Name: dir.Name, Directory: true}, ParentCID: parent})
		}
		if !upstreamOK(response.More) {
			resolved := make(map[string]string, len(entries)+1)
			resolved[rootCID] = ""
			depths := map[string]int{rootCID: 0}
			var resolve func(string, int) (string, error)
			resolve = func(id string, depth int) (string, error) {
				if p, found := resolved[id]; found {
					return p, nil
				}
				index, found := byID[id]
				if !found || depth > 64 {
					return "", fmt.Errorf("%w：目录层级不完整或存在循环", ErrDriveTreeIncomplete)
				}
				parent, err := resolve(entries[index].ParentCID, depth+1)
				if err != nil {
					return "", err
				}
				depths[id] = depths[entries[index].ParentCID] + 1
				if depths[id] > 64 {
					return "", fmt.Errorf("%w：目录层级过深", ErrDriveTreeIncomplete)
				}
				resolved[id] = path.Join(parent, entries[index].Name)
				return resolved[id], nil
			}
			for i := range entries {
				p, err := resolve(entries[i].ID, 0)
				if err != nil {
					return nil, err
				}
				entries[i].RelativePath = p
			}
			// Consumers claim the sanitized parent path before accepting children.
			// Stable ordering also makes the winner of filename collisions repeatable.
			sort.Slice(entries, func(i, j int) bool {
				if left, right := depths[entries[i].ID], depths[entries[j].ID]; left != right {
					return left < right
				}
				if entries[i].RelativePath != entries[j].RelativePath {
					return entries[i].RelativePath < entries[j].RelativePath
				}
				return entries[i].ID < entries[j].ID
			})
			return entries, nil
		}
	}
	return nil, fmt.Errorf("%w：目录数量超过范围", ErrDriveTreeIncomplete)
}
