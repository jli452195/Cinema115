package p115

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math/big"
)

// p115CipherTable 是 115 RSA/XOR 密钥派生表。
var p115CipherTable = []byte{0xf0, 0xe5, 0x69, 0xae, 0xbf, 0xdc, 0xbf, 0x8a, 0x1a, 0x45, 0xe8, 0xbe, 0x7d, 0xa6, 0x73, 0xb8, 0xde, 0x8f, 0xe7, 0xc4, 0x45, 0xda, 0x86, 0xc4, 0x9b, 0x64, 0x8b, 0x14, 0x6a, 0xb4, 0xf1, 0xaa, 0x38, 0x01, 0x35, 0x9e, 0x26, 0x69, 0x2c, 0x86, 0x00, 0x6b, 0x4f, 0xa5, 0x36, 0x34, 0x62, 0xa6, 0x2a, 0x96, 0x68, 0x18, 0xf2, 0x4a, 0xfd, 0xbd, 0x6b, 0x97, 0x8f, 0x4d, 0x8f, 0x89, 0x13, 0xb7, 0x6c, 0x8e, 0x93, 0xed, 0x0e, 0x0d, 0x48, 0x3e, 0xd7, 0x2f, 0x88, 0xd8, 0xfe, 0xfe, 0x7e, 0x86, 0x50, 0x95, 0x4f, 0xd1, 0xeb, 0x83, 0x26, 0x34, 0xdb, 0x66, 0x7b, 0x9c, 0x7e, 0x9d, 0x7a, 0x81, 0x32, 0xea, 0xb6, 0x33, 0xde, 0x3a, 0xa9, 0x59, 0x34, 0x66, 0x3b, 0xaa, 0xba, 0x81, 0x60, 0x48, 0xb9, 0xd5, 0x81, 0x9c, 0xf8, 0x6c, 0x84, 0x77, 0xff, 0x54, 0x78, 0x26, 0x5f, 0xbe, 0xe8, 0x1e, 0x36, 0x9f, 0x34, 0x80, 0x5c, 0x45, 0x2c, 0x9b, 0x76, 0xd5, 0x1b, 0x8f, 0xcc, 0xc3, 0xb8, 0xf5}

var p115RSAModulus, _ = new(big.Int).SetString("8686980c0f5a24c4b9d43020cd2c22703ff3f450756529058b1cf88f09b8602136477198a6e2683149659bd122c33592fdb5ad47944ad1ea4d36c6b172aad6338c3bb6ac6227502d010993ac967d1aef00f0c8e038de2e4d3bc2ec368af2e9f10a6f1eda4f7262f136420c07c331b871bf139f74f3010e3c4fe57df3afb71683", 16)
var p115RSAExponent = big.NewInt(0x10001)
var p115RSAKey = []byte{0x8d, 0xa5, 0xa5, 0x8d}
var p115GKeyL = []byte{'x', 0x06, 0xad, 'L', '3', 0x86, ']', 0x18, 'L', 0x01, '?', 'F'}

// p115XORKey 根据 115 客户端算法派生密钥。
func p115XORKey(randKey []byte, size int) []byte {
	key := make([]byte, size)
	for i := 0; i < size; i++ {
		key[i] = p115CipherTable[size*(size-1-i)] ^ (randKey[i] + p115CipherTable[i*size])
	}
	return key
}

// RSAEncrypt 按 115 协议完成固定密钥 RSA 加密并返回 Base64 数据。
func RSAEncrypt(data []byte) string {
	plain := p115XOR(data, p115RSAKey)
	for i, j := 0, len(plain)-1; i < j; i, j = i+1, j-1 {
		plain[i], plain[j] = plain[j], plain[i]
	}
	buf := append(make([]byte, 16), p115XOR(plain, p115GKeyL)...)
	out := make([]byte, 0, ((len(buf)+116)/117)*128)
	for start := 0; start < len(buf); start += 117 {
		end := start + 117
		if end > len(buf) {
			end = len(buf)
		}
		block := make([]byte, 128)
		block[0] = 0
		for i := 1; i < 127-(end-start); i++ {
			block[i] = 2
		}
		copy(block[128-(end-start):], buf[start:end])
		n := new(big.Int).SetBytes(block)
		out = append(out, n.Exp(n, p115RSAExponent, p115RSAModulus).FillBytes(make([]byte, 128))...)
	}
	return base64.StdEncoding.EncodeToString(out)
}

// RSADecrypt 按 115 协议解码 Base64 RSA 数据。
func RSADecrypt(encoded string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%128 != 0 {
		return nil, fmt.Errorf("RSA 数据必须包含完整的 128 字节分块")
	}
	plain := make([]byte, 0, len(ciphertext))
	for start := 0; start+128 <= len(ciphertext); start += 128 {
		n := new(big.Int).SetBytes(ciphertext[start : start+128])
		if n.Cmp(p115RSAModulus) >= 0 {
			return nil, fmt.Errorf("RSA 分块超出模数范围")
		}
		block := n.Exp(n, p115RSAExponent, p115RSAModulus).FillBytes(make([]byte, 128))
		separator := bytes.IndexByte(block[2:], 0) + 2
		if block[0] != 0 || (block[1] != 1 && block[1] != 2) || separator < 10 {
			return nil, fmt.Errorf("RSA 分块填充无效")
		}
		plain = append(plain, block[separator+1:]...)
	}
	if len(plain) < 16 {
		return nil, fmt.Errorf("无效的 RSA 数据")
	}
	randKey := plain[:16]
	key := p115XORKey(randKey, 12)
	body := p115XOR(plain[16:], key)
	for i, j := 0, len(body)-1; i < j; i, j = i+1, j-1 {
		body[i], body[j] = body[j], body[i]
	}
	return p115XOR(body, p115RSAKey), nil
}

// 115 会先处理不足四字节的前缀，再从密钥开头循环处理余下数据。
func p115XOR(src, key []byte) []byte {
	out := make([]byte, len(src))
	prefix := len(src) % 4
	for i := 0; i < prefix; i++ {
		out[i] = src[i] ^ key[i]
	}
	for i := prefix; i < len(src); i++ {
		out[i] = src[i] ^ key[(i-prefix)%len(key)]
	}
	return out
}
