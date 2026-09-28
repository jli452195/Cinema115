package p115

import (
	"strings"
	"testing"
)

func TestLZ4LiteralBlocks(t *testing.T) {
	// 两个仅含字面量的合法 LZ4 分块，以及零长度终止符。
	got, err := LZ4Decompress([]byte{4, 0, 0x30, 'a', 'b', 'c', 3, 0, 0x20, 'd', 'e', 0, 0})
	if err != nil || string(got) != "abcde" {
		t.Fatalf("got %q, err %v", got, err)
	}
	for _, raw := range [][]byte{{1}, {4, 0, 1}, {2, 0, 0xff, 0xff}} {
		if _, err := LZ4Decompress(raw); err == nil {
			t.Fatal("损坏分块被接受")
		}
	}
}

func TestInstantLZ4ShortTailRequiresCompleteJSON(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		tail        []byte
		ok          bool
	}{
		{"one-byte-tail", `{"status":2}`, []byte{0x81}, true},
		{"two-byte-tail", `{"status":2}`, []byte{0xff, 0xff}, true},
		{"incomplete-json", `{"status":`, []byte{0xff}, false},
		{"truncated-block", `{"status":2}`, []byte{4, 0, 0x30}, false},
		{"corrupt-block", `{"status":2}`, []byte{2, 0, 0xff, 0xff}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := append([]byte{byte(len(tc.value) << 4)}, []byte(tc.value)...)
			raw := append([]byte{byte(len(block)), 0}, block...)
			raw = append(raw, tc.tail...)
			out, err := decompressInstantLZ4(raw)
			if (err == nil) != tc.ok {
				t.Fatal("unexpected acceptance", err)
			}
			if tc.ok && string(out) != tc.value {
				t.Fatal("changed JSON")
			}
			if _, err := LZ4Decompress(raw); err == nil {
				t.Fatal("generic decoder was loosened")
			}
			if err != nil && !strings.Contains(err.Error(), "分块=") {
				t.Fatal("missing diagnostics", err)
			}
		})
	}
}
