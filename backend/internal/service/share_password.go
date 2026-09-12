package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
)

var sharePasswordPattern = regexp.MustCompile(`^[a-zA-Z0-9]{4}$`)

// 未设置固定密码时，每次使用密码学随机源生成新的四位分享密码。
func newSharePassword(fixed string) (string, error) {
	if fixed != "" {
		return fixed, nil
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var password [4]byte
	limit := big.NewInt(int64(len(alphabet)))
	for i := range password {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("生成随机分享密码: %w", err)
		}
		password[i] = alphabet[n.Int64()]
	}
	return string(password[:]), nil
}
