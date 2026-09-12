package p115

import (
	"encoding/binary"
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// LZ4Decompress 解压 115 使用的长度前缀 LZ4 分块数据。
func LZ4Decompress(src []byte) ([]byte, error) {
	var out []byte
	for len(src) > 0 {
		if len(src) < 2 {
			return nil, fmt.Errorf("LZ4 分块长度不完整")
		}
		n := int(binary.LittleEndian.Uint16(src[:2]))
		src = src[2:]
		if n == 0 {
			return out, nil
		}
		if n > len(src) {
			return nil, fmt.Errorf("LZ4 分块长度无效")
		}
		block := src[:n]
		src = src[n:]
		dst := make([]byte, 0x2000)
		decoded, err := lz4.UncompressBlock(block, dst)
		if err != nil {
			return nil, err
		}
		if decoded == 0 {
			return nil, fmt.Errorf("LZ4 分块为空或无效")
		}
		if len(out)+decoded > 16<<20 {
			return nil, fmt.Errorf("LZ4 解压结果超过限制")
		}
		out = append(out, dst[:decoded]...)
	}
	return out, nil
}
