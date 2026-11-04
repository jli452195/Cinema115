package p115

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func metadataBatchFile(id, name, code string) ShareFile {
	return ShareFile{ID: id, Name: name, PickCode: code, Size: 100, SHA1: strings.Repeat("A", 40)}
}

func metadataBatchRaw(files []ShareFile) []byte {
	rows := make(map[string]any, len(files))
	for _, file := range files {
		rows[file.ID] = map[string]any{"file_id": file.ID, "pick_code": file.PickCode, "file_name": file.Name, "file_size": file.Size, "sha1": file.SHA1, "url": map[string]string{"url": "https://cdn.115cdn.com/attachment?token=private"}}
	}
	raw, _ := json.Marshal(rows)
	return raw
}

func TestPrepareDriveMetadataSplitsPreviewAndOneOriginalBatch(t *testing.T) {
	poster := metadataBatchFile("1", "poster.jpg", "poster123")
	poster.Thumbnail = "https://thumb.115.com/a/" + poster.SHA1 + "_640?token=private"
	files := []ShareFile{poster, metadataBatchFile("2", "movie.srt", "subtitle123"), metadataBatchFile("3", "movie.nfo", "nfo1234")}
	var requested []ShareFile
	calls := 0
	links, err := prepareDriveMetadata(context.Background(), files, "UID=42_A", "cdn", func(_ context.Context, originals []ShareFile, cookie string) (map[string]DriveMetadataLink, error) {
		calls++
		if cookie != "UID=42_A" {
			t.Fatal("取链切换了凭据")
		}
		requested = originals
		return parseDriveMetadataLinks(metadataBatchRaw(originals), originals)
	})
	if err != nil || calls != 1 || len(requested) != 2 || requested[0].ID != "2" || requested[1].ID != "3" || len(links) != 3 {
		t.Fatal("附件没有按需合并取链", links, requested, calls, err)
	}
	if !links["1"].Preview || !strings.HasPrefix(links["1"].URL, "https://imgjump.115.com/?") || !strings.Contains(links["1"].URL, "size=640") || links["2"].Preview {
		t.Fatal("预览下载没有保留原图规格声明", links)
	}
	encoded, err := json.Marshal(poster)
	if err != nil || bytes.Contains(encoded, []byte("private")) || bytes.Contains(encoded, []byte("Thumbnail")) || bytes.Contains(encoded, []byte("thumb")) {
		t.Fatal("图片临时地址被序列化", string(encoded), err)
	}
}

func TestPrepareDriveMetadataPreviewNeedsNoAPIAndInvalidThumbnailFallsBackOnce(t *testing.T) {
	file := metadataBatchFile("1", "poster.png", "poster123")
	file.Thumbnail = "https://imgjump.115.com/?sha1=" + file.SHA1 + "&size=640"
	c := NewShareClient()
	calls := 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/app/chrome/downurl" || r.Header.Get("Cookie") != "UID=42_A" || r.UserAgent() != PlaybackUserAgent {
			t.Fatal("批量取链请求不正确")
		}
		return instantResponse(http.StatusForbidden, nil), nil
	})
	if links, err := c.PrepareDriveMetadata(context.Background(), []ShareFile{file}, "UID=42_A", "cdn"); err != nil || len(links) != 1 || !links["1"].Preview || calls != 0 {
		t.Fatal("有效预览图触发了额外API", links, calls, err)
	}
	file.Thumbnail = "http://127.0.0.1/private"
	_, err := c.PrepareDriveMetadata(context.Background(), []ShareFile{file, metadataBatchFile("2", "movie.srt", "sub1234")}, "UID=42_A", "cdn")
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Status != http.StatusForbidden || calls != 1 {
		t.Fatal("批量失败拆成单文件请求或丢失403类型", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.PrepareDriveMetadata(ctx, []ShareFile{file}, "UID=42_A", "cdn"); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("取消后仍发出了请求", calls, err)
	}
}

func TestParseDriveMetadataLinksIsolatesUnavailableFilesAndRejectsAmbiguousBatch(t *testing.T) {
	files := []ShareFile{metadataBatchFile("1", "movie.srt", "sub1234"), metadataBatchFile("2", "movie.nfo", "nfo1234")}
	raw := string(metadataBatchRaw(files))
	links, err := parseDriveMetadataLinks([]byte(raw), files)
	if err != nil || len(links) != 2 || links["1"].File != files[0] || links["1"].Err != nil || links["2"].Err != nil {
		t.Fatal(links, err)
	}
	if optional, err := parseDriveMetadataLinks([]byte(strings.ReplaceAll(strings.ReplaceAll(raw, `"file_id":"1",`, ""), `"file_id":"2",`, "")), files); err != nil || optional["1"].Err != nil || optional["2"].Err != nil {
		t.Fatal("顶层映射已确认身份时误拒绝缺可选编号字段", err)
	}
	for _, bad := range []string{
		string(metadataBatchRaw(files[:1])),
		strings.Replace(raw, `"file_id":"1"`, `"file_id":"9"`, 1),
		strings.Replace(raw, `"pick_code":"sub1234"`, `"pick_code":"other123"`, 1),
		strings.Replace(raw, `"file_size":100`, `"file_size":101`, 1),
		strings.Replace(raw, strings.Repeat("A", 40), strings.Repeat("B", 40), 1),
		strings.Replace(raw, `"sha1":"`+strings.Repeat("A", 40)+`"`, `"sha1":""`, 1),
		strings.Replace(raw, `cdn.115cdn.com`, `127.0.0.1`, 1),
		strings.Replace(raw, `https://cdn.115cdn.com/attachment?token=private`, ``, 1),
		strings.Replace(raw, `token=private`, `f=3`, 1),
		strings.Replace(raw, `token=private`, `f=1&f=3`, 1),
	} {
		links, err := parseDriveMetadataLinks([]byte(bad), files)
		if err != nil || len(links) != 2 {
			t.Fatal("单件不可用导致整批失败", err)
		}
		failed := 0
		for _, link := range links {
			if link.Err != nil {
				failed++
				if link.URL != "" || strings.Contains(link.Err.Error(), "private") {
					t.Fatal("失败附件保留了地址或泄漏签名", link.Err)
				}
			} else if link.URL == "" {
				t.Fatal("有效附件丢失下载地址")
			}
		}
		if failed != 1 {
			t.Fatal("单件异常影响了其他附件", failed)
		}
	}
	for _, bad := range []string{
		strings.Replace(raw, `"file_id":"1"`, `"file_id":"1","file_id":"1"`, 1),
		strings.Replace(raw, `"url":{"url":`, `"url":{"url":"","url":`, 1),
		strings.Replace(raw, `"2":`, `"9":`, 1),
		`{"1":{},"1":{}}`,
	} {
		if _, err := parseDriveMetadataLinks([]byte(bad), files); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("歧义批次被接受或泄漏签名", err)
		}
	}
}

// 独立测试密钥只用于模拟上游私钥封包，生产公钥与单文件协议均不改变。
func metadataTestRSAResponse(t *testing.T, key *rsa.PrivateKey, raw []byte) string {
	t.Helper()
	randKey := make([]byte, 16)
	body := p115XOR(raw, p115RSAKey)
	for i, j := 0, len(body)-1; i < j; i, j = i+1, j-1 {
		body[i], body[j] = body[j], body[i]
	}
	plain := append(randKey, p115XOR(body, p115XORKey(randKey, 12))...)
	ciphertext := make([]byte, 0, ((len(plain)+116)/117)*128)
	for start := 0; start < len(plain); start += 117 {
		part := plain[start:min(start+117, len(plain))]
		block := make([]byte, 128)
		block[1] = 1
		for i := 2; i < 127-len(part); i++ {
			block[i] = 255
		}
		copy(block[128-len(part):], part)
		value := new(big.Int).SetBytes(block)
		ciphertext = append(ciphertext, value.Exp(value, key.D, key.N).FillBytes(make([]byte, 128))...)
	}
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func metadataTestRSARequest(t *testing.T, key *rsa.PrivateKey, encoded string) []byte {
	t.Helper()
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%128 != 0 {
		t.Fatal("请求没有按RSA协议封包", err)
	}
	plain := make([]byte, 0, len(ciphertext))
	for start := 0; start < len(ciphertext); start += 128 {
		value := new(big.Int).SetBytes(ciphertext[start : start+128])
		block := value.Exp(value, key.D, key.N).FillBytes(make([]byte, 128))
		separator := bytes.IndexByte(block[2:], 0) + 2
		if separator < 10 {
			t.Fatal("请求RSA填充无效")
		}
		plain = append(plain, block[separator+1:]...)
	}
	if len(plain) < 16 {
		t.Fatal("请求RSA内容不足")
	}
	body := p115XOR(plain[16:], p115GKeyL)
	for i, j := 0, len(body)-1; i < j; i, j = i+1, j-1 {
		body[i], body[j] = body[j], body[i]
	}
	return p115XOR(body, p115RSAKey)
}

func TestPrepareDriveMetadataOneHTTPBatchUsesRealRSAEnvelope(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	previousModulus := p115RSAModulus
	p115RSAModulus = key.N
	defer func() { p115RSAModulus = previousModulus }()
	files := []ShareFile{metadataBatchFile("1", "movie.srt", "sub1234"), metadataBatchFile("2", "movie.nfo", "nfo1234")}
	responseFiles := files
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/app/chrome/downurl" || r.Header.Get("Cookie") != "UID=42_A" || r.UserAgent() != PlaybackUserAgent {
			t.Error("原件批量请求没有固定接口或凭据")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		var payload map[string]string
		if err := json.Unmarshal(metadataTestRSARequest(t, key, r.Form.Get("data")), &payload); err != nil || payload["pickcode"] != "sub1234,nfo1234" || payload["user_id"] != "42" {
			t.Error("批量请求没有合并提取码或丢失账号身份", payload, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"state": true, "data": metadataTestRSAResponse(t, key, metadataBatchRaw(responseFiles))})
	}))
	defer server.Close()
	c := NewShareClient()
	c.AppURL, c.HTTP = server.URL, server.Client()
	poster := metadataBatchFile("3", "poster.png", "poster123")
	poster.Thumbnail = "https://imgjump.115.com/?sha1=" + poster.SHA1 + "&size=640"
	links, err := c.PrepareDriveMetadata(context.Background(), append(files, poster), "UID=42_A", "cdn")
	if err != nil || calls != 1 || len(links) != 3 || !links["3"].Preview || links["1"].URL == "" || links["1"].Err != nil || links["2"].Err != nil {
		t.Fatal("完整HTTP/RSA路径没有一次取得全部原件并保留CDN图片", links, calls, err)
	}
	responseFiles = files[:1]
	links, err = c.PrepareDriveMetadata(context.Background(), files, "UID=42_A", "original")
	if err != nil || calls != 2 || len(links) != 2 || links["1"].Err != nil || links["1"].URL == "" || links["2"].Err == nil || links["2"].URL != "" {
		t.Fatal("一件缺失导致有效附件连带失败或触发额外请求", calls, err)
	}
	if _, err := c.ReadDriveMetadataLink(context.Background(), links["2"]); err == nil || calls != 2 {
		t.Fatal("失败附件仍发出下载请求", calls, err)
	}
	serialized, err := json.Marshal(links["2"])
	if err != nil || bytes.Contains(serialized, []byte("Err")) {
		t.Fatal("附件内存错误被序列化", err)
	}
}

func TestPrepareDriveMetadataRejectsInvalidInputWithoutRequests(t *testing.T) {
	file := metadataBatchFile("1", "poster.jpg", "poster123")
	for _, files := range [][]ShareFile{nil, {file, file}, make([]ShareFile, DriveMetadataBatchLimit+1), {{ID: "1", PickCode: "poster123", SHA1: strings.Repeat("A", 40), Name: "poster.jpg", Size: MaxMetadataSize + 1}}} {
		if _, err := prepareDriveMetadata(context.Background(), files, "UID=42_A", "cdn", func(context.Context, []ShareFile, string) (map[string]DriveMetadataLink, error) {
			t.Fatal("无效参数仍请求网盘")
			return nil, nil
		}); err == nil {
			t.Fatal("无效附件参数被接受")
		}
	}
}

func TestReadDriveMetadataLinkValidatesPreviewAndKeepsHTTPStatus(t *testing.T) {
	imageData := image.NewRGBA(image.Rect(0, 0, 4, 4))
	imageData.Set(0, 0, color.RGBA{R: 200, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	file := metadataBatchFile("1", "poster.png", "poster123")
	file.Size = 2000
	for _, kind := range []string{"compressed-preview", "original-size-mismatch", "html", "format-mismatch", "forbidden", "forbidden-no-request", "untrusted-redirect", "error-image"} {
		t.Run(kind, func(t *testing.T) {
			link := DriveMetadataLink{File: file, Preview: true, URL: "https://imgjump.115.com/path-private?sha1=" + file.SHA1 + "&token=query-private"}
			c := NewShareClient()
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.UserAgent() != PlaybackUserAgent {
					t.Fatal("图片下载携带登录凭据或UA不一致")
				}
				response := instantResponse(http.StatusOK, encoded.Bytes())
				response.Request = r
				switch kind {
				case "html":
					response.Body = io.NopCloser(strings.NewReader("<html>403 Forbidden</html>"))
				case "forbidden", "forbidden-no-request":
					response.StatusCode = http.StatusForbidden
					response.Header.Set("Retry-After", "180")
					if kind == "forbidden-no-request" {
						response.Request = nil
					}
				case "untrusted-redirect":
					response.StatusCode = http.StatusFound
					response.Header.Set("Location", "http://127.0.0.1/private")
				}
				return response, nil
			})
			switch kind {
			case "original-size-mismatch":
				link.Preview = false
			case "format-mismatch":
				link.File.Name = "poster.jpg"
			case "error-image":
				link.URL = "https://cdn.115cdn.com/403.jpg?token=private"
			}
			data, err := c.ReadDriveMetadataLink(context.Background(), link)
			if kind == "compressed-preview" {
				if err != nil || !bytes.Equal(data, encoded.Bytes()) {
					t.Fatal("压缩预览图被原文件大小拒绝", err)
				}
			} else if err == nil {
				t.Fatal("无效下载内容被接受")
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("错误泄漏下载签名", err)
			}
			if kind == "forbidden" || kind == "forbidden-no-request" {
				var upstream *HTTPError
				if !errors.As(err, &upstream) || upstream.Status != http.StatusForbidden || upstream.RetryAfter != 180*time.Second || upstream.Endpoint != "imgjump.115.com" {
					t.Fatal("CDN拒绝未保留类型或冷却时间", err)
				}
			}
			if calls > 1 {
				t.Fatal("自动重试或请求了未授权重定向", calls)
			}
		})
	}
}

func TestDriveFilesKeepsThumbnailInMemoryOnly(t *testing.T) {
	c := NewShareClient()
	c.RequestInterval = 0
	thumb := "https://thumb.115.com/a/" + strings.Repeat("A", 40) + "_640?token=private"
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/files" {
			t.Fatal("读取图片地址额外请求了身份或直链接口")
		}
		return instantResponse(http.StatusOK, []byte(fmt.Sprintf(`{"state":true,"cid":"10","count":1,"offset":0,"data":[{"fid":"1","cid":"10","n":"poster.jpg","s":100,"sha":"%s","pc":"poster123","thumb":%q}]}`, strings.Repeat("A", 40), thumb))), nil
	})
	page, err := c.DriveFiles(context.Background(), "10", 0, "UID=42_A")
	if err != nil || len(page.Files) != 1 || page.Files[0].Thumbnail != thumb {
		t.Fatal("目录列表丢失图片地址", page, err)
	}
	raw, err := json.Marshal(page)
	if err != nil || bytes.Contains(raw, []byte("private")) {
		t.Fatal("目录API响应泄漏图片临时签名", string(raw), err)
	}
	if !reflect.DeepEqual(page.Files[0].SHA1, strings.Repeat("A", 40)) {
		t.Fatal("读取预览地址改变了文件身份")
	}
}

func TestMetadataPreviewRejectsConflictingHashQuery(t *testing.T) {
	file := metadataBatchFile("1", "poster.jpg", "poster123")
	file.Thumbnail = "https://imgjump.115.com/?sha1=" + file.SHA1 + "&sha1=" + strings.Repeat("B", 40)
	if DriveMetadataUsesPreview(file, "cdn") {
		t.Fatal("重复查询参数中的冲突图片身份被忽略")
	}
}

func TestMetadataDownloadIgnoresAPIJarAcrossTrustedRedirect(t *testing.T) {
	c := NewShareClient()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	start, err := url.Parse("https://imgjump.115.com/?token=private")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(start, []*http.Cookie{{Name: "UID", Value: "42_A", Domain: ".115.com", Path: "/"}})
	c.HTTP.Jar = jar
	calls := 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("附件下载或重定向继承了 API Cookie Jar")
		}
		response := instantResponse(http.StatusOK, []byte("metadata"))
		response.Request = r
		if calls == 1 {
			response.StatusCode = http.StatusFound
			response.Header.Set("Location", "https://thumb.115.com/image?token=private")
			response.Header.Set("Set-Cookie", "download=private; Domain=.115.com; Path=/")
		}
		return response, nil
	})
	data, err := c.readMetadataURLMode(context.Background(), start.String(), true)
	if err != nil || string(data) != "metadata" || calls != 2 {
		t.Fatal("可信图片重定向失败或产生额外请求", calls, err)
	}
	if cookies := c.HTTP.Jar.Cookies(start); len(cookies) != 1 || cookies[0].Name != "UID" {
		t.Fatal("附件下载修改了 API Cookie Jar")
	}
}
