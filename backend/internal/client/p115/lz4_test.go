package p115

import "testing"

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
