package torrentmeta

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
)

func encode(tb testing.TB, value any) []byte {
	tb.Helper()
	raw, err := bencode.Marshal(value)
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func singleInfo() map[string]any {
	return map[string]any{"name": "episode.mkv", "length": int64(1), "piece length": int64(1), "pieces": strings.Repeat("p", 20)}
}

func multiInfo(files ...any) map[string]any {
	return map[string]any{"name": "Series", "files": files, "piece length": int64(math.MaxInt64), "pieces": strings.Repeat("p", 20)}
}

func fileInfo(size int64, path ...string) map[string]any {
	return map[string]any{"length": size, "path": path}
}

func torrent(tb testing.TB, info map[string]any) []byte {
	tb.Helper()
	return encode(tb, map[string]any{"info": info})
}

func TestParseManifest(t *testing.T) {
	large := singleInfo()
	large["length"], large["piece length"] = int64(9007199254740993), int64(math.MaxInt64)
	utf8 := multiInfo(fileInfo(1, "legacy", "ep01.mkv"))
	utf8["name.utf-8"] = "动画全集"
	utf8["files"].([]any)[0].(map[string]any)["path.utf-8"] = []string{"正片", "第一集.mkv"}
	for _, tc := range []struct {
		name  string
		info  map[string]any
		root  string
		files []File
	}{
		{"single exact int64", large, "episode.mkv", []File{{"episode.mkv", 9007199254740993}}},
		{"original order and all file types", multiInfo(
			fileInfo(4, "Re:Zero", "episode02.mkv"), fileInfo(3, "Subs", "episode02.zh.ass"),
			fileInfo(2, "episode01.mp4"), fileInfo(0, "empty.txt"), fileInfo(1, "disc.iso"),
		), "Series", []File{{"Re:Zero/episode02.mkv", 4}, {"Subs/episode02.zh.ass", 3}, {"episode01.mp4", 2}, {"empty.txt", 0}, {"disc.iso", 1}}},
		{"utf8 aliases", utf8, "动画全集", []File{{"正片/第一集.mkv", 1}}},
		{"one-file directory", multiInfo(fileInfo(1, "Series")), "Series", []File{{"Series", 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := torrent(t, tc.info)
			got, err := Parse(raw)
			_, multi := tc.info["files"]
			if err != nil || got.Name != tc.root || got.MultiFile != multi || !reflect.DeepEqual(got.Files, tc.files) {
				t.Fatalf("manifest = %+v, err = %v", got, err)
			}
			// Returned paths must not borrow mutable input storage.
			for i := range raw {
				raw[i] = 0
			}
			if got.Name != tc.root || !reflect.DeepEqual(got.Files, tc.files) {
				t.Fatal("manifest aliases input")
			}
		})
	}
}

func TestParseHashesOriginalInfo(t *testing.T) {
	info := singleInfo()
	// Unknown fields participate in the hash and must survive without re-encoding.
	info["vendor extension"] = map[string]any{"value": int64(123)}
	rawInfo := encode(t, info)
	want := sha1.Sum(rawInfo)
	for _, root := range []map[string]any{
		{"info": bencode.Bytes(rawInfo)},
		{"announce": "https://example.invalid/tracker", "comment": "different envelope", "info": bencode.Bytes(rawInfo)},
	} {
		got, err := Parse(encode(t, root))
		if err != nil || got.InfoHash != hex.EncodeToString(want[:]) {
			t.Fatalf("original info hash = %q, err = %v", got.InfoHash, err)
		}
	}
}

func TestParseRejectsInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing name", func(i map[string]any) { delete(i, "name") }},
		{"missing pieces", func(i map[string]any) { delete(i, "pieces") }},
		{"missing piece length", func(i map[string]any) { delete(i, "piece length") }},
		{"missing length and files", func(i map[string]any) { delete(i, "length") }},
		{"both length and files", func(i map[string]any) { i["files"] = []any{fileInfo(1, "a")} }},
		{"zero length with files", func(i map[string]any) { i["length"], i["files"] = int64(0), []any{fileInfo(1, "a")} }},
		{"zero total", func(i map[string]any) { i["length"] = int64(0) }},
		{"negative length", func(i map[string]any) { i["length"] = int64(-1) }},
		{"zero piece length", func(i map[string]any) { i["piece length"] = int64(0) }},
		{"negative piece length", func(i map[string]any) { i["piece length"] = int64(-1) }},
		{"partial hash", func(i map[string]any) { i["pieces"] = "partial" }},
		{"no hashes", func(i map[string]any) { i["pieces"] = "" }},
		{"wrong piece count", func(i map[string]any) { i["length"] = int64(2) }},
		{"empty name", func(i map[string]any) { i["name"] = "" }},
		{"list wrapped name", func(i map[string]any) { i["name"] = []string{"episode.mkv"} }},
		{"list wrapped length", func(i map[string]any) { i["length"] = []int64{1} }},
		{"list wrapped piece length", func(i map[string]any) { i["piece length"] = []int64{1} }},
		{"list wrapped pieces", func(i map[string]any) { i["pieces"] = []string{strings.Repeat("p", 20)} }},
		{"root traversal", func(i map[string]any) { i["name"] = ".." }},
		{"root absolute", func(i map[string]any) { i["name"] = "/tmp/a" }},
		{"utf8 root traversal", func(i map[string]any) { i["name.utf-8"] = "../a" }},
		{"list wrapped utf8 name", func(i map[string]any) { i["name.utf-8"] = []string{"episode.mkv"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := singleInfo()
			tc.change(info)
			if got, err := Parse(torrent(t, info)); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(got, Metadata{}) {
				t.Fatalf("invalid metadata returned %+v, %v", got, err)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		files []any
	}{
		{"empty files", []any{}},
		{"file not dictionary", []any{[]any{fileInfo(1, "a")}}},
		{"missing file length", []any{map[string]any{"path": []string{"a"}}}},
		{"negative file size", []any{fileInfo(-1, "a")}},
		{"overflow total", []any{fileInfo(math.MaxInt64, "a"), fileInfo(1, "b")}},
		{"missing path", []any{map[string]any{"length": int64(1)}}},
		{"string path", []any{map[string]any{"length": int64(1), "path": "a"}}},
		{"empty path", []any{fileInfo(1)}},
		{"empty component", []any{fileInfo(1, "")}},
		{"dot component", []any{fileInfo(1, ".", "a")}},
		{"traversal", []any{fileInfo(1, "..", "a")}},
		{"absolute path", []any{fileInfo(1, "/tmp", "a")}},
		{"backslash", []any{fileInfo(1, "..\\a")}},
		{"windows drive", []any{fileInfo(1, "C:", "a")}},
		{"nul", []any{fileInfo(1, "a\x00.mkv")}},
		{"nested path list", []any{map[string]any{"length": int64(1), "path": []any{[]string{"a"}}}}},
		{"duplicate paths", []any{fileInfo(1, "a"), fileInfo(1, "a")}},
		{"unsafe utf8 path", []any{map[string]any{"length": int64(1), "path": []string{"a"}, "path.utf-8": []string{"..", "a"}}}},
		{"duplicate utf8 paths", []any{
			map[string]any{"length": int64(1), "path": []string{"a"}, "path.utf-8": []string{"same"}},
			map[string]any{"length": int64(1), "path": []string{"b"}, "path.utf-8": []string{"same"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(torrent(t, multiInfo(tc.files...))); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid file list: %v", err)
			}
		})
	}
}

func TestParseUnsupportedVersions(t *testing.T) {
	for _, version := range []int64{0, 1, 2, 3} {
		info := singleInfo()
		info["meta version"] = version
		if _, err := Parse(torrent(t, info)); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("hybrid/meta version %d: %v", version, err)
		}
	}
	info := singleInfo()
	delete(info, "length")
	delete(info, "pieces")
	info["meta version"] = int64(2)
	info["file tree"] = map[string]any{"a": map[string]any{"": map[string]any{"length": int64(1)}}}
	if _, err := Parse(torrent(t, info)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("v2: %v", err)
	}
}

func TestEncodingBounds(t *testing.T) {
	valid := torrent(t, singleInfo())
	for _, raw := range [][]byte{
		nil, []byte("<html>error</html>"), append(append([]byte{}, valid...), 'x'), valid[:len(valid)-1],
		[]byte("d4:infoi1ee"), []byte("d4:infodee"),
		[]byte(strings.Replace(string(valid), "i1e", "i01e", 1)),
		[]byte(strings.Replace(string(valid), "i1e", "i-0e", 1)),
		[]byte(strings.Replace(string(valid), "i1e", "i9223372036854775808e", 1)),
		[]byte(strings.Replace(string(valid), "4:name", "04:name", 1)),
		[]byte(strings.Replace(string(valid), "6:lengthi1e", "6:lengthi1e6:lengthi1e", 1)),
		[]byte(strings.Replace(string(valid), "6:lengthi1e4:name11:episode.mkv", "4:name11:episode.mkv6:lengthi1e", 1)),
		[]byte("d0:i1e0:i2ee"), []byte(strings.Repeat("l", 34) + strings.Repeat("e", 34)),
		[]byte("l" + strings.Repeat("i0e", 32768) + "e"), make([]byte, MaxBytes+1),
	} {
		if _, err := Parse(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid encoding accepted (length %d): %v", len(raw), err)
		}
	}
	for _, raw := range []string{
		strings.Repeat("l", 33) + strings.Repeat("e", 33),
		"l" + strings.Repeat("i0e", 32767) + "e",
	} {
		if err := checkEncoding([]byte(raw)); err != nil {
			t.Fatalf("valid limit boundary rejected: %v", err)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(torrent(f, singleInfo()))
	f.Add(torrent(f, multiInfo(fileInfo(2, "a.mkv"), fileInfo(1, "Subs", "a.ass"))))
	f.Add([]byte("d4:infodee"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := Parse(raw)
		if err == nil && (len(got.InfoHash) != 40 || got.Name == "" || len(got.Files) == 0) {
			t.Fatalf("incomplete successful result: %+v", got)
		}
	})
}

func BenchmarkParse(b *testing.B) {
	files := make([]any, 0, 300)
	for i := 1; i <= 150; i++ {
		name := "episode" + strconv.Itoa(i)
		files = append(files, fileInfo(1<<30, name+".mkv"), fileInfo(4096, "Subs", name+".ass"))
	}
	raw := torrent(b, multiInfo(files...))
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(raw); err != nil {
			b.Fatal(err)
		}
	}
}
