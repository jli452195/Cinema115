package p115

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// LZ4Decompress 解压 115 使用的长度前缀 LZ4 分块数据。
func LZ4Decompress(src []byte) ([]byte, error) {
	return decompressLZ4(src, false)
}

// Only initupload permits the short tail used by p115cipher. Require a full
// JSON value before tolerating it; never accept a truncated compressed block.
func decompressInstantLZ4(src []byte) ([]byte, error) {
	return decompressLZ4(src, true)
}

func decompressLZ4(src []byte, jsonTail bool) ([]byte, error) {
	var out []byte
	offset, index := 0, 0
	for len(src) > 0 {
		index++
		failure := func(reason string, declared int) error {
			return fmt.Errorf("%s（分块=%d，偏移=%d，声明长度=%d，剩余字节=%d，已解压字节=%d）", reason, index, offset, declared, len(src), len(out))
		}
		if jsonTail && len(src) <= 2 && json.Valid(out) {
			return out, nil
		}
		if len(src) < 2 {
			return nil, failure("LZ4 分块头不完整", -1)
		}
		n := int(binary.LittleEndian.Uint16(src[:2]))
		src = src[2:]
		if n == 0 {
			return out, nil
		}
		if n > len(src) {
			return nil, failure("LZ4 分块截断", n)
		}
		block := src[:n]
		dst := make([]byte, 0x2000)
		decoded, err := lz4.UncompressBlock(block, dst)
		if err != nil {
			return nil, failure("LZ4 压缩块无效或解压超过 8192 字节", n)
		}
		if decoded == 0 {
			return nil, failure("LZ4 分块为空或无效", n)
		}
		if len(out)+decoded > 16<<20 {
			return nil, fmt.Errorf("LZ4 解压结果超过限制")
		}
		out = append(out, dst[:decoded]...)
		src = src[n:]
		offset += 2 + n
	}
	return out, nil
}
