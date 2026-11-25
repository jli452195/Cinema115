package p115

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/netproxy"
)

// CloudTask is the stable subset of a 115 cloud-download task.  Raw keeps the
// upstream fields available while 115 changes its response shape.
type CloudTask struct {
	InfoHash      string          `json:"infoHash,omitempty"`
	Name          string          `json:"name,omitempty"`
	Status        int             `json:"status,omitempty"`
	Progress      float64         `json:"progress,omitempty"`
	DoneBytes     int64           `json:"doneBytes,omitempty"`
	TotalBytes    int64           `json:"totalBytes,omitempty"`
	Error         string          `json:"error,omitempty"`
	DisplayStatus string          `json:"displayStatus,omitempty"`
	StatusText    string          `json:"statusText,omitempty"`
	StatusKnown   bool            `json:"-"`
	Raw           json.RawMessage `json:"-"`
}

// RedoReady returns whether the task may be removed for a new submission and
// whether the upstream state was understood. The single-task and list APIs use
// different numeric status values, so explicit display state wins.
func (t CloudTask) RedoReady() (ready, known bool) {
	display := strings.ToLower(strings.TrimSpace(t.DisplayStatus))
	switch display {
	case "finished", "completed", "success", "failed", "stopped", "cancelled", "canceled":
		return true, true
	case "running", "downloading", "pending", "waiting", "parsing":
		return false, true
	}
	if t.Progress >= 100 {
		return true, true
	}
	if t.Error != "" {
		return true, true
	}
	if !t.StatusKnown {
		return false, false
	}
	switch t.Status {
	case -1, 0, 2:
		return true, true
	case 1:
		return false, true
	default:
		return false, false
	}
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
	HTTP        *http.Client
	BaseURL     string
	SignURL     string
	WebAPI      string
	requestGate cloudRequestGate
}

func NewCloudClient() *CloudClient {
	return &CloudClient{
		HTTP: &http.Client{Transport: netproxy.DirectTransport(), Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
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
var ErrCloudTaskMissing = errors.New("115 云下载任务不存在")
var ErrCloudSubmissionRejected = errors.New("115 拒绝提交")
var ErrCloudSubmissionUncertain = errors.New("115 提交结果未确认")

// CloudSubmissionRejection 只保留 115 返回的数字错误码。
type CloudSubmissionRejection struct {
	Code int64
}

func (e *CloudSubmissionRejection) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s（错误码 %d）", ErrCloudSubmissionRejected, e.Code)
	}
	return ErrCloudSubmissionRejected.Error()
}

func (e *CloudSubmissionRejection) Unwrap() error { return ErrCloudSubmissionRejected }

// CloudURLAddResult 保存 Cookie 批量接口返回的链接和任务标识，调用方按内容匹配。
type CloudURLAddResult struct {
	URL       string
	InfoHash  string
	Rejected  bool
	Confirmed bool
}

type CloudResource struct {
	Protocol string
	Hash     string
}

func (r CloudResource) MatchesTaskHash(value string) bool {
	normalized, ok := normalizeTaskHash(value, r.Protocol)
	return ok && normalized == r.Hash
}

const cloudDownloadUserAgent = "Mozilla/5.0 115disk/36.2.28 115Browser/36.2.28 115wangpan_android/36.2.28"

// ValidateCloudURL accepts only magnet BT links and canonical ED2K file
// links. Other protocols supported by 115 are deliberately outside this
// feature's scope.
func ValidateCloudURL(raw string) (string, error) {
	if _, err := ParseCloudResource(raw); err != nil {
		return "", err
	}
	return strings.TrimSpace(raw), nil
}

// ParseCloudResource extracts the stable identifier used by 115 to de-duplicate
// cloud-download tasks. Magnet Base32 hashes are normalized to 40 lower-case
// hex characters; ED2K keeps its 32-character MD4 hash.
func ParseCloudResource(raw string) (CloudResource, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") || len(raw) > 8192 {
		return CloudResource{}, ErrCloudInput
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "magnet:?xt=urn:btih:") {
		u, err := url.Parse(raw)
		if err != nil {
			return CloudResource{}, ErrCloudInput
		}
		xt := u.Query().Get("xt")
		const prefix = "urn:btih:"
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			return CloudResource{}, ErrCloudInput
		}
		hash := xt[len(prefix):]
		if len(hash) == 40 {
			if _, err := hex.DecodeString(hash); err != nil {
				return CloudResource{}, ErrCloudInput
			}
			return CloudResource{Protocol: "magnet", Hash: strings.ToLower(hash)}, nil
		}
		if len(hash) != 32 {
			return CloudResource{}, ErrCloudInput
		}
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(hash))
		if err != nil || len(decoded) != 20 {
			return CloudResource{}, ErrCloudInput
		}
		return CloudResource{Protocol: "magnet", Hash: hex.EncodeToString(decoded)}, nil
	}
	if strings.HasPrefix(lower, "ed2k://|file|") && strings.HasSuffix(raw, "|/") {
		parts := strings.Split(raw, "|")
		if len(parts) >= 6 && parts[2] != "" && parts[3] != "" && len(parts[4]) == 32 {
			if size, err := strconv.ParseInt(parts[3], 10, 64); err == nil && size >= 0 {
				if _, err := hex.DecodeString(parts[4]); err == nil {
					return CloudResource{Protocol: "ed2k", Hash: strings.ToLower(parts[4])}, nil
				}
			}
		}
	}
	return CloudResource{}, ErrCloudInput
}

func (c *CloudClient) AddURL(ctx context.Context, cookie, rawURL, targetCID string) (json.RawMessage, error) {
	raw, err := c.sendCloudURL(ctx, cookie, rawURL, targetCID)
	if err != nil {
		return nil, err
	}
	return unwrapCloudData(raw)
}

// SubmitURL 用于普通云下载，只核验提交接口本次返回的结果。
func (c *CloudClient) SubmitURL(ctx context.Context, cookie, rawURL, targetCID string) error {
	raw, err := c.sendCloudURL(ctx, cookie, rawURL, targetCID)
	if err != nil {
		return cloudSubmissionResponseError(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ErrCloudSubmissionUncertain
	}
	if cloudExplicitlyRejected(envelope) {
		return cloudSubmissionRejection(envelope)
	}
	fields := envelope
	if data := envelope["data"]; len(data) > 0 && string(data) != "null" {
		fields, err = decodeCloudSubmissionObject(data)
		if err != nil {
			return err
		}
	}
	if cloudExplicitlyRejected(fields) {
		return cloudSubmissionRejection(fields)
	}
	for _, state := range []json.RawMessage{envelope["state"], fields["state"]} {
		if len(state) > 0 && !upstreamOK(state) {
			return ErrCloudSubmissionUncertain
		}
	}
	if upstreamOK(envelope["state"]) || upstreamOK(fields["state"]) {
		return nil
	}
	resource, err := ParseCloudResource(rawURL)
	if err == nil && (resource.MatchesTaskHash(firstString(fields, "info_hash")) || resource.MatchesTaskHash(firstString(envelope, "info_hash"))) {
		return nil
	}
	return ErrCloudSubmissionUncertain
}

func (c *CloudClient) sendCloudURL(ctx context.Context, cookie, rawURL, targetCID string) (json.RawMessage, error) {
	rawURL, err := ValidateCloudURL(rawURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{"url": {rawURL}}
	if err := setCloudTarget(form, targetCID); err != nil {
		return nil, err
	}
	return c.sspRequest(ctx, cookie, "add_task_url", form)
}

// AddURLs 在同一个目标目录直接提交一组链接，只读取提交请求本身的回执。
func (c *CloudClient) AddURLs(ctx context.Context, cookie string, urls []string, targetCID string) ([]CloudURLAddResult, error) {
	if len(urls) == 0 {
		return nil, ErrCloudInput
	}
	form := make(url.Values, len(urls)+1)
	if err := setCloudTarget(form, targetCID); err != nil {
		return nil, err
	}
	for i, input := range urls {
		link, err := ValidateCloudURL(input)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条链接: %w", i+1, err)
		}
		form.Set("url["+strconv.Itoa(i)+"]", link)
	}
	raw, err := c.sspRequest(ctx, cookie, "add_task_urls", form)
	if err != nil {
		return nil, cloudSubmissionResponseError(err)
	}
	return parseCloudURLAddResults(raw)
}

func cloudSubmissionResponseError(err error) error {
	var responseError *cloudResponseError
	if errors.As(err, &responseError) {
		return fmt.Errorf("%w: %w", ErrCloudSubmissionUncertain, err)
	}
	return err
}

func setCloudTarget(form url.Values, cid string) error {
	cid = strings.TrimSpace(cid)
	if cid == "" {
		return nil
	}
	if _, err := strconv.ParseUint(cid, 10, 64); err != nil {
		return fmt.Errorf("%w：目标目录编号无效", ErrCloudInput)
	}
	form.Set("wp_path_id", cid)
	return nil
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
	if err := setCloudTarget(form, targetCID); err != nil {
		return nil, err
	}
	raw, err := c.sspRequest(ctx, cookie, "add_task_bt", form)
	if err != nil {
		return nil, err
	}
	return unwrapCloudData(raw)
}

// LookupTask uses 115's single-hash endpoint. errcode 10021 is the documented
// runtime response for an absent task and is returned as found=false.
func (c *CloudClient) LookupTask(ctx context.Context, cookie string, resource CloudResource) (CloudTask, bool, error) {
	task, err := c.GetTask(ctx, cookie, resource.Hash)
	if errors.Is(err, ErrCloudTaskMissing) {
		return CloudTask{}, false, nil
	}
	if err != nil {
		return CloudTask{}, false, err
	}
	if !resource.MatchesTaskHash(task.InfoHash) {
		return CloudTask{}, false, errors.New("115 返回的云下载任务 Hash 与查询目标不一致")
	}
	return task, true, nil
}

func normalizeTaskHash(value, protocol string) (string, bool) {
	value = strings.TrimSpace(value)
	switch protocol {
	case "magnet":
		if len(value) == 40 {
			if _, err := hex.DecodeString(value); err == nil {
				return strings.ToLower(value), true
			}
		}
		if len(value) == 32 {
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
			if err == nil && len(decoded) == 20 {
				return hex.EncodeToString(decoded), true
			}
		}
	case "ed2k":
		if len(value) == 32 {
			if _, err := hex.DecodeString(value); err == nil {
				return strings.ToLower(value), true
			}
		}
	}
	return "", false
}

// 仅删除精确匹配的任务记录；不传 flag（等价于 0），所有入口始终保留已下载文件。
func (c *CloudClient) DeleteTaskKeepFiles(ctx context.Context, cookie, infoHash string) error {
	infoHash = strings.TrimSpace(infoHash)
	if len(infoHash) != 32 && len(infoHash) != 40 {
		return fmt.Errorf("%w：云下载任务 Hash 无效", ErrCloudInput)
	}
	if _, err := hex.DecodeString(infoHash); err != nil {
		return fmt.Errorf("%w：云下载任务 Hash 无效", ErrCloudInput)
	}
	form := url.Values{"ac": {"task_del"}, "hash[0]": {infoHash}}
	raw, err := c.webRequest(ctx, cookie, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/web/", nil, form)
	if err != nil {
		return err
	}
	if _, err := unwrapCloudData(raw); err != nil {
		return err
	}
	return nil
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
		client = NewCloudClient().HTTP
	} else if client.Transport == nil {
		copy := *client
		copy.Transport = netproxy.DirectTransport()
		client = &copy
	}
	waitStarted := time.Now()
	lease, err := c.requestGate.acquire(ctx, cookie)
	waited := time.Since(waitStarted)
	if err != nil {
		reportRequestTiming(ctx, RequestTiming{Wait: waited})
		return nil, err
	}
	defer lease.release()
	started := time.Now()
	writes := 0
	if method != http.MethodGet {
		writes = 1
	}
	defer func() {
		reportRequestTiming(ctx, RequestTiming{Requests: 1, Writes: writes, Wait: waited, HTTP: time.Since(started)})
	}()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("请求 115 云下载接口: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := retryAfter(resp.Header.Get("Retry-After"))
		lease.observe(resp.StatusCode, retry)
		return nil, &HTTPError{Status: resp.StatusCode, Method: method, Endpoint: req.URL.Host + req.URL.Path, RetryAfter: retry}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, &cloudResponseError{fmt.Errorf("读取 115 云下载响应: %w", err)}
	}
	if len(raw) > 4<<20 {
		return nil, &cloudResponseError{errors.New("115 云下载响应过大")}
	}
	var envelope json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, &cloudResponseError{fmt.Errorf("解析 115 云下载响应: %w", err)}
	}
	return envelope, nil
}

// cloudResponseError 表明请求已发出，客户端未能读取完整响应。
type cloudResponseError struct{ error }

func (e *cloudResponseError) Unwrap() error { return e.error }

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
	_, hasStatus := fields["status"]
	_, hasStat := fields["stat"]
	task.StatusKnown = hasStatus || hasStat
	task.DisplayStatus = firstString(fields, "display_status", "displayStatus")
	task.StatusText = firstString(fields, "status_text", "statusText")
	task.Progress = firstFloat(fields, "percent", "progress", "percent_done", "percentDone", "display_percent")
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

func parseCloudURLAddResults(raw json.RawMessage) ([]CloudURLAddResult, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, ErrCloudSubmissionUncertain
	}
	fields := envelope
	if data := envelope["data"]; envelope["result"] == nil && len(data) > 0 && string(data) != "null" {
		var err error
		fields, err = decodeCloudSubmissionObject(data)
		if err != nil {
			if cloudExplicitlyRejected(envelope) {
				return nil, cloudSubmissionRejection(envelope)
			}
			return nil, err
		}
	}
	var rows []json.RawMessage
	if rawRows := fields["result"]; rawRows != nil {
		if err := json.Unmarshal(rawRows, &rows); err != nil {
			return nil, ErrCloudSubmissionUncertain
		}
	}
	if len(rows) == 0 {
		for _, response := range []map[string]json.RawMessage{fields, envelope} {
			if cloudExplicitlyRejected(response) {
				return nil, cloudSubmissionRejection(response)
			}
		}
		return nil, ErrCloudSubmissionUncertain
	}
	results := make([]CloudURLAddResult, 0, len(rows))
	for _, row := range rows {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(row, &item); err != nil {
			continue
		}
		result := CloudURLAddResult{URL: firstString(item, "url"), InfoHash: firstString(item, "info_hash")}
		if result.URL == "" && result.InfoHash == "" {
			continue
		}
		result.Rejected = cloudExplicitlyRejected(item) || (result.InfoHash == "" && firstInt64(item, "code") != 0)
		result.Confirmed = upstreamOK(item["state"])
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, ErrCloudSubmissionUncertain
	}
	return results, nil
}

func decodeCloudSubmissionObject(data json.RawMessage) (map[string]json.RawMessage, error) {
	var encoded string
	if json.Unmarshal(data, &encoded) == nil {
		decrypted, err := RSADecrypt(encoded)
		if err != nil {
			return nil, ErrCloudSubmissionUncertain
		}
		data = decrypted
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, ErrCloudSubmissionUncertain
	}
	return fields, nil
}

func cloudSubmissionRejection(fields map[string]json.RawMessage) error {
	for _, key := range []string{"errno", "errcode", "code"} {
		if code := firstInt64(fields, key); code != 0 {
			return &CloudSubmissionRejection{Code: code}
		}
	}
	return &CloudSubmissionRejection{}
}

func cloudExplicitlyRejected(fields map[string]json.RawMessage) bool {
	state := string(fields["state"])
	return state == "false" || state == "0" || firstInt64(fields, "errno") != 0 || firstInt64(fields, "errcode") != 0
}

func unwrapCloudData(raw json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("解析 115 云下载响应: %w", err)
	}
	if state, ok := object["state"]; ok && !upstreamOK(state) {
		code := firstInt64(object, "errno", "errcode", "code")
		if code == 10021 {
			return nil, ErrCloudTaskMissing
		}
		message := firstString(object, "error", "message", "msg", "errmsg")
		if message == "" {
			message = "115 拒绝云下载请求"
			if code != 0 {
				message += "（错误码 " + strconv.FormatInt(code, 10) + "）"
			}
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
