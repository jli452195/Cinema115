package p115

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	unicodeencoding "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// The upstream export job cannot be canceled. The caller persists its ID and
// owns polling/cleanup, including after cancellation or process restart.
var ErrDriveTreeExportPending = errors.New("115 目录树仍在导出")

// ErrDriveTreeExportUnsupported requests a full metadata listing instead of
// treating an empty or unrecognized export as a complete source snapshot.
var ErrDriveTreeExportUnsupported = errors.New("115 目录树格式不可用于完整核对")

type DriveTreeExport struct {
	ID       string
	FileID   string
	PickCode string
	Name     string
}

const maxDriveTreeBytes int64 = 128 << 20

func (c *ShareClient) StartDriveTreeExport(ctx context.Context, cid, cookie string) (string, error) {
	if !validDriveID(cid, false) {
		return "", fmt.Errorf("导出来源目录无效")
	}
	raw, err := c.request(ctx, http.MethodPost, c.WebURL+"/files/export_dir", cookie, url.Values{"file_ids": {cid}, "target": {"U_1_0"}})
	if err != nil {
		return "", fmt.Errorf("提交目录树导出: %w", err)
	}
	var data struct {
		ID json.Number `json:"export_id"`
	}
	if json.Unmarshal(raw, &data) != nil || !validDriveID(data.ID.String(), false) {
		return "", fmt.Errorf("115 未返回有效目录树任务编号")
	}
	return data.ID.String(), nil
}

func (c *ShareClient) DriveTreeExportStatus(ctx context.Context, id, cookie string) (DriveTreeExport, error) {
	job := DriveTreeExport{ID: id}
	if !validDriveID(id, false) {
		return job, fmt.Errorf("目录树任务编号无效")
	}
	raw, err := c.request(ctx, http.MethodGet, c.WebURL+"/files/export_dir?"+url.Values{"export_id": {id}}.Encode(), cookie, nil)
	if err != nil {
		return job, fmt.Errorf("读取目录树导出进度: %w", err)
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
		return job, nil
	}
	var data struct {
		ID       json.Number `json:"export_id"`
		FileID   json.Number `json:"file_id"`
		Name     string      `json:"file_name"`
		PickCode string      `json:"pick_code"`
	}
	if json.Unmarshal(raw, &data) != nil || data.ID != "" && data.ID.String() != id {
		return job, fmt.Errorf("目录树导出任务身份不一致")
	}
	if data.FileID == "" || data.FileID == "0" {
		if data.Name != "" || data.PickCode != "" {
			return job, fmt.Errorf("目录树导出文件身份不完整")
		}
		return job, nil
	}
	if data.ID.String() != id || !validDriveID(data.FileID.String(), false) || !instantPickCode.MatchString(data.PickCode) || !validDriveTreeName(data.Name) || !strings.EqualFold(path.Ext(data.Name), ".txt") {
		return job, fmt.Errorf("目录树导出文件身份无效")
	}
	job.FileID, job.Name, job.PickCode = data.FileID.String(), data.Name, data.PickCode
	return job, nil
}

// DeleteDriveTreeExport re-reads only this export job before deleting its own
// generated TXT. It never accepts a file ID supplied by a browser or a tree row.
func (c *ShareClient) DeleteDriveTreeExport(ctx context.Context, id, cookie string) error {
	job, err := c.DriveTreeExportStatus(ctx, id, cookie)
	if err != nil {
		return err
	}
	if job.FileID == "" {
		return ErrDriveTreeExportPending
	}
	if err := c.DeleteEntries(ctx, []string{job.FileID}, cookie); err != nil {
		return fmt.Errorf("清理目录树导出文件: %w", err)
	}
	return nil
}

// ReadDriveTreeExport emits leaf paths relative to the selected source root.
// It does not retain the complete tree, wait for a job, or delete cloud files.
func (c *ShareClient) ReadDriveTreeExport(ctx context.Context, job DriveTreeExport, rootName, cookie string, visit func(string) error) error {
	if !validDriveTreeName(rootName) || visit == nil {
		return fmt.Errorf("目录树来源名称或回调无效")
	}
	confirmed, err := c.DriveTreeExportStatus(ctx, job.ID, cookie)
	if err != nil {
		return err
	}
	if confirmed.FileID == "" {
		return ErrDriveTreeExportPending
	}
	if job != confirmed {
		return fmt.Errorf("目录树导出文件已变化")
	}
	info, fileID, err := c.pickCodeInfo(ctx, job.PickCode, cookie, PlaybackUserAgent)
	if err != nil {
		return fmt.Errorf("读取目录树下载地址: %w", err)
	}
	if fileID != job.FileID || info.Name != job.Name || info.Size <= 0 || info.Size > maxDriveTreeBytes || !AllowedMediaURL(info.URL) {
		return fmt.Errorf("目录树下载身份或大小无效")
	}
	return c.readDriveTreeExportURL(ctx, info.URL, info.Size, rootName, visit)
}

func (c *ShareClient) readDriveTreeExportURL(ctx context.Context, link string, size int64, rootName string, visit func(string) error) error {
	if size <= 0 || size > maxDriveTreeBytes || !AllowedMediaURL(link) {
		return fmt.Errorf("目录树下载地址或大小无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return fmt.Errorf("创建目录树下载请求失败")
	}
	req.Header.Set("User-Agent", PlaybackUserAgent)
	// Only the fixed 115 API receives cookies; CDN redirects stay allowlisted.
	client := &http.Client{Transport: c.HTTP.Transport, Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !AllowedMediaURL(req.URL.String()) {
			return fmt.Errorf("目录树下载重定向不被允许")
		}
		return nil
	}}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("目录树下载连接失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength >= 0 && response.ContentLength != size {
		return fmt.Errorf("目录树下载响应不完整（HTTP %d）", response.StatusCode)
	}
	reader := &io.LimitedReader{R: response.Body, N: size + 1}
	if err := parseDriveExportTree(ctx, reader, rootName, visit); err != nil {
		return err
	}
	if reader.N != 1 {
		return fmt.Errorf("目录树下载长度与文件声明不一致")
	}
	return nil
}

func validDriveTreeName(name string) bool {
	return utf8.ValidString(name) && strings.TrimSpace(name) != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\uFFFD") && !strings.ContainsFunc(name, unicode.IsControl)
}

func parseDriveExportTree(ctx context.Context, reader io.Reader, rootName string, visit func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validDriveTreeName(rootName) || visit == nil {
		return fmt.Errorf("目录树来源名称或回调无效")
	}
	decoded := transform.NewReader(reader, unicodeencoding.UTF16(unicodeencoding.LittleEndian, unicodeencoding.ExpectBOM).NewDecoder())
	scanner := bufio.NewScanner(decoded)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	stack := make([]string, 0, 65)
	lineNo, entries := 0, 0
	lastDepth, pending := 0, ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		lineNo++
		line := scanner.Text()
		if lineNo == 1 {
			// 115 names the header after the selected directory's parent;
			// only directories directly under CID 0 use “根目录”. The first
			// indented entry below is still required to match rootName.
			if !strings.HasPrefix(line, "|——") || !validDriveTreeName(strings.TrimPrefix(line, "|——")) {
				return fmt.Errorf("%w: 缺少有效根标记", ErrDriveTreeExportUnsupported)
			}
			continue
		}
		depth := 0
		for strings.HasPrefix(line, "| ") {
			depth++
			line = strings.TrimPrefix(line, "| ")
		}
		if depth == 0 || depth > 65 || !strings.HasPrefix(line, "|-") {
			return fmt.Errorf("%w: 第 %d 行格式无效", ErrDriveTreeExportUnsupported, lineNo)
		}
		name := strings.ReplaceAll(strings.TrimPrefix(line, "|-"), "\\'", "'")
		if !validDriveTreeName(name) || depth > lastDepth+1 || depth == 1 && entries > 0 || entries == 0 && (depth != 1 || name != rootName) {
			return fmt.Errorf("%w: 第 %d 行路径或层级无效", ErrDriveTreeExportUnsupported, lineNo)
		}
		if pending != "" && depth <= lastDepth {
			if err := visit(pending); err != nil {
				return err
			}
		}
		if depth > len(stack)+1 {
			return fmt.Errorf("%w: 层级不连续", ErrDriveTreeExportUnsupported)
		}
		stack = append(stack[:depth-1], name)
		pending = ""
		if depth > 1 {
			pending = strings.Join(stack[1:], "/")
		}
		lastDepth = depth
		entries++
		if entries > 1000000 {
			return fmt.Errorf("%w: 条目过多，请缩小来源目录", ErrDriveTreeExportUnsupported)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%w: 读取目录树: %w", ErrDriveTreeExportUnsupported, err)
	}
	if entries < 2 {
		return fmt.Errorf("%w: 未包含已选择的来源文件，请完整扫描确认", ErrDriveTreeExportUnsupported)
	}
	if pending != "" {
		return visit(pending)
	}
	return nil
}
