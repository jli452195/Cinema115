package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTelegramTextDocumentReadsGetFilePath(t *testing.T) {
	const token = "123:fixture-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + token + "/getFile":
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || r.Method != http.MethodPost || input["file_id"] != "fixture-id" {
				t.Errorf("getFile 请求或 file_id 有误：%v", err)
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_path":"documents/file_0.txt"}}`)
		case "/file/bot" + token + "/documents/file_0.txt":
			if r.Method != http.MethodGet || r.URL.RawQuery != "" {
				t.Errorf("附件下载请求有误：%s", r.Method)
			}
			_, _ = io.WriteString(w, "\ufeffhttps://115.com/s/example\r\n提取码：abcd\r")
		default:
			t.Error("意外的 Telegram 请求路径")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	b := &Telegram{Token: token, BaseURL: server.URL, HTTP: server.Client()}
	text, err := b.readTextDocument(context.Background(), &telegramTextDocument{FileID: "fixture-id", FileName: "链接.TXT", MIMEType: "application/octet-stream"})
	if err != nil || text != "https://115.com/s/example\n提取码：abcd\n" {
		t.Fatalf("TXT 下载或换行规范化失败：%q，%v", text, err)
	}
	if (*telegramTextDocument)(nil).isText() || (&telegramTextDocument{FileName: "list.zip", MIMEType: "text/plain"}).isText() || !(&telegramTextDocument{FileName: "list", MIMEType: "Text/Plain; charset=UTF-8"}).isText() {
		t.Fatal("TXT 附件识别规则有误")
	}
}

func TestTelegramTextDocumentRejectsOversizeOrUnsafeDownload(t *testing.T) {
	const token = "123:fixture-token"
	cases := []struct {
		name      string
		fileSize  int64
		filePath  string
		remote    func(http.ResponseWriter, *http.Request)
		expected  error
		wantCalls int32
	}{
		{name: "申报大小超限", fileSize: maxTelegramTextDocumentBytes + 1, expected: errTelegramTextDocumentTooLarge},
		{name: "绝对地址", filePath: "https://example.invalid/leak.txt", expected: errTelegramTextDocumentUnavailable, wantCalls: 1},
		{name: "编码目录穿越", filePath: "documents/%2e%2e/file.txt", expected: errTelegramTextDocumentUnavailable, wantCalls: 1},
		{name: "相对目录穿越", filePath: "documents/../file.txt", expected: errTelegramTextDocumentUnavailable, wantCalls: 1},
		{name: "拒绝重定向", filePath: "documents/file.txt", expected: errTelegramTextDocumentUnavailable, wantCalls: 2, remote: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/unexpected", http.StatusFound)
		}},
		{name: "实际流超限", filePath: "documents/file.txt", expected: errTelegramTextDocumentTooLarge, wantCalls: 2, remote: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, strings.Repeat("a", maxTelegramTextDocumentBytes+1))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch r.URL.Path {
				case "/bot" + token + "/getFile":
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]string{"file_path": tc.filePath}})
				case "/file/bot" + token + "/documents/file.txt":
					if tc.remote != nil {
						tc.remote(w, r)
						return
					}
					fallthrough
				default:
					t.Error("附件发出了不该发生的下载请求")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			b := &Telegram{Token: token, BaseURL: server.URL, HTTP: server.Client()}
			_, err := b.readTextDocument(context.Background(), &telegramTextDocument{FileID: "fixture-id", FileName: "links.txt", FileSize: tc.fileSize})
			if !errors.Is(err, tc.expected) || calls.Load() != tc.wantCalls || strings.Contains(err.Error(), token) {
				t.Fatalf("错误或请求数不匹配：%v，请求 %d 次", err, calls.Load())
			}
		})
	}
}

func TestDecodeTelegramTextDocumentAcceptsUTF16WithBOM(t *testing.T) {
	for _, input := range [][]byte{
		{0xff, 0xfe, 'A', 0, '\r', 0, '\n', 0, 0x42, 0xd8, 0xb7, 0xdf},
		{0xfe, 0xff, 0, 'A', 0, '\r', 0, '\n', 0xd8, 0x42, 0xdf, 0xb7},
	} {
		text, err := decodeTelegramTextDocument(input)
		if err != nil || text != "A\n𠮷" {
			t.Fatalf("UTF-16 解码或换行规范化失败：%q，%v", text, err)
		}
	}
	for _, input := range [][]byte{{0xff, 0xfe, 'A'}, {0xfe, 0xff, 0xd8, 0x42, 0, 'A'}, {'A', 0, 'B'}, {0xff}} {
		if _, err := decodeTelegramTextDocument(input); !errors.Is(err, errTelegramTextDocumentInvalid) {
			t.Fatalf("无效文本未被拒绝：%v", err)
		}
	}
}
