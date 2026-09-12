package p115

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

var defaultAESKey = []byte{0xfb, 0x1a, 0x19, 0xd6, 0x52, 0xf5, 0xaa, 0xf7, 0xbc, 0x65, 0x1d, 0x0f, 0x69, 0xbf, 0x42, 0x2f}
var defaultAESIV = []byte{0x69, 0xbf, 0x42, 0x2f, 0x49, 0x96, 0x05, 0x50, 0xa0, 0xad, 0x44, 0xec, 0x34, 0x46, 0xcb, 0x4c}

// AESEncryptCBC 使用 115 的 AES-CBC 参数加密数据。
func AESEncryptCBC(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(defaultAESKey)
	if err != nil {
		return nil, err
	}
	// 115 对已经按块对齐的数据不再追加整块填充。
	pad := (aes.BlockSize - len(data)%aes.BlockSize) % aes.BlockSize
	padded := make([]byte, len(data)+pad)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, defaultAESIV).CryptBlocks(out, padded)
	return out, nil
}

// AESDecryptCBC 解密 115 的 AES-CBC 数据并按原协议移除不足一块的填充。
func AESDecryptCBC(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(defaultAESKey)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("无效的 AES 数据长度")
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, defaultAESIV).CryptBlocks(out, data)
	pad := int(out[len(out)-1])
	if pad == 0 || pad >= aes.BlockSize {
		return out, nil
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return out, nil
		}
	}
	return out[:len(out)-pad], nil
}
