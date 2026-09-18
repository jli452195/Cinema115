package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CloudTask is the stable subset of a 115 cloud-download task.  Raw keeps the
// upstream fields available while 115 changes its response shape.
type CloudTask struct {
	InfoHash   string          `json:"infoHash,omitempty"`
	Name       string          `json:"name,omitempty"`
	Status     int             `json:"status,omitempty"`
	Progress   float64         `json:"progress,omitempty"`
	DoneBytes  int64           `json:"doneBytes,omitempty"`
	TotalBytes int64           `json:"totalBytes,omitempty"`
	Error      string          `json:"error,omitempty"`
	Raw        json.RawMessage `json:"-"`
}

// CloudTaskPage is the normalized task-list response. The raw response is
// retained because task fields differ between web and SSP endpoints.
type CloudTaskPage struct {
	Tasks []CloudTask
	Total int
	Raw   json.RawMessage
}

// CloudSign contains the short-lived values returned by the cloud-download
// space endpoint. They are intentionally not persisted.
type CloudSign struct {
	Sign string
	Time int64
}

// CloudClient implements the subset of p115client's cloud-download API used
// by Cinema115. It accepts a cookie per call so an operation is always bound
// to one explicitly selected account.
type CloudClient struct {
	HTTP    *http.Client
	BaseURL string
	SignURL string
	WebAPI  string
}

func NewCloudClient() *CloudClient {
	return &CloudClient{
		HTTP: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		BaseURL: "https://clouddownload.115.com",
		SignURL: "https://115.com/?ct=clouddownload&ac=space",
		WebAPI:  "https://webapi.115.com",
	}
}

// CloudPath is a destination directory returned by 115's cloud-download
// path endpoint. The raw response is retained because older accounts return
// either id/cid and name/n fields.
type CloudPath struct {
	ID   string
	Name string
	Raw  json.RawMessage
}

var ErrCloudInput = errors.New("云下载链接或目录无效")

const cloudDownloadUserAgent = "Mozilla/5.0 115disk/36.2.28 115Browser/36.2.28 115wangpan_android/36.2.28"

// ValidateCloudURL accepts only magnet BT links and canonical ED2K file
// links. Other protocols supported by 115 are deliberately outside this
// feature's scope.
func ValidateCloudURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") || len(raw) > 8192 {
		return "", ErrCloudInput
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "magnet:?xt=urn:btih:") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", ErrCloudInput
		}
		xt := u.Query().Get("xt")
		const prefix = "urn:btih:"
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			return "", ErrCloudInput
		}
		hash := xt[len(prefix):]
		if len(hash) != 32 && len(hash) != 40 {
			return "", ErrCloudInput
		}
		for _, r := range hash {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || (len(hash) == 32 && ((r >= 'g' && r <= 'z') || (r >= 'G' && r <= 'Z')))) {
				return "", ErrCloudInput
			}
		}
		return raw, nil
	}
	if strings.HasPrefix(lower, "ed2k://|file|") && strings.HasSuffix(raw, "|/") {
		parts := strings.Split(raw, "|")
		if len(parts) >= 6 && parts[2] != "" && parts[3] != "" && len(parts[4]) == 32 {
			if _, err := strconv.ParseInt(parts[3], 10, 64); err == nil {
				return raw, nil
			}
		}
	}
	return "", ErrCloudInput
}

func (c *CloudClient) AddURL(ctx context.Context, cookie, rawURL, targetCID string) (json.RawMessage, error) {
	rawURL, err := ValidateCloudURL(rawURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{"url": {rawURL}}
	if strings.TrimSpace(targetCID) != "" {
		if _, err := strconv.ParseUint(strings.TrimSpace(targetCID), 10, 64); err != nil {
			return nil, fmt.Errorf("%w：目标目录编号无效", ErrCloudInput)
		}
		form.Set("wp_path_id", strings.TrimSpace(targetCID))
	}
	return c.sspRequest(ctx, cookie, "add_task_url", form)
}

// AddBT adds a torrent by info hash. wanted is an optional comma-separated
// list of file indexes; leaving it empty lets 115 download the whole torrent.
func (c *CloudClient) AddBT(ctx context.Context, cookie, infoHash, targetCID, wanted string) (json.RawMessage, error) {
	infoHash = strings.TrimSpace(infoHash)
	if len(infoHash) != 40 {
		return nil, fmt.Errorf("%w：BT Hash 无效", ErrCloudInput)
	}
	for _, r := range infoHash {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return nil, fmt.Errorf("%w：BT Hash 无效", ErrCloudInput)
		}
	}
	form := url.Values{"info_hash": {infoHash}}
	if strings.TrimSpace(wanted) != "" {
		form.Set("wanted", strings.TrimSpace(wanted))
	}
	if strings.TrimSpace(targetCID) != "" {
		if _, err := strconv.ParseUint(strings.TrimSpace(targetCID), 10, 64); err != nil {
			return nil, fmt.Errorf("%w：目标目录编号无效", ErrCloudInput)
		}
		form.Set("wp_path_id", strings.TrimSpace(targetCID))
	}
	return c.sspRequest(ctx, cookie, "add_task_bt", form)
}

func (c *CloudClient) GetSign(ctx context.Context, cookie string) (CloudSign, error) {
	raw, err := c.webRequest(ctx, cookie, http.MethodGet, c.SignURL, nil, nil)
	if err != nil {
		return CloudSign{}, err
	}
	var value struct {
		Sign string          `json:"sign"`
		Time json.RawMessage `json:"time"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return CloudSign{}, fmt.Errorf("解析 115 云下载签名: %w", err)
	}
	if value.Sign == "" && len(value.Data) > 0 {
		_ = json.Unmarshal(value.Data, &value)
	}
	if value.Sign == "" {
		return CloudSign{}, fmt.Errorf("115 云下载响应缺少 sign")
	}
	var unix int64
	if len(value.Time) > 0 {
		if err := json.Unmarshal(value.Time, &unix); err != nil {
			var text string
			if json.Unmarshal(value.Time, &text) == nil {
				unix, _ = strconv.ParseInt(text, 10, 64)
			}
		}
	}
	return CloudSign{Sign: value.Sign, Time: unix}, nil
}

func (c *CloudClient) DownloadPaths(ctx context.Context, cookie string, limit int) ([]CloudPath, error) {
	if limit <= 0 || limit > 2000 {
		limit = 1150
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	base := strings.TrimRight(c.WebAPI, "/")
	raw, err := c.webRequest(ctx, cookie, http.MethodGet, base+"/offine/downpath", query, nil)
	if err != nil {
		return nil, err
	}
	value, err := unwrapCloudData(raw)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil {
		var object struct {
			Data  []json.RawMessage `json:"data"`
			List  []json.RawMessage `json:"list"`
			Paths []json.RawMessage `json:"paths"`
		}
		if err := json.Unmarshal(value, &object); err != nil {
			return nil, fmt.Errorf("解析 115 云下载目录: %w", err)
		}
		items = object.Data
		if len(items) == 0 {
			items = object.List
		}
		if len(items) == 0 {
			items = object.Paths
		}
	}
	paths := make([]CloudPath, 0, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, fmt.Errorf("解析 115 云下载目录条目: %w", err)
		}
		id := firstString(fields, "id", "cid", "file_id", "fileId")
		if id == "" {
			id = strconv.FormatInt(firstInt64(fields, "id", "cid", "file_id", "fileId"), 10)
		}
		if id == "" || id == "0" {
			continue
		}
		paths = append(paths, CloudPath{ID: id, Name: firstString(fields, "name", "n", "path"), Raw: append(json.RawMessage(nil), item...)})
	}
	return paths, nil
}

func (c *CloudClient) ListTasks(ctx context.Context, cookie string, page, pageSize int, stat string) (CloudTaskPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 30
	}
	query := url.Values{"ac": {"task_lists"}, "page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(pageSize)}}
	if strings.TrimSpace(stat) != "" {
		query.Set("stat", strings.TrimSpace(stat))
	}
	raw, err := c.webRequest(ctx, cookie, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/web/", query, nil)
	if err != nil {
		return CloudTaskPage{}, err
	}
	return parseCloudTaskPage(raw)
}

func (c *CloudClient) GetTask(ctx context.Context, cookie, infoHash string) (CloudTask, error) {
	infoHash = strings.TrimSpace(infoHash)
	if infoHash == "" {
		return CloudTask{}, fmt.Errorf("%w：任务编号为空", ErrCloudInput)
	}
	query := url.Values{"ac": {"get_user_task"}, "info_hash": {infoHash}}
	raw, err := c.webRequest(ctx, cookie, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/web/", query, nil)
	if err != nil {
		return CloudTask{}, err
	}
	task, err := parseCloudTask(raw)
	if err != nil {
		return CloudTask{}, err
	}
	return task, nil
}

func (c *CloudClient) webRequest(ctx context.Context, cookie string, method string, endpoint string, query url.Values, form url.Values) (json.RawMessage, error) {
	if query != nil {
		endpoint += "?" + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("创建 115 云下载请求: %w", err)
	}
	req.Header.Set("User-Agent", cloudDownloadUserAgent)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("请求 115 云下载接口: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Method: method, Endpoint: req.URL.Host + req.URL.Path, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("读取 115 云下载响应: %w", err)
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("115 云下载响应过大")
	}
	var envelope json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("解析 115 云下载响应: %w", err)
	}
	return envelope, nil
}

func (c *CloudClient) sspRequest(ctx context.Context, cookie, action string, form url.Values) (json.RawMessage, error) {
	payload := make(map[string]string, len(form)+2)
	for key, values := range form {
		if len(values) > 0 {
			payload[key] = values[0]
		}
	}
	payload["ac"] = action
	// Keep this aligned with p115client's current web client version. It is
	// an ordinary compatibility hint, not an account or credential value.
	payload["app_ver"] = "36.2.28"
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("编码 115 云下载参数: %w", err)
	}
	body := url.Values{"data": {RSAEncrypt(encoded)}}
	return c.webRequest(ctx, cookie, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/lixianssp/", nil, body)
}

func parseCloudTaskPage(raw json.RawMessage) (CloudTaskPage, error) {
	value, err := unwrapCloudData(raw)
	if err != nil {
		return CloudTaskPage{}, err
	}
	var page struct {
		Tasks []json.RawMessage `json:"tasks"`
		List  []json.RawMessage `json:"list"`
		Total int               `json:"count"`
	}
	if err := json.Unmarshal(value, &page); err != nil {
		return CloudTaskPage{}, fmt.Errorf("解析 115 云下载任务列表: %w", err)
	}
	items := page.Tasks
	if len(items) == 0 {
		items = page.List
	}
	tasks := make([]CloudTask, 0, len(items))
	for _, item := range items {
		task, err := parseCloudTask(item)
		if err != nil {
			return CloudTaskPage{}, err
		}
		tasks = append(tasks, task)
	}
	return CloudTaskPage{Tasks: tasks, Total: page.Total, Raw: raw}, nil
}

func parseCloudTask(raw json.RawMessage) (CloudTask, error) {
	value, err := unwrapCloudData(raw)
	if err != nil {
		return CloudTask{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return CloudTask{}, fmt.Errorf("解析 115 云下载任务: %w", err)
	}
	task := CloudTask{Raw: append(json.RawMessage(nil), value...)}
	task.InfoHash = firstString(fields, "info_hash", "infoHash", "hash")
	task.Name = firstString(fields, "name", "file_name", "title")
	task.Status = firstInt(fields, "status", "stat")
	task.Progress = firstFloat(fields, "percent", "progress", "percent_done")
	task.DoneBytes = firstInt64(fields, "done_size", "done_bytes", "downloaded")
	task.TotalBytes = firstInt64(fields, "size", "total_size", "total_bytes")
	task.Error = firstString(fields, "error", "error_msg", "message", "errmsg")
	return task, nil
}

// ParseCloudTask normalizes one upstream task payload for callers outside the
// p115 client package.
func ParseCloudTask(raw json.RawMessage) (CloudTask, error) {
	return parseCloudTask(raw)
}

func unwrapCloudData(raw json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("解析 115 云下载响应: %w", err)
	}
	if state, ok := object["state"]; ok && !upstreamOK(state) {
		message := firstString(object, "error", "message", "msg", "errmsg")
		if message == "" {
			message = "115 拒绝云下载请求"
		}
		return nil, errors.New(message)
	}
	if data, ok := object["data"]; ok && len(data) > 0 && string(data) != "null" {
		var encrypted string
		if json.Unmarshal(data, &encrypted) == nil && encrypted != "" {
			if decrypted, err := RSADecrypt(encrypted); err == nil {
				return decrypted, nil
			}
		}
		return data, nil
	}
	return raw, nil
}

func firstString(fields map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		if value, ok := fields[name]; ok {
			var text string
			if json.Unmarshal(value, &text) == nil && text != "" {
				return text
			}
		}
	}
	return ""
}

func firstInt(fields map[string]json.RawMessage, names ...string) int {
	return int(firstInt64(fields, names...))
}

func firstInt64(fields map[string]json.RawMessage, names ...string) int64 {
	for _, name := range names {
		if value, ok := fields[name]; ok {
			var number json.Number
			if json.Unmarshal(value, &number) == nil {
				if parsed, err := number.Int64(); err == nil {
					return parsed
				}
			}
			var text string
			if json.Unmarshal(value, &text) == nil {
				if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
					return parsed
				}
			}
		}
	}
	return 0
}

func firstFloat(fields map[string]json.RawMessage, names ...string) float64 {
	for _, name := range names {
		if value, ok := fields[name]; ok {
			var number float64
			if json.Unmarshal(value, &number) == nil {
				return number
			}
			var text string
			if json.Unmarshal(value, &text) == nil {
				if parsed, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64); err == nil {
					return parsed
				}
			}
		}
	}
	return 0
}
