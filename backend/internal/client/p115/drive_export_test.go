package p115

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"strings"
	"testing"

	unicodeencoding "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func driveExportText(t *testing.T, text string, order unicodeencoding.Endianness) []byte {
	t.Helper()
	raw, _, err := transform.Bytes(unicodeencoding.UTF16(order, unicodeencoding.UseBOM).NewEncoder(), []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDriveExportTreeRelativeLeaves(t *testing.T) {
	for _, order := range []unicodeencoding.Endianness{unicodeencoding.LittleEndian, unicodeencoding.BigEndian} {
		text := "|——根目录\r\n| |-电影与剧集\r\n| | |-文件夹.mkv\r\n| | | |-小明\\'s 第01集.mkv\r\n| | | |-中文字幕.ass\r\n| | |-Season 02\r\n| | | |-02.mp4\r\n| | |-电影.strm\r\n"
		paths := make([]string, 0, 4)
		err := parseDriveExportTree(context.Background(), bytes.NewReader(driveExportText(t, text, order)), "电影与剧集", func(p string) error {
			paths = append(paths, p)
			return nil
		})
		want := []string{"文件夹.mkv/小明's 第01集.mkv", "文件夹.mkv/中文字幕.ass", "Season 02/02.mp4", "电影.strm"}
		if err != nil || !reflect.DeepEqual(paths, want) {
			t.Fatalf("paths=%q err=%v", paths, err)
		}
	}
}

func TestDriveExportTreeRejectsIncompleteAndAmbiguousPaths(t *testing.T) {
	prefix := "|——根目录\n| |-来源\n"
	tests := map[string]string{
		"empty":            "",
		"header-only":      "|——根目录\n",
		"empty-root":       prefix,
		"wrong-header":     "|--another\n| |-来源\n| | |-01.mkv\n",
		"wrong-source":     "|——根目录\n| |-其他来源\n| | |-01.mkv\n",
		"multiple-sources": prefix + "| | |-01.mkv\n| |-其他来源\n",
		"depth-jump":       prefix + "| | | |-01.mkv\n",
		"invalid-depth":    prefix + strings.Repeat("| ", 66) + "|-01.mkv\n",
		"traversal":        prefix + "| | |-..\n",
		"forward-slash":    prefix + "| | |-a/b.mkv\n",
		"backslash":        prefix + "| | |-a\\b.mkv\n",
		"control":          prefix + "| | |-a\x00b.mkv\n",
		"multiline-name":   prefix + "| | |-a\ncontinued.mkv\n",
		"invalid-unicode":  prefix + "| | |-\uFFFD.mkv\n",
		"line-overflow":    prefix + "| | |-" + strings.Repeat("x", 64<<10) + ".mkv\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			err := parseDriveExportTree(context.Background(), bytes.NewReader(driveExportText(t, input, unicodeencoding.LittleEndian)), "来源", func(string) error { return nil })
			if !errors.Is(err, ErrDriveTreeExportUnsupported) {
				t.Fatalf("expected full-list fallback: %v", err)
			}
		})
	}
	for _, data := range [][]byte{[]byte(prefix), {0xff, 0xfe, 0x7c}} {
		if err := parseDriveExportTree(context.Background(), bytes.NewReader(data), "来源", func(string) error { return nil }); !errors.Is(err, ErrDriveTreeExportUnsupported) {
			t.Fatalf("accepted invalid UTF-16: %v", err)
		}
	}
}

func TestDriveExportTreeNestedSourceUsesRealParentHeader(t *testing.T) {
	text := "|——露蒂的玩具 (2011) {tmdb-42508}\n| |-Season 00\n| | |-露蒂的玩具 - S00E01 - ReinForcee.mkv\n"
	paths := make([]string, 0, 1)
	err := parseDriveExportTree(context.Background(), bytes.NewReader(driveExportText(t, text, unicodeencoding.LittleEndian)), "Season 00", func(p string) error { paths = append(paths, p); return nil })
	if err != nil || !reflect.DeepEqual(paths, []string{"露蒂的玩具 - S00E01 - ReinForcee.mkv"}) {
		t.Fatalf("nested source paths=%q err=%v", paths, err)
	}
}

func TestDriveExportTreePreservesCancellationAndCallbackFailure(t *testing.T) {
	data := driveExportText(t, "|——根目录\n| |-来源\n| | |-01.mkv\n| | |-02.mkv\n", unicodeencoding.LittleEndian)
	want := errors.New("store failed")
	if err := parseDriveExportTree(context.Background(), bytes.NewReader(data), "来源", func(string) error { return want }); !errors.Is(err, want) {
		t.Fatalf("lost callback failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := parseDriveExportTree(ctx, bytes.NewReader(data), "来源", func(string) error { t.Fatal("visited after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestDriveTreeExportSubmitDoesNotReplayWrites(t *testing.T) {
	for _, status := range []int{200, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := newTestShareClient()
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Method != http.MethodPost || r.URL.Path != "/files/export_dir" || r.URL.RawQuery != "" || r.Form.Get("file_ids") != "3516637914517734609" || r.Form.Get("target") != "U_1_0" || len(r.Form) != 2 || r.Header.Get("Cookie") != "fixed-cookie" {
					t.Fatalf("unexpected export form: %v", r.Form)
				}
				return instantResponse(status, []byte(`{"state":true,"data":{"export_id":"12345"}}`)), nil
			})
			id, err := client.StartDriveTreeExport(context.Background(), "3516637914517734609", "fixed-cookie")
			if calls != 1 || (err == nil) != (status == 200) || status == 200 && id != "12345" {
				t.Fatalf("calls=%d id=%s err=%v", calls, id, err)
			}
		})
	}
}

func TestDriveTreeExportStatusAndScopedCleanup(t *testing.T) {
	tests := []struct {
		name, data string
		pending    bool
		invalid    bool
	}{
		{"null", `null`, true, false},
		{"empty-array", `[]`, true, false},
		{"empty-object", `{}`, true, false},
		{"pending-id", `{"export_id":12345}`, true, false},
		{"complete", `{"export_id":"12345","file_id":"3516637914517734609","file_name":"来源_目录树.txt","pick_code":"abc123"}`, false, false},
		{"wrong-job", `{"export_id":"999","file_id":"1","file_name":"来源.txt","pick_code":"abc123"}`, false, true},
		{"missing-job", `{"file_id":"1","file_name":"来源.txt","pick_code":"abc123"}`, false, true},
		{"partial", `{"export_id":"12345","file_name":"来源.txt"}`, false, true},
		{"invalid-file", `{"export_id":"12345","file_id":"0","file_name":"来源.txt","pick_code":"abc123"}`, false, true},
		{"wrong-type", `{"export_id":"12345","file_id":"1","file_name":"video.mkv","pick_code":"abc123"}`, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deleted := ""
			lookups := 0
			client := newTestShareClient()
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Cookie") != "fixed-cookie" {
					t.Fatal("credentials changed")
				}
				if r.Method == http.MethodGet && r.URL.Path == "/files/export_dir" && r.URL.Query().Get("export_id") == "12345" {
					lookups++
					return instantResponse(200, []byte(`{"state":true,"data":`+tt.data+`}`)), nil
				}
				if r.Method != http.MethodPost || r.URL.Path != "/rb/delete" || lookups != 1 {
					t.Fatal("cleanup did not verify its own job first")
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				deleted = r.Form.Get("fid")
				return instantResponse(200, []byte(`{"state":true}`)), nil
			})
			err := client.DeleteDriveTreeExport(context.Background(), "12345", "fixed-cookie")
			switch {
			case tt.pending:
				if !errors.Is(err, ErrDriveTreeExportPending) || deleted != "" {
					t.Fatalf("pending job: deleted=%s err=%v", deleted, err)
				}
			case tt.invalid:
				if err == nil || deleted != "" {
					t.Fatalf("invalid job: deleted=%s err=%v", deleted, err)
				}
			default:
				if err != nil || deleted != "3516637914517734609" {
					t.Fatalf("cleanup: deleted=%s err=%v", deleted, err)
				}
			}
		})
	}
}

func TestDriveTreeExportDownloadBoundsAndCredentials(t *testing.T) {
	data := driveExportText(t, "|——根目录\n| |-来源\n| | |-01.mkv\n", unicodeencoding.LittleEndian)
	for _, kind := range []string{"success", "unknown-size", "truncated", "oversize", "wrong-length", "http-error", "redirect", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			client := newTestShareClient()
			client.HTTP.Jar, _ = cookiejar.New(nil)
			cdn, _ := url.Parse("https://cdn.115cdn.com/tree")
			client.HTTP.Jar.SetCookies(cdn, []*http.Cookie{{Name: "UID", Value: "secret"}})
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.UserAgent() != PlaybackUserAgent {
					t.Fatal("CDN request leaked credentials or used wrong UA")
				}
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				body := data
				if kind == "truncated" {
					body = body[:len(body)-4]
				}
				if kind == "oversize" {
					body = append(append([]byte(nil), data...), 0, 0)
				}
				resp := instantResponse(200, body)
				resp.ContentLength = int64(len(data))
				resp.Request = r
				switch kind {
				case "unknown-size", "oversize":
					resp.ContentLength = -1
				case "wrong-length":
					resp.ContentLength++
				case "http-error":
					resp.StatusCode = 503
				case "redirect":
					resp.StatusCode = 302
					resp.Header.Set("Location", "http://127.0.0.1/private")
				}
				return resp, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			paths := make([]string, 0, 1)
			err := client.readDriveTreeExportURL(ctx, "https://cdn.115cdn.com/tree?token=private", int64(len(data)), "来源", func(p string) error { paths = append(paths, p); return nil })
			if kind == "success" || kind == "unknown-size" {
				if err != nil || !reflect.DeepEqual(paths, []string{"01.mkv"}) {
					t.Fatalf("paths=%v err=%v", paths, err)
				}
			} else if err == nil {
				t.Fatal("unsafe response accepted")
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "token=private") || calls > 1 {
				t.Fatal("leaked signed URL or followed forbidden redirect")
			}
		})
	}
}

func TestDriveTreeExportReadRejectsChangedJob(t *testing.T) {
	client := newTestShareClient()
	calls := 0
	client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/files/export_dir" {
			t.Fatal("requested download after export identity changed")
		}
		return instantResponse(200, []byte(`{"state":true,"data":{"export_id":"12345","file_id":"999","file_name":"tree.txt","pick_code":"abc123"}}`)), nil
	})
	err := client.ReadDriveTreeExport(context.Background(), DriveTreeExport{ID: "12345", FileID: "100", PickCode: "abc123", Name: "tree.txt"}, "来源", "fixed-cookie", func(string) error { t.Fatal("unexpected visit"); return nil })
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestDriveTreeExportDownloadRejectsInvalidLimitsBeforeRequest(t *testing.T) {
	client := newTestShareClient()
	client.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network request"); return nil, io.EOF })
	for _, size := range []int64{-1, 0, maxDriveTreeBytes + 1} {
		if err := client.readDriveTreeExportURL(context.Background(), "https://cdn.115cdn.com/tree", size, "来源", func(string) error { return nil }); err == nil {
			t.Fatal("accepted invalid size", size)
		}
	}
	if err := client.readDriveTreeExportURL(context.Background(), "http://127.0.0.1/tree", 1, "来源", func(string) error { return nil }); err == nil {
		t.Fatal("accepted invalid host")
	}
}
