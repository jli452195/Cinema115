// Package strm defines the shared wire format used by generation and playback.
package strm

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const ShareFormat = "share115-v2-required-password"
const SharePrefix = "/api/share115/"

var sharePath = regexp.MustCompile(`^/api/share115/([a-zA-Z0-9]{1,128})/([a-fA-F0-9]{40})/([a-zA-Z0-9]{1,32})/([1-9][0-9]{0,19})$`)
var shareCode = regexp.MustCompile(`^[a-zA-Z0-9]{1,128}$`)
var receiveCode = regexp.MustCompile(`^[a-zA-Z0-9]{1,32}$`)
var fileID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func ValidatePassword(password string) error {
	if !receiveCode.MatchString(password) {
		return fmt.Errorf("分享提取码不能为空，且只能包含 1 至 32 位字母或数字")
	}
	return nil
}

type Share struct {
	Code, SHA1, Password, FileID string
}

func ParseSharePath(path string) (Share, error) {
	parts := sharePath.FindStringSubmatch(path)
	if parts == nil {
		return Share{}, fmt.Errorf("STRM 分享路径须为 /api/share115/分享码/40位SHA1/提取码/文件ID；提取码不能为空")
	}
	if _, err := strconv.ParseUint(parts[4], 10, 64); err != nil {
		return Share{}, fmt.Errorf("STRM 文件 ID 超出范围: %w", err)
	}
	return Share{Code: parts[1], SHA1: strings.ToUpper(parts[2]), Password: parts[3], FileID: parts[4]}, nil
}

func SharePath(value Share) (string, error) {
	if err := ValidatePassword(value.Password); err != nil {
		return "", err
	}
	path := SharePrefix + value.Code + "/" + strings.ToUpper(value.SHA1) + "/" + value.Password + "/" + value.FileID
	if _, err := ParseSharePath(path); err != nil {
		return "", err
	}
	return path, nil
}

// ParseShareQuery reads existing STRM files only. Extras cannot override identity
// or provide a trusted SHA1; malformed encoding is rejected for the whole query.
func ParseShareQuery(raw string) (Share, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return Share{}, fmt.Errorf("STRM 分享参数编码无效")
	}
	for _, key := range []string{"share_code", "id", "receive_code"} {
		if len(q[key]) != 1 {
			return Share{}, fmt.Errorf("STRM 参数 %s 必须且只能出现一次", key)
		}
	}
	value := Share{Code: q.Get("share_code"), Password: q.Get("receive_code"), FileID: q.Get("id")}
	if err := ValidatePassword(value.Password); err != nil {
		return Share{}, err
	}
	if !shareCode.MatchString(value.Code) || !fileID.MatchString(value.FileID) {
		return Share{}, fmt.Errorf("STRM 分享码或文件 ID 格式无效")
	}
	if _, err := strconv.ParseUint(value.FileID, 10, 64); err != nil {
		return Share{}, fmt.Errorf("STRM 文件 ID 超出范围: %w", err)
	}
	return value, nil
}
