package p115

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Protocol reference: p115client upload_init/upload_file_init and p115cipher.
// No OSS upload or automatic replay of a write is performed.
const instantAppVersion = "36.2.28"
const MaxInstantRangeBytes int64 = 8 << 20

var ErrInstantUnsupported = errors.New("115 要求普通上传，此文件未秒传；已停止，不上传整部视频")
var ErrInstantUncertain = errors.New("秒传结果未确认，请先检查目标目录，避免重复提交")
var ErrPlaybackRangeUnavailable = errors.New("缺少本次校验片段，已知副本及源文件暂不可读；资料已保留，请恢复来源后重试")
var instantHash = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
var instantPickCode = regexp.MustCompile(`^[a-zA-Z0-9]{6,64}$`)
var instantRange = regexp.MustCompile(`^([0-9]+)-([0-9]+)$`)

type InstantUploadResult struct {
	FileID     string `json:"fileId,omitempty"`
	PickCode   string `json:"pickCode,omitempty"`
	Challenged bool   `json:"challenged"`
}

// InstantProgress contains only fixed stage names and byte counts, never credentials or URLs.
type InstantProgress struct {
	Stage string
	Bytes int64
}

func ValidateInstantSource(info *DownloadInfo) error {
	if info == nil || !instantHash.MatchString(info.SHA1) || info.Size <= 0 || strings.TrimSpace(info.Name) == "" || len([]rune(info.Name)) > 255 || strings.ContainsAny(info.Name, "/\\\r\n\x00") {
		return fmt.Errorf("源文件缺少有效的完整 SHA1、文件名或大小，无法秒传")
	}
	if !AllowedMediaURL(info.URL) && info.loadPlaybackURL == nil && info.ResolveRangeSHA1 == nil {
		return fmt.Errorf("源文件直链无效")
	}
	return nil
}

// InstantUpload uses only the pinned destination Cookie; range reads never receive it.
func (c *ShareClient) InstantUpload(ctx context.Context, info *DownloadInfo, cid, cookie string, progress func(InstantProgress)) (*InstantUploadResult, error) {
	return c.instantUpload(ctx, info, cid, cookie, progress, false)
}

// InstantUploadToEmptyDirectory combines directory identity and emptiness checks
// in the same preflight request. Playback uses a newly created dedicated folder.
func (c *ShareClient) InstantUploadToEmptyDirectory(ctx context.Context, info *DownloadInfo, cid, cookie string, progress func(InstantProgress)) (*InstantUploadResult, error) {
	// A dedicated playback folder must never silently resolve to drive root.
	if !validDriveID(cid, false) {
		return nil, fmt.Errorf("缓存子目录编号无效，禁止向根目录秒传播放缓存")
	}
	return c.instantUpload(ctx, info, cid, cookie, progress, true)
}

func (c *ShareClient) instantUpload(ctx context.Context, info *DownloadInfo, cid, cookie string, progress func(InstantProgress), requireEmpty bool) (*InstantUploadResult, error) {
	return c.instantUploadPrepared(ctx, info, cid, cookie, progress, requireEmpty, nil)
}

func (c *ShareClient) instantUploadPrepared(ctx context.Context, info *DownloadInfo, cid, cookie string, progress func(InstantProgress), requireEmpty bool, prepared *PlaybackUploadTicket) (*InstantUploadResult, error) {
	if err := ValidateInstantSource(info); err != nil {
		return nil, err
	}
	if !validDriveID(cid, true) || CookieUserID(cookie) == "" {
		return nil, fmt.Errorf("请选择已登录的目标账号和缓存目录")
	}
	report := func(stage string, n int64) {
		if progress != nil {
			progress(InstantProgress{stage, n})
		}
	}
	report("target", 0)
	var identity struct {
		UserID  json.Number     `json:"user_id"`
		UserKey string          `json:"userkey"`
		State   json.RawMessage `json:"state"`
	}
	var raw []byte
	var err error
	if prepared != nil {
		if prepared.cookieHash != playbackCredentialHash(cookie) || time.Now().After(prepared.expires) || prepared.uid != CookieUserID(cookie) || !validDriveID(cid, true) {
			return nil, fmt.Errorf("秒传预检凭据已失效，请重新请求播放")
		}
		identity.UserID, identity.UserKey = json.Number(prepared.uid), prepared.key
	} else {
		// Require explicit directory identity, since deleted directories may silently resolve to root.
		q := url.Values{"cid": {cid}, "limit": {"1"}, "offset": {"0"}}
		if requireEmpty {
			q.Set("show_dir", "1")
		}
		if requireEmpty {
			// Preserve the original cache-directory read's throttling, Retry-After
			// and read-only retry policy while eliminating the duplicate request.
			raw, err = c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
		} else {
			raw, err = c.instantRequest(ctx, http.MethodGet, c.WebURL+"/files?"+q.Encode(), cookie, nil)
		}
		if err != nil {
			return nil, err
		}
		var directory struct {
			State json.RawMessage
			CID   json.Number
			Path  []struct{ CID json.Number }
			Count json.Number
			Data  []json.RawMessage
		}
		if json.Unmarshal(raw, &directory) != nil || !upstreamOK(directory.State) || (directory.CID.String() != cid && len(directory.Path) == 0) || (directory.CID != "" && directory.CID.String() != cid) || (len(directory.Path) > 0 && directory.Path[len(directory.Path)-1].CID.String() != cid) {
			return nil, fmt.Errorf("目标缓存目录无法确认，请重新选择目录")
		}
		if requireEmpty {
			count, err := directory.Count.Int64()
			if err != nil || count != 0 || directory.Data == nil || len(directory.Data) != 0 {
				return nil, fmt.Errorf("新缓存目录未能确认为空，已停止秒传")
			}
		}
		raw, err = c.instantRequest(ctx, http.MethodGet, c.WebURL+"/files/uploadinfo", cookie, nil)
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &identity) != nil || (len(identity.State) > 0 && !upstreamOK(identity.State)) || identity.UserID.String() != CookieUserID(cookie) || identity.UserKey == "" || len(identity.UserKey) > 256 {
			return nil, fmt.Errorf("无法获取目标账号的上传凭据，请检查该账号登录状态")
		}
	}
	form := url.Values{"filename": {info.Name}, "fileid": {strings.ToUpper(info.SHA1)}, "filesize": {strconv.FormatInt(info.Size, 10)}, "target": {"U_1_" + cid}, "userid": {identity.UserID.String()}, "userkey": {identity.UserKey}, "appversion": {instantAppVersion}, "topupload": {"true"}}
	challenged := false
	for attempt := 0; attempt < 2; attempt++ {
		stage := fmt.Sprintf("第 %d 次秒传提交", attempt+1)
		body, token, err := instantPayload(form, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		if attempt == 0 {
			stop := info.StartRangeSourcePrefetch(ctx)
			defer stop()
		}
		report("init", 0)
		raw, err = c.instantRequest(ctx, http.MethodPost, "https://uplb.115.com/4.0/initupload.php?k_ec="+url.QueryEscape(token), cookie, body)
		if err != nil {
			return nil, instantFailure(stage, InstantUploadFailureReason(err), errors.Join(ErrInstantUncertain, err))
		}
		// initupload may append a non-block trailer (observed: 12 bytes).
		// The upload protocol decrypts only complete AES blocks; keep generic AES strict.
		decoded, err := AESDecryptCBC(raw[:len(raw)/16*16])
		if err != nil {
			return nil, instantFailure(stage, fmt.Sprintf("上传响应 AES 解密失败（响应字节数=%d）", len(raw)), ErrInstantUncertain)
		}
		decryptedBytes := len(decoded)
		decoded, err = decompressInstantLZ4(decoded)
		if err != nil {
			return nil, instantFailure(stage, fmt.Sprintf("上传响应 LZ4 解压失败（响应字节数=%d，AES输入字节=%d，密文尾部字节=%d，解密后字节=%d）：%s", len(raw), len(raw)/16*16, len(raw)%16, decryptedBytes, err), ErrInstantUncertain)
		}
		var response struct {
			Status    json.Number `json:"status"`
			SignKey   string      `json:"sign_key"`
			SignCheck string      `json:"sign_check"`
			PickCode  string      `json:"pickcode"`
			FileID    json.Number `json:"file_id"`
		}
		if json.Unmarshal(decoded, &response) != nil {
			return nil, instantFailure(stage, "上传响应 JSON 格式或字段类型无效", ErrInstantUncertain)
		}
		switch response.Status.String() {
		case "2":
			result := &InstantUploadResult{Challenged: challenged}
			if validDriveID(response.FileID.String(), false) {
				result.FileID = response.FileID.String()
			}
			if instantPickCode.MatchString(response.PickCode) {
				result.PickCode = response.PickCode
			}
			if result.FileID == "" && result.PickCode == "" {
				return nil, instantFailure(stage, "115 返回 status=2，但缺少有效 file_id 和 pickcode", ErrInstantUncertain)
			}
			return result, nil
		case "1":
			return nil, instantFailure(stage, "115 要求普通上传，未执行整文件上传（"+instantResponseCodes(decoded)+"）", ErrInstantUnsupported)
		case "7":
			if attempt != 0 || response.SignKey == "" || len(response.SignKey) > 256 {
				return nil, instantFailure(stage, "115 返回 status=7，但重复要求二次校验或校验凭据无效", nil)
			}
			start, end, err := parseInstantRange(response.SignCheck, info.Size)
			if err != nil {
				return nil, instantFailure(stage, "115 返回 status=7，但校验范围格式错误、越界或超过 8 MB", err)
			}
			report("range", end-start+1)
			var hash string
			if info.ResolveRangeSHA1 != nil {
				hash, err = info.ResolveRangeSHA1(ctx, start, end)
			} else {
				hash, err = c.PlaybackRangeSHA1(ctx, info, start, end)
			}
			if err != nil {
				return nil, instantFailure("二次校验片段读取", SharePlaybackFailureReason(err), err)
			}
			if !instantHash.MatchString(hash) {
				return nil, instantFailure("二次校验片段读取", "校验片段 SHA1 无效", nil)
			}
			report("range_done", end-start+1)
			form.Set("sign_key", response.SignKey)
			form.Set("sign_val", strings.ToUpper(hash))
			challenged = true
		default:
			return nil, instantFailure(stage, "115 未返回秒传成功状态（"+instantResponseCodes(decoded)+"）", nil)
		}
	}
	return nil, fmt.Errorf("115 二次校验未完成")
}

func instantPayload(input url.Values, timestamp int64) ([]byte, string, error) {
	form := make(url.Values, len(input)+3)
	for k, v := range input {
		form[k] = append([]string(nil), v...)
	}
	form.Set("t", strconv.FormatInt(timestamp, 10))
	inner := sha1.Sum([]byte(form.Get("userid") + form.Get("fileid") + form.Get("target") + "0"))
	sign := sha1.Sum([]byte(form.Get("userkey") + hex.EncodeToString(inner[:]) + "000000"))
	form.Set("sig", strings.ToUpper(hex.EncodeToString(sign[:])))
	uidMD5 := md5.Sum([]byte(form.Get("userid")))
	tokenMD5 := md5.Sum([]byte("Qclm8MGWUv59TnrR0XPg" + form.Get("fileid") + form.Get("filesize") + form.Get("sign_key") + form.Get("sign_val") + form.Get("userid") + form.Get("t") + hex.EncodeToString(uidMD5[:]) + form.Get("appversion")))
	form.Set("token", hex.EncodeToString(tokenMD5[:]))
	for k := range form {
		if form.Get(k) == "" {
			delete(form, k)
		}
	}
	// Go leaves '~' unescaped, matching Python's quote_plus used by the protocol.
	body, err := AESEncryptCBC([]byte(form.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("编码秒传请求: %w", err)
	}
	pub := []byte{0x1d, 0x03, 0x0e, 0x80, 0xa1, 0x78, 0xdc, 0xee, 0xce, 0xcd, 0xa3, 0x77, 0xde, 0x12, 0x8d, 0x8e, 0xd9, 0xdd, 0xcf, 0x55, 0xae, 0x61, 0xed, 0x46, 0xea, 0x12, 0x1a, 0x1c, 0xfc, 0x81}
	token := make([]byte, 48)
	copy(token, pub[:15])
	token[16] = 0x73
	binary.LittleEndian.PutUint32(token[20:24], uint32(timestamp))
	copy(token[24:39], pub[15:])
	token[40] = 1
	binary.LittleEndian.PutUint32(token[44:], crc32.ChecksumIEEE(append([]byte("^j>WD3Kr?J2gLFjD4W2y@"), token[:44]...)))
	return body, base64.StdEncoding.EncodeToString(token), nil
}

func (c *ShareClient) instantRequest(ctx context.Context, method, endpoint, cookie string, body []byte) ([]byte, error) {
	if err := WaitRequestBudget(ctx, cookie); err != nil {
		return nil, err
	}
	if method == http.MethodPost {
		var finish func()
		ctx, finish = tracePlaybackHTTP(ctx, "秒传提交 HTTP")
		defer finish()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建秒传接口请求失败")
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 115disk/"+instantAppVersion+" 115Browser/"+instantAppVersion+" 115wangpan_android/"+instantAppVersion)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := &http.Client{Transport: c.HTTP.Transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, instantFailure("秒传接口连接", SharePlaybackFailureReason(err), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		ObserveRequestBudget(ctx, resp.StatusCode, retryAfter(resp.Header.Get("Retry-After")))
		return nil, &HTTPError{Status: resp.StatusCode, Method: method, Endpoint: req.URL.Host + req.URL.Path, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, instantFailure("秒传响应读取", SharePlaybackFailureReason(err), err)
	}
	if len(raw) > 1<<20 {
		return nil, instantFailure("秒传响应读取", "响应超过 1 MB 上限", nil)
	}
	return raw, nil
}

func parseInstantRange(value string, size int64) (int64, int64, error) {
	parts := instantRange.FindStringSubmatch(value)
	if parts == nil {
		return 0, 0, fmt.Errorf("115 返回了无效的校验范围")
	}
	start, a := strconv.ParseInt(parts[1], 10, 64)
	end, b := strconv.ParseInt(parts[2], 10, 64)
	if a != nil || b != nil || start < 0 || end < start || end >= size || end-start >= MaxInstantRangeBytes {
		return 0, 0, fmt.Errorf("115 校验范围越界或超过 8 MB 上限")
	}
	return start, end, nil
}

func (c *ShareClient) instantRangeHash(ctx context.Context, link string, start, end, total int64) (string, error) {
	if !AllowedMediaURL(link) {
		return "", fmt.Errorf("校验直链域名无效")
	}
	ctx, finish := tracePlaybackHTTP(ctx, "校验片段 CDN")
	defer finish()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", fmt.Errorf("创建片段校验请求失败")
	}
	req.Header.Set("User-Agent", PlaybackUserAgent)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("Accept-Encoding", "identity")
	client := &http.Client{Transport: c.HTTP.Transport, Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !AllowedMediaURL(req.URL.String()) {
			return fmt.Errorf("校验直链重定向无效")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("读取校验片段连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("校验片段返回 HTTP %d，已停止读取", resp.StatusCode)
	}
	expected := fmt.Sprintf("bytes %d-%d/%d", start, end, total)
	if resp.Header.Get("Content-Range") != expected || (resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity") {
		return "", fmt.Errorf("校验片段范围或编码不匹配")
	}
	hash := sha1.New()
	writer := &playbackHashWriter{writer: hash}
	readStarted := time.Now()
	n, err := io.Copy(writer, io.LimitReader(resp.Body, end-start+2))
	emitPlaybackTiming(ctx, PlaybackTiming{Stage: "片段读取及哈希", Duration: time.Since(readStarted), Bytes: n})
	emitPlaybackTiming(ctx, PlaybackTiming{Stage: "片段 SHA1 计算（包含在读取耗时内）", Duration: writer.duration, Bytes: n})
	if err != nil || n != end-start+1 {
		return "", fmt.Errorf("校验片段长度不匹配或读取失败")
	}
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil))), nil
}
